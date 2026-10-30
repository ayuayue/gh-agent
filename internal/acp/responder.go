package acp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/worktree"
)

// diffLimit 是单个草稿里保留的 diff 上限（字符），防止把 drafts.json 撑爆。
const diffLimit = 64 * 1024

// codeInstruction 只在工作目录确实是 git 仓库时追加：不这样的话
// agent 不知道自己躺在代码里，只会照着 prompt 里那点片段作答。
const codeInstruction = "你现在在这个仓库的本地副本里，可以读文件、跑命令、改代码。" +
	"如果这条评论要求修 bug 或改行为，直接在工作区里改：不要提交、不要建分支、不要动与问题无关的文件。" +
	"回复里说明你改了什么、理由是什么，不要贴大段 diff。"

// Responder 用某个本地 agent 出稿，实现 agent.Responder。
//
// 每次出稿都新起一个进程、跑完就关：agent 的启动成本可以接受，
// 而常驻会话要处理崩溃、上下文膨胀、僵尸进程一堆问题，不值。
type Responder struct {
	// AgentID 是 Catalog 里的 id。
	AgentID string
	// Workdir 返回这个仓库的本地工作目录；返回空则报错。
	Workdir func(repo string) string
	// Policy 决定 agent 能用哪些工具。
	Policy Policy
	// Timeout 是单轮出稿的上限。
	Timeout time.Duration
	Logf    func(format string, args ...any)
}

// Respond 实现 agent.Responder。
func (r *Responder) Respond(ctx context.Context, req agent.Request) (agent.Reply, error) {
	def, ok := Lookup(r.AgentID)
	if !ok {
		return agent.Reply{}, fmt.Errorf("不认识的 agent %q", r.AgentID)
	}
	cwd := ""
	if r.Workdir != nil {
		cwd = r.Workdir(req.Repo)
	}
	if cwd == "" {
		return agent.Reply{}, fmt.Errorf("%s 需要一个本地工作目录", def.Name)
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return agent.Reply{}, fmt.Errorf("工作目录 %s 不可用：%v", cwd, err)
	}

	before := worktree.Capture(cwd)
	if !before.Repo && r.Logf != nil {
		r.Logf("工作目录 %s 不是 git 仓库：agent 读不到代码，也不会记录改动", cwd)
	}
	system := req.System
	if before.Repo {
		system += "\n\n" + codeInstruction
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	policy := r.Policy
	if policy == "" {
		policy = PolicyDeny
	}
	client, err := Start(runCtx, def.Command, cwd, WithPolicy(policy))
	if err != nil {
		return agent.Reply{}, fmt.Errorf("%s 启动失败：%w", def.Name, err)
	}
	defer client.Close()
	if r.Logf != nil {
		if pid := client.PID(); pid > 0 {
			r.Logf("%s 已启动（pid %d）", def.Name, pid)
		}
	}

	out, err := client.Prompt(runCtx, system+"\n\n---\n\n"+req.User, nil)
	if err != nil {
		return agent.Reply{}, fmt.Errorf("%s 出稿失败：%w", def.Name, err)
	}
	if tools := client.Tools(); len(tools) > 0 && r.Logf != nil {
		r.Logf("%s 用了 %d 个工具：%s", def.Name, len(tools), strings.Join(tools, "、"))
	}
	if denied := client.Denied(); len(denied) > 0 && r.Logf != nil {
		r.Logf("按权限策略拦下 %d 个工具调用：%s", len(denied), strings.Join(denied, "、"))
	}

	reply := agent.Reply{Text: out}
	if before.Repo {
		after := worktree.Capture(cwd)
		reply.Changes = worktree.Collect(cwd, before, after, worktree.Changed(before, after), diffLimit)
		if reply.Changes != nil && r.Logf != nil {
			r.Logf("%s 改了 %d 个文件", def.Name, len(reply.Changes.Files))
		}
	}
	return reply, nil
}

// Label 实现 agent.Responder。
func (r *Responder) Label() string {
	if def, ok := Lookup(r.AgentID); ok {
		return def.Name
	}
	return r.AgentID
}
