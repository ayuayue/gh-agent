package agent

import (
	"fmt"
	"strings"

	"github.com/ayuayue/gh-agent/internal/ghapi"
)

const (
	bodyMaxRunes    = 4000
	commentMaxRunes = 6000
	threadContext   = 6
	replyMaxRunes   = 4000
)

// PromptInput 是组装 prompt 需要的全部素材。
type PromptInput struct {
	Repo         string
	Issue        ghapi.Issue
	Comment      ghapi.Comment
	Thread       []ghapi.Comment
	Instructions string
	// Me 是自己的 login：线程里自己发过的回复要标出来，避免重复回答。
	Me string
}

// BuildPrompt 生成喂给模型的用户消息：任务说明 + 条目上下文 + 待回复评论 + 最近对话。
func BuildPrompt(in PromptInput) string {
	var sb strings.Builder
	writeItemContext(&sb, in)
	fmt.Fprintf(&sb, "\n需要你回复的评论（来自 %s，%s）：\n%s\n",
		in.Comment.User.Login, in.Comment.AuthorAssociation, Clip(in.Comment.Body, commentMaxRunes))

	recent := make([]ghapi.Comment, 0, len(in.Thread))
	for _, item := range in.Thread {
		if item.ID == in.Comment.ID {
			continue
		}
		recent = append(recent, item)
	}
	writeThread(&sb, in, recent, threadContext)
	sb.WriteString("\n只输出回复正文。")
	return sb.String()
}

// reviewThreadContext 是手动审查时带上的评论条数：比轮询多一些，
// 因为这一条可能攒了很久的讨论。
const reviewThreadContext = 10

// BuildReviewPrompt 是手动「审查」用的 prompt：没有指定要回复哪条评论，
// 让模型自己判断这个条目现在需要什么（回答、定位问题、或者直接改代码）。
func BuildReviewPrompt(in PromptInput) string {
	var sb strings.Builder
	writeItemContext(&sb, in)
	writeThread(&sb, in, in.Thread, reviewThreadContext)
	sb.WriteString("\n任务：审查这个条目。先判断它现在需要什么——回答问题、定位问题、给方案，")
	sb.WriteString("或者直接在工作区里改代码（不要提交、不要建分支、不要动无关文件）。")
	sb.WriteString("回复里说明你做了什么、为什么；不要贴大段 diff，不要输出过程。\n")
	sb.WriteString("只输出要发到这条 issue / PR 的回复正文。")
	return sb.String()
}

