package acp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/agent"
)

// Responder 用某个本地 agent 出稿，实现 agent.Responder。
//
// 每次出稿都新起一个进程、跑完就关：agent 的启动成本可以接受，
// 而常驻会话要处理崩溃、上下文膨胀、僵尸进程一堆问题，不值。
type Responder struct {
	// AgentID 是 Catalog 里的 id。
	AgentID string
	// Workdir 返回这个仓库的本地工作目录；返回空则用一个临时目录。
	Workdir func(repo string) string
	// Timeout 是单轮出稿的上限。
	Timeout time.Duration
	Logf    func(format string, args ...any)
}

// Respond 实现 agent.Responder。
func (r *Responder) Respond(ctx context.Context, req agent.Request) (string, error) {
	def, ok := Lookup(r.AgentID)
	if !ok {
		return "", fmt.Errorf("不认识的 agent %q", r.AgentID)
	}
	cwd := ""
	if r.Workdir != nil {
		cwd = r.Workdir(req.Repo)
	}
	if cwd == "" {
		return "", fmt.Errorf("%s 需要一个本地工作目录", def.Name)
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := Start(runCtx, def.Command, cwd)
	if err != nil {
		return "", fmt.Errorf("%s 启动失败：%w", def.Name, err)
	}
	defer client.Close()

	out, err := client.Prompt(runCtx, req.System+"\n\n---\n\n"+req.User, nil)
	if err != nil {
		return "", fmt.Errorf("%s 出稿失败：%w", def.Name, err)
	}
	if tools := client.Tools(); len(tools) > 0 && r.Logf != nil {
		r.Logf("%s 用了 %d 个工具：%s", def.Name, len(tools), strings.Join(tools, "、"))
	}
	return out, nil
}

// Label 实现 agent.Responder。
func (r *Responder) Label() string {
	if def, ok := Lookup(r.AgentID); ok {
		return def.Name
	}
	return r.AgentID
}
