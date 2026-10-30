// Package bootstrap 把配置、GitHub 客户端、模型客户端、状态拼成一个能跑的
// Runner。CLI（cmd/gh-agent）和桌面壳（internal/desk）共用这一份装配逻辑。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/acp"
	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/llm"
	"github.com/ayuayue/gh-agent/internal/state"
)

// agentTimeout 是本地 agent 单轮出稿的上限。读代码 + 跑命令比打模型慢得多。
const agentTimeout = 8 * time.Minute

// DefaultInstructions 是没有配置 instructions_file 时用的系统提示。
const DefaultInstructions = `你在一个软件仓库里回答 issue / PR 评论。

你只能看到下面给出的上下文：没有读文件、跑命令、联网的能力。
上下文里没有的仓库细节不要假装知道——直接说明还需要什么信息，或建议对方贴出来。
不要输出工具调用、函数调用或任何 <...> 标记，只写正文。

要求：
- 中文回答，先给结论再给理由，不写客套话，300 字以内。
- 只输出一条给评论者的回复正文：不要输出思考过程，不要用代码块包裹整条回复，不要 @ 任何人。
- 不确定的问题就说明缺什么信息，不要编造项目里的细节。`

// Options 是装配 Runner 时可选的钩子。
type Options struct {
	// Logf 收日志；为 nil 时不记。
	Logf func(format string, args ...any)
	// Verbose 打开后额外打印配置目录等信息。
	Verbose bool
	// OnDraft 非空时，出稿进待审队列而不是直接发帖。
	OnDraft agent.OnDraftFunc
}

// NewRunner 装配一个 Runner：解析 token、校验身份、加载状态与指令。
func NewRunner(cfg config.Config, opts Options) (*agent.Runner, error) {
	token, err := ResolveToken()
	if err != nil {
		return nil, err
	}
	client := ghapi.New(token)
	me, err := client.CurrentLogin(context.Background())
	if err != nil {
		return nil, fmt.Errorf("校验 GitHub token 失败：%w", err)
	}

	responder, err := buildResponder(cfg, opts)
	if err != nil {
		return nil, err
	}

	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	statePath := filepath.Join(dir, "state.json")
	st := state.Load(statePath)

	if cfg.InstructionsFile != "" {
		if _, err := os.ReadFile(cfg.InstructionsFile); err != nil {
			return nil, fmt.Errorf("读取 instructions_file 失败：%w", err)
		}
	}
	instructions := Instructions(cfg)

	if opts.Verbose && opts.Logf != nil {
		opts.Logf("配置目录 %s", dir)
	}
	return &agent.Runner{
		Config:       cfg,
		Instructions: instructions,
		Me:           me,
		Live:         !cfg.DryRun,
		GH:           client,
		Responder:    responder,
		State:        st,
		StatePath:    statePath,
		Logf:         opts.Logf,
		OnDraft:      opts.OnDraft,
	}, nil
}

// ResolveToken 依次尝试 GH_TOKEN、GITHUB_TOKEN、gh auth token（复用已登录的 gh）。
func ResolveToken() (string, error) {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, nil
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		if token := strings.TrimSpace(string(out)); token != "" {
			return token, nil
		}
	}
	return "", errors.New("没有 GitHub token：设置 GH_TOKEN，或先执行 gh auth login")
}

// buildResponder 按配置挑出稿方式。
func buildResponder(cfg config.Config, opts Options) (agent.Responder, error) {
	return ResponderFor(cfg, cfg.Agent, opts.Logf)
}

// ResponderFor 按 agent id 造一个出稿器；agentID 为空时用配置里的默认值。
// 界面里"换一个 agent 重做"也走这里。
func ResponderFor(cfg config.Config, agentID string, logf func(string, ...any)) (agent.Responder, error) {
	kind := strings.TrimSpace(agentID)
	if kind == "" {
		kind = strings.TrimSpace(cfg.Agent)
	}
	if kind == "" || kind == "llm" {
		apiKey := ""
		if cfg.Provider.APIKeyEnv != "" {
			apiKey = os.Getenv(cfg.Provider.APIKeyEnv)
		}
		if apiKey == "" {
			apiKey = cfg.Provider.APIKey
		}
		return llmResponder{client: llm.New(
			cfg.Provider.BaseURL,
			cfg.Provider.Model,
			apiKey,
			time.Duration(cfg.Provider.TimeoutSeconds)*time.Second,
			cfg.Provider.MaxOutputTokens,
			cfg.Provider.Temperature,
		)}, nil
	}
	if _, ok := acp.Lookup(kind); !ok {
		return nil, fmt.Errorf("配置里的 agent %q 不认识，可选：llm、codebuddy、pi", kind)
	}
	return &acp.Responder{
		AgentID: kind,
		Workdir: func(repo string) string { return agentWorkdir(cfg, repo) },
		Timeout: agentTimeout,
		Logf:    logf,
	}, nil
}

// Instructions 返回系统提示：配置了 instructions_file 就读它，否则用内置的。
func Instructions(cfg config.Config) string {
	if cfg.InstructionsFile != "" {
		if raw, err := os.ReadFile(cfg.InstructionsFile); err == nil && strings.TrimSpace(string(raw)) != "" {
			return string(raw)
		}
	}
	return DefaultInstructions
}

// agentWorkdir 返回本地 agent 的工作目录：优先用 repo_paths 里配的本地克隆，
// 否则在配置目录下开一个空的 work/<repo>，保证 agent 有个合法的 cwd。
func agentWorkdir(cfg config.Config, repo string) string {
	if path := strings.TrimSpace(cfg.RepoPaths[repo]); path != "" {
		return path
	}
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	slug := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(repo)
	work := filepath.Join(dir, "work", slug)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return ""
	}
	return work
}

// llmResponder 把 llm.Client 适配成 agent.Responder，
// 这样 llm 包不用反向依赖 agent。
type llmResponder struct{ client *llm.Client }

func (r llmResponder) Respond(ctx context.Context, req agent.Request) (string, error) {
	return r.client.Chat(ctx, req.System, req.User)
}

func (r llmResponder) Label() string { return r.client.Model }
