// Command gh-agent 轮询 GitHub 的 issue / PR 评论，用本地模型生成回复。
//
// 默认干跑（dry-run）：只把"将要发送的内容"打印出来，不写 GitHub。
// 加 --live 才会真的发评论。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/config"
)

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

	runner, err := bootstrap.NewRunner(cfg, bootstrap.Options{Logf: logf, Verbose: *verbose})
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
