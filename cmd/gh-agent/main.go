// Command gh-agent 轮询 GitHub 的 issue / PR 评论，用本地模型生成回复。
//
// 默认干跑（dry-run）：只把"将要发送的内容"打印出来，不写 GitHub。
// 加 --live 才会真的发评论。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/llm"
	"github.com/ayuayue/gh-agent/internal/state"
)

const defaultInstructions = `你在一个软件仓库里回答 issue / PR 评论。

你只能看到下面给出的上下文：没有读文件、跑命令、联网的能力。
上下文里没有的仓库细节不要假装知道——直接说明还需要什么信息，或建议对方贴出来。
不要输出工具调用、函数调用或任何 <...> 标记，只写正文。

要求：
- 中文回答，先给结论再给理由，不写客套话，300 字以内。
- 只输出一条给评论者的回复正文：不要输出思考过程，不要用代码块包裹整条回复，不要 @ 任何人。
- 不确定的问题就说明缺什么信息，不要编造项目里的细节。`

func main() {
	var (
		configPath  = flag.String("config", "", "配置文件路径（默认 %AppData%/gh-agent/config.json）")
		runOnce     = flag.Bool("once", false, "只跑一轮就退出")
		live        = flag.Bool("live", false, "真的发评论（默认干跑）")
		interval    = flag.Int("interval", 0, "轮询间隔秒数（覆盖配置）")
		repoFlag    = flag.String("repo", "", "只处理这个仓库 owner/name（覆盖配置）")
		printConfig = flag.Bool("print-config", false, "打印生效配置并退出")
		verbose     = flag.Bool("verbose", false, "打印调试信息")
	)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fail(err)
	}
	if *repoFlag != "" {
		cfg.Repos = []string{*repoFlag}
	}
	if *interval > 0 {
		cfg.IntervalSeconds = *interval
	}
	if *live {
		cfg.DryRun = false
	}

	if *printConfig {
		raw, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(raw))
		return
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "配置不完整："+err.Error())
		fmt.Fprintf(os.Stderr, "配置文件：%s\n", configPathOr(*configPath))
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner, err := buildRunner(cfg, *verbose)
	if err != nil {
		fail(err)
	}

	logf("仓库 %s｜身份 %s｜%s｜间隔 %ds", strings.Join(cfg.Repos, ", "), runner.Me, modeLabel(!cfg.DryRun), cfg.IntervalSeconds)
	logf("模型 %s @ %s", cfg.Provider.Model, cfg.Provider.BaseURL)

	if *runOnce {
		handled, err := runner.RunOnce(ctx)
		if err != nil {
			logf("本轮失败：%v", err)
			os.Exit(1)
		}
		logf("本轮结束，处理 %d 条", handled)
		return
	}

	for {
		handled, err := runner.RunOnce(ctx)
		if err != nil {
			logf("本轮失败：%v", err)
		} else if handled > 0 {
			logf("本轮处理 %d 条", handled)
		}
		select {
		case <-ctx.Done():
			logf("收到退出信号，已停止")
			return
		case <-time.After(cfg.Interval()):
		}
	}
}

func buildRunner(cfg config.Config, verbose bool) (*agent.Runner, error) {
	token, err := resolveToken()
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

	instructions := defaultInstructions
	if cfg.InstructionsFile != "" {
		raw, err := os.ReadFile(cfg.InstructionsFile)
		if err != nil {
			return nil, fmt.Errorf("读取 instructions_file 失败：%w", err)
		}
		if strings.TrimSpace(string(raw)) != "" {
			instructions = string(raw)
		}
	}

	if verbose {
		logf("配置目录 %s", dir)
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
		Logf:         logf,
	}, nil
}

// resolveToken 依次尝试 GH_TOKEN、GITHUB_TOKEN、gh auth token（复用已登录的 gh）。
func resolveToken() (string, error) {
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

func configPathOr(path string) string {
	if path != "" {
		return path
	}
	resolved, err := config.DefaultPath()
	if err != nil {
		return "(未知)"
	}
	return resolved
}

func modeLabel(live bool) string {
	if live {
		return "LIVE（会真的发评论）"
	}
	return "dry-run（只打印）"
}

func logf(format string, args ...any) {
	stamp := time.Now().Format("15:04:05")
	fmt.Printf("[%s] "+format+"\n", append([]any{stamp}, args...)...)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "错误："+err.Error())
	os.Exit(1)
}
