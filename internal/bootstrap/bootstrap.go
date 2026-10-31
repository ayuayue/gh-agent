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
	"github.com/ayuayue/gh-agent/internal/state"
)

// agentTimeout 是本地 agent 单轮出稿的上限。读代码 + 跑命令比打模型慢得多。
const agentTimeout = 8 * time.Minute

// DefaultInstructions 是没有配置 instructions_file 时用的系统提示。
const DefaultInstructions = `你在一个软件仓库里回答 issue / PR 评论。

你能不能读文件、跑命令取决于运行环境：能读就先看代码再回答，不能读就只基于给出的上下文。
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
	// State 非空时复用调用方已经加载的状态：桌面壳和轮询线程共享同一份，
	// 界面上点「忽略」之后，下一轮立刻生效。
	State *state.State
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
	st := opts.State
	if st == nil {
		st = state.Load(statePath)
	}

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

// StatePath 是轮询状态文件路径：已处理的评论 id、手动忽略的条目和评论。
func StatePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
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
	if kind == "" {
		return nil, errors.New("还没选出稿方：在设置里选 CodeBuddy / Codex / Claude Code / pi")
	}
	if _, ok := acp.Lookup(kind); !ok {
		return nil, fmt.Errorf("配置里的 agent %q 不认识，可选：codebuddy、codex、claude、pi", kind)
	}
	opt := cfg.AgentOptionFor(kind)
	return &acp.Responder{
		AgentID: kind,
		Workdir: func(repo string) string { return RepoWorkdir(cfg, repo) },
		Policy:  acp.ParsePolicy(cfg.AgentPermission),
		Model:   opt.Model,
		Mode:    opt.Mode,
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

// RepoWorkdir 返回本地 agent 的工作目录：优先用 repo_paths 里配的本地克隆，
// 否则在配置目录下开一个空的 work/<repo>，保证 agent 有个合法的 cwd。
func RepoWorkdir(cfg config.Config, repo string) string {
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

// ReviewInstructions 是手动审查的系统提示：默认那段是给「没有工具」的模型写的，
// 审查一般跑在能读代码的本地 agent 里，所以单独给一份；配了自定义指令就拼在后面。
func ReviewInstructions(cfg config.Config) string {
	if cfg.InstructionsFile != "" {
		if raw, err := os.ReadFile(cfg.InstructionsFile); err == nil && strings.TrimSpace(string(raw)) != "" {
			return strings.TrimSpace(string(raw)) + "\n\n" + agent.ReviewWorkflow
		}
	}
	return agent.ReviewInstructions
}
