// Package acp 用 Agent Client Protocol 驱动本机的编码 agent（codebuddy / pi / ...）。
//
// 官方只提供 Kotlin / Python / Rust / TypeScript 的 SDK，没有 Go 版，
// 所以这里按官方 schema（agentclientprotocol，v1）自己实现客户端：
// JSON-RPC 2.0 over ndjson，走子进程的 stdin/stdout。
// 我们只用到四个方法：initialize、session/new、session/prompt、session/update。
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// protocolVersion 是 ACP v1 的版本号。
const protocolVersion = 1

// ModelOption / ModeOption 是 agent 报告的可选项：id 用来切换，name 给人看。
type ModelOption struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type ModeOption struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Details 是 initialize / session/new 里能拿到的 agent 参数：
// 名字版本、协议能力、可用模型和模式。界面上用来展示「它是什么、默认用什么模型」。
type Details struct {
	AgentName    string        `json:"agentName,omitempty"`
	AgentVersion string        `json:"agentVersion,omitempty"`
	Protocol     int           `json:"protocol,omitempty"`
	Capabilities []string      `json:"capabilities,omitempty"`
	Models       []ModelOption `json:"models,omitempty"`
	CurrentModel string        `json:"currentModel,omitempty"`
	Modes        []ModeOption  `json:"modes,omitempty"`
	CurrentMode  string        `json:"currentMode,omitempty"`
}

// Client 是一个已经握手完成的 ACP 会话。
type Client struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	sessionID string
	stderr    *tailBuffer
	details   Details

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcReply

	// 一次只允许一个 prompt：ACP 会话本身就是串行的。
	promptMu sync.Mutex
	bufMu    sync.Mutex
	buf      strings.Builder
	tools    []string

	closeOnce sync.Once
	done      chan struct{}

	// policy 决定怎么答复 agent 的工具授权请求。
	policy Policy
	denied []string

	// model / mode 是 session/new 之后要切过去的选项；空表示用 agent 默认。
	model string
	mode  string
}

// Option 配置 Client 的行为。
type Option func(*Client)

// WithPolicy 设置工具授权策略。不设置时按 deny 处理，什么工具都不放行。
func WithPolicy(p Policy) Option {
	return func(c *Client) { c.policy = p }
}

// WithModel 在 session/new 之后切到指定模型（用 session/set_model）。
func WithModel(id string) Option {
	return func(c *Client) { c.model = strings.TrimSpace(id) }
}

// WithMode 在 session/new 之后切到指定模式（用 session/set_mode），
// 比如 CodeBuddy 的 PTC / 权限模式。
func WithMode(id string) Option {
	return func(c *Client) { c.mode = strings.TrimSpace(id) }
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

// tailBuffer 留最后几行 stderr，agent 崩了能说清原因。
type tailBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, line := range strings.Split(string(p), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		t.lines = append(t.lines, line)
		if len(t.lines) > 8 {
			t.lines = t.lines[len(t.lines)-8:]
		}
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, " | ")
}

// Start 拉起 agent 进程并完成 initialize + session/new。
func Start(ctx context.Context, command, cwd string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("agent 命令为空")
	}
	cmd := shellCommand(command)
	cmd.Dir = cwd

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := &tailBuffer{}
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 agent 失败：%w", err)
	}

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stderr:  tail,
		pending: make(map[int64]chan rpcReply),
		done:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	go c.readLoop(stdout)
	go func() {
		_ = cmd.Wait()
		c.failAll(errors.New("agent 进程已退出"))
		close(c.done)
	}()

	if err := c.handshake(ctx, cwd); err != nil {
		// 把 stderr 尾巴带上：agent 启动失败（配置坏了、没登录）时，
		// 「进程已退出」本身说明不了任何问题。
		tail := c.stderr.String()
		c.Close()
		if tail != "" {
			return nil, fmt.Errorf("%w（agent stderr：%s）", err, tail)
		}
		return nil, err
	}
	return c, nil
}