// writeItemContext 写仓库、条目、标签和正文，两种 prompt 共用。
func writeItemContext(sb *strings.Builder, in PromptInput) {
	kind := "Issue"
	if in.Issue.IsPull() {
		kind = "PR"
	}
	fmt.Fprintf(sb, "仓库：%s\n", in.Repo)
	fmt.Fprintf(sb, "条目：#%d（%s）%s\n", in.Issue.Number, kind, in.Issue.Title)
	if in.Issue.HTMLURL != "" {
		fmt.Fprintf(sb, "链接：%s\n", in.Issue.HTMLURL)
	}
	if len(in.Issue.Labels) > 0 {
		names := make([]string, 0, len(in.Issue.Labels))
		for _, label := range in.Issue.Labels {
			if label.Name != "" {
				names = append(names, label.Name)
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(sb, "标签：%s\n", strings.Join(names, "、"))
		}
	}
	if body := strings.TrimSpace(in.Issue.Body); body != "" {
		fmt.Fprintf(sb, "\n条目正文（可能被截断）：\n%s\n", Clip(body, bodyMaxRunes))
	} else {
		sb.WriteString("\n条目正文：（空）\n")
	}
}

// writeThread 写最近的评论：自己发过的会标出来，避免模型重复回答。
func writeThread(sb *strings.Builder, in PromptInput, thread []ghapi.Comment, limit int) {
	if len(thread) > limit {
		thread = thread[len(thread)-limit:]
	}
	if len(thread) == 0 {
		return
	}
	sb.WriteString("\n已有的回复（按时间顺序，仅供上下文，不要重复已经说过的内容）：\n")
	for _, item := range thread {
		who := item.User.Login
		if in.Me != "" && who == in.Me {
			who += "（你自己之前的回复）"
		}
		fmt.Fprintf(sb, "- %s：%s\n", who, Clip(strings.Join(strings.Fields(item.Body), " "), 400))
	}
}

// toolMarkupMarkers 是模型"自己发明工具调用"时会吐出来的标记前缀。
// deepseek 系带工具能力的模型在没有 tools 参数时会这么干。
var toolMarkupMarkers = []string{
	"<｜｜DSML｜｜",
	"｜｜DSML｜｜",
	"<tool_call",
	"</tool_call",
	"<function_call",
}

// stripToolMarkup 从第一个标记处截断：宁可这条不发，也不能把标记贴进 issue。
func stripToolMarkup(text string) string {
	cut := len(text)
	for _, marker := range toolMarkupMarkers {
		if idx := strings.Index(text, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(text[:cut])
}

// Finalize 裁剪模型输出并追加签名；返回空串表示这条不该发。
func Finalize(text, signature string) string {
	trimmed := stripToolMarkup(strings.TrimSpace(text))
	if trimmed == "" {
		return ""
	}
	if len([]rune(trimmed)) > replyMaxRunes {
		trimmed = Clip(trimmed, replyMaxRunes) + "\n\n…（已截断）"
	}
	if strings.TrimSpace(signature) == "" {
		return trimmed
	}
	return trimmed + "\n\n" + strings.TrimSpace(signature)
}

// Clip 按 rune 截断，避免把 emoji / 生僻字劈成半个代理对。
func Clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// MergeMarker 是 PR 审查结论的机器可读标记：模型认为可以合并时写在回复最后，
// 解析后从正文里去掉，不会出现在 GitHub 上。
const MergeMarker = "<!-- gh-agent: merge -->"

// SplitMergeVerdict 把回复里的合并标记摘出来。
func SplitMergeVerdict(text string) (string, bool) {
	if !strings.Contains(text, MergeMarker) {
		return text, false
	}
	return strings.TrimSpace(strings.ReplaceAll(text, MergeMarker, "")), true
}

// ReviewWorkflow 是手动审查的工作流说明；和自定义指令拼的时候也要带上。
const ReviewWorkflow = `按条目类型处理：
- Bug：先在代码里定位根因并修复。正文或评论里已经给出方案的话，先评估：可行就按它做，
  不可行就在回复里说明为什么。改完说明改了什么、为什么、怎么验证。
- Feature：改动小、风险低、容易验证就直接实现；影响面大或需求不清楚，就只给方案、不动代码。
- PR：先看改动（能跑命令的话可以用 gh pr diff <编号>），判断有没有问题。没问题就在回复最后
  单独写一行 ` + MergeMarker + `，表示可以合并；有问题就指出具体问题，不要写这一行。

约束：
- 你的工作目录是一个独立的 git worktree，检出于专用分支——不要切换分支、不要动主仓库，
  也不要自己执行 git 提交或建分支；工具会在你完成后提交、摘掉 worktree 并合回基准分支。
- 只输出要发到这条 issue / PR 的回复正文：不要输出思考过程，不要贴大段 diff，不要 @ 任何人。
- 中文回答，先结论后理由，尽量简短（需要给命令或代码片段时除外）。`

// ReviewInstructions 是手动审查用的系统提示：不沿用默认那段「你没有读文件能力」，
// 因为审查通常跑在能读代码的本地 agent 里。
const ReviewInstructions = `你在一个软件仓库里处理 GitHub issue / PR。你会拿到条目的标题、正文、标签和已有评论。
如果运行环境允许，先读相关代码再下结论。

` + ReviewWorkflow
