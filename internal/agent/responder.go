package agent

import (
	"context"

	"github.com/ayuayue/gh-agent/internal/worktree"
)

// Request 是交给 Responder 出稿的全部素材。
type Request struct {
	// Repo 是 owner/name，本地 agent 据此挑工作目录。
	Repo string
	// System 是系统提示（行为约束）。
	System string
	// User 是具体任务：条目上下文 + 要回复的评论。
	User string
}

// Reply 是一轮出稿的结果。
type Reply struct {
	// Text 是回复正文。
	Text string
	// Changes 非空表示这一轮动过工作区，只有本地 agent 会有。
	Changes *worktree.Result
}

// Responder 产出回复正文。实现有两个：
//   - llm.Client：打 OpenAI 兼容端点，快、便宜、看不到代码；
//   - acp.Responder：驱动本机编码 agent，慢、能读仓库、能改代码。
type Responder interface {
	Respond(ctx context.Context, req Request) (Reply, error)
	// Label 用于日志和界面显示。
	Label() string
}