func (c *Client) handshake(ctx context.Context, cwd string) error {
	var init struct {
		ProtocolVersion int `json:"protocolVersion"`
		AgentInfo       struct {
			Name    string `json:"name"`
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"agentInfo"`
		AgentCapabilities struct {
			LoadSession        bool `json:"loadSession"`
			PromptCapabilities struct {
				Image           bool `json:"image"`
				Audio           bool `json:"audio"`
				EmbeddedContext bool `json:"embeddedContext"`
			} `json:"promptCapabilities"`
			MCPCapabilities struct {
				HTTP bool `json:"http"`
				SSE  bool `json:"sse"`
			} `json:"mcpCapabilities"`
		} `json:"agentCapabilities"`
	}
	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := c.callInto(initCtx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"clientCapabilities": map[string]any{
			// 我们不提供文件读写和终端，agent 要用就自己用它的工具。
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	}, &init); err != nil {
		return fmt.Errorf("initialize 失败：%w", err)
	}

	var session struct {
		SessionID string `json:"sessionId"`
		Models    struct {
			CurrentModelID  string `json:"currentModelId"`
			AvailableModels []struct {
				ModelID string `json:"modelId"`
				Name    string `json:"name"`
			} `json:"availableModels"`
		} `json:"models"`
		Modes struct {
			CurrentModeID  string `json:"currentModeId"`
			AvailableModes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"availableModes"`
		} `json:"modes"`
	}
	newCtx, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel2()
	if err := c.callInto(newCtx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	}, &session); err != nil {
		return fmt.Errorf("session/new 失败：%w", err)
	}
	if session.SessionID == "" {
		return errors.New("session/new 没有返回 sessionId")
	}
	c.sessionID = session.SessionID

	info := Details{
		AgentName:    init.AgentInfo.Name,
		AgentVersion: init.AgentInfo.Version,
		Protocol:     init.ProtocolVersion,
		CurrentModel: session.Models.CurrentModelID,
		CurrentMode:  session.Modes.CurrentModeID,
	}
	if info.AgentName == "" {
		info.AgentName = init.AgentInfo.Title
	}
	if init.AgentCapabilities.LoadSession {
		info.Capabilities = append(info.Capabilities, "loadSession")
	}
	if init.AgentCapabilities.PromptCapabilities.Image {
		info.Capabilities = append(info.Capabilities, "图片输入")
	}
	if init.AgentCapabilities.PromptCapabilities.Audio {
		info.Capabilities = append(info.Capabilities, "音频输入")
	}
	if init.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		info.Capabilities = append(info.Capabilities, "内嵌上下文")
	}
	if init.AgentCapabilities.MCPCapabilities.HTTP {
		info.Capabilities = append(info.Capabilities, "mcp.http")
	}
	if init.AgentCapabilities.MCPCapabilities.SSE {
		info.Capabilities = append(info.Capabilities, "mcp.sse")
	}
	for _, model := range session.Models.AvailableModels {
		if model.ModelID == "" {
			continue
		}
		name := model.Name
		if name == "" {
			name = model.ModelID
		}
		info.Models = append(info.Models, ModelOption{ID: model.ModelID, Name: name})
	}
	for _, mode := range session.Modes.AvailableModes {
		if mode.ID == "" {
			continue
		}
		name := mode.Name
		if name == "" {
			name = mode.ID
		}
		info.Modes = append(info.Modes, ModeOption{ID: mode.ID, Name: name})
	}
	c.details = info
	return c.applyOptions(ctx)
}

// applyOptions 按配置切换模型 / 模式。失败就报错——用户明确选了某个模型，
// 静默退回默认反而更让人困惑。
func (c *Client) applyOptions(ctx context.Context) error {
	if c.mode != "" {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := c.call(callCtx, "session/set_mode", map[string]any{
			"sessionId": c.sessionID,
			"modeId":    c.mode,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("切换模式 %q 失败：%w", c.mode, err)
		}
	}
	if c.model != "" {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := c.call(callCtx, "session/set_model", map[string]any{
			"sessionId": c.sessionID,
			"modelId":   c.model,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("切换模型 %q 失败：%w", c.model, err)
		}
	}
	return nil
}

// Details 返回握手时记下的 agent 参数。
func (c *Client) Details() Details { return c.details }

// Probe 启动一次 agent，读它在 initialize / session/new 里报的模型和能力，然后关掉。
// 只用于界面展示：策略设成拒绝，不跑任何工具。
func Probe(ctx context.Context, def Agent) (Details, error) {
	dir, err := os.MkdirTemp("", "gh-agent-probe-")
	if err != nil {
		return Details{}, err
	}
	defer os.RemoveAll(dir)

	client, err := Start(ctx, def.Command, dir, WithPolicy(PolicyDeny))
	if err != nil {
		return Details{}, err
	}
	defer client.Close()
	return client.Details(), nil
}

// Prompt 发一轮对话，返回 agent 的正文（流式片段拼起来）。
// onChunk 非空时边收边回调，用于界面实时显示。
func (c *Client) Prompt(ctx context.Context, text string, onChunk func(string)) (string, error) {
	c.promptMu.Lock()
	defer c.promptMu.Unlock()

	c.bufMu.Lock()
	c.buf.Reset()
	c.bufMu.Unlock()

	c.mu.Lock()
	c.tools = c.tools[:0]
	c.denied = c.denied[:0]
	c.mu.Unlock()

	_, err := c.call(ctx, "session/prompt", map[string]any{
		"sessionId": c.sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	})

	c.bufMu.Lock()
	out := strings.TrimSpace(c.buf.String())
	c.bufMu.Unlock()

	if err != nil {
		if tail := c.stderr.String(); tail != "" {
			return out, fmt.Errorf("%w（agent stderr：%s）", err, tail)
		}
		return out, err
	}
	if out == "" {
		return "", errors.New("agent 没有返回任何正文")
	}
	return out, nil
}

// Tools 返回本轮用过的工具标题，用来告诉用户它读了哪些文件。
func (c *Client) Tools() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.tools...)
}

