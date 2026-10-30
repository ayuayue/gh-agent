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

	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/llm"
	"github.com/ayuayue/gh-agent/internal/state"
)

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
	OnDraft func(repo string, issue ghapi.Issue, comment ghapi.Comment, body string) error
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

	apiKey := ""
	if cfg.Provider.APIKeyEnv != "" {
		apiKey = os.Getenv(cfg.Provider.APIKeyEnv)
	}
	if apiKey == "" {
		apiKey = cfg.Provider.APIKey
	}

	llmClient := llm.New(
		cfg.Provider.BaseURL,
		cfg.Provider.Model,
		apiKey,
		time.Duration(cfg.Provider.TimeoutSeconds)*time.Second,
		cfg.Provider.MaxOutputTokens,
		cfg.Provider.Temperature,
	)

	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	statePath := filepath.Join(dir, "state.json")
	st := state.Load(statePath)

	instructions := DefaultInstructions
	if cfg.InstructionsFile != "" {
		raw, err := os.ReadFile(cfg.InstructionsFile)
		if err != nil {
			return nil, fmt.Errorf("读取 instructions_file 失败：%w", err)
		}
		if strings.TrimSpace(string(raw)) != "" {
			instructions = string(raw)
		}
	}

	if opts.Verbose && opts.Logf != nil {
		opts.Logf("配置目录 %s", dir)
	}
	return &agent.Runner{
		Config:       cfg,
		Instructions: instructions,
		Me:           me,
		Live:         !cfg.DryRun,
		GH:           client,
		LLM:          llmClient,
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