// Close 结束会话并杀掉整个进程树。
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			if runtime.GOOS == "windows" {
				// cmd.exe 只是壳，必须连子进程一起杀，否则 node 会留着。
				_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.cmd.Process.Pid)).Run()
			} else {
				_ = c.cmd.Process.Kill()
			}
		}
	})
	return nil
}

// PID 返回底层 agent 进程的 pid；还没启动时是 0。
// 日志里带上它，用户能在任务管理器里对上是哪个进程。
func (c *Client) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *Client) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     *json.Number     `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
			Result json.RawMessage  `json:"result"`
			Error  *json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // 非 JSON 行（agent 的调试输出）直接跳过
		}

		// 响应
		if msg.ID != nil {
			if msg.Method == "" {
				c.deliver(msg.ID, msg.Result, msg.Error)
				continue
			}
			// agent 反向请求：权限按策略回，其余能力我们没声明，一律拒绝。
			if msg.Method == "session/request_permission" {
				c.handlePermission(msg.ID, msg.Params)
			} else {
				c.replyError(msg.ID, -32601, "client does not support "+msg.Method)
			}
			continue
		}

		// 通知
		if msg.Method == "session/update" {
			c.handleUpdate(msg.Params)
		}
	}
}

func (c *Client) handleUpdate(raw json.RawMessage) {
	var update struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Title string `json:"title"`
		} `json:"update"`
	}
	if err := json.Unmarshal(raw, &update); err != nil {
		return
	}
	switch update.Update.SessionUpdate {
	case "agent_message_chunk":
		if update.Update.Content.Type == "text" && update.Update.Content.Text != "" {
			c.bufMu.Lock()
			c.buf.WriteString(update.Update.Content.Text)
			c.bufMu.Unlock()
		}
	case "tool_call":
		if title := update.Update.Title; title != "" {
			c.mu.Lock()
			c.tools = append(c.tools, title)
			c.mu.Unlock()
		}
	}
}

func (c *Client) deliver(id *json.Number, result json.RawMessage, errPayload *json.RawMessage) {
	n, _ := id.Int64()
	c.mu.Lock()
	ch, ok := c.pending[n]
	delete(c.pending, n)
	c.mu.Unlock()
	if !ok {
		return
	}
	if errPayload != nil {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(*errPayload, &e)
		ch <- rpcReply{err: fmt.Errorf("ACP 错误 %d：%s", e.Code, e.Message)}
		return
	}
	ch <- rpcReply{result: result}
}

// handlePermission 答复 agent 的工具授权请求：按策略放行或拒绝。
func (c *Client) handlePermission(id *json.Number, raw json.RawMessage) {
	var req struct {
		ToolCall struct {
			Title string `json:"title"`
			Kind  string `json:"kind"`
			Meta  struct {
				ToolName string `json:"codebuddy.ai/toolName"`
			} `json:"_meta"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(raw, &req)

	pick := func(kinds ...string) string {
		for _, want := range kinds {
			for _, o := range req.Options {
				if o.Kind == want {
					return o.OptionID
				}
			}
		}
		return ""
	}

	name := req.ToolCall.Title
	if name == "" {
		name = req.ToolCall.Meta.ToolName
	}
	class := ClassifyTool(req.ToolCall.Kind, req.ToolCall.Meta.ToolName, req.ToolCall.Title)

	choice := pick("reject_once", "reject_always")
	if c.policy.Allows(class) {
		if allow := pick("allow_once", "allow_always"); allow != "" {
			choice = allow
			c.mu.Lock()
			c.tools = append(c.tools, name)
			c.mu.Unlock()
		}
	} else {
		c.mu.Lock()
		c.denied = append(c.denied, name+"（"+string(class)+"）")
		c.mu.Unlock()
	}

	if choice == "" {
		c.replyError(id, -32601, "no permission option")
		return
	}
	c.reply(id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": choice}})
}

// Denied 返回被策略拦下的工具调用，用来告诉用户"它想干什么但没让干"。
func (c *Client) Denied() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.denied...)
}

func (c *Client) reply(id *json.Number, result any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	c.mu.Lock()
	_, _ = c.stdin.Write(append(payload, '\n'))
	c.mu.Unlock()
}

func (c *Client) replyError(id *json.Number, code int, message string) {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
	c.mu.Lock()
	_, _ = c.stdin.Write(append(payload, '\n'))
	c.mu.Unlock()
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.callInto(ctx, method, params, &out)
	return out, err
}

func (c *Client) callInto(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcReply, 1)
	c.pending[id] = ch
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err == nil {
		_, err = c.stdin.Write(append(payload, '\n'))
	}
	c.mu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}

	select {
	case reply := <-ch:
		if reply.err != nil {
			return reply.err
		}
		if out == nil || len(reply.result) == 0 {
			return nil
		}
		return json.Unmarshal(reply.result, out)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// 超时就把进程杀掉，避免半截会话继续占着
		c.Close()
		return fmt.Errorf("%s 超时或被取消：%w", method, ctx.Err())
	}
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int64]chan rpcReply)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcReply{err: err}
	}
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", command)
	}
	return exec.Command("sh", "-c", command)
}
