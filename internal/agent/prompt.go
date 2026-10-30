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
}

// BuildPrompt 生成喂给模型的用户消息：任务说明 + 条目上下文 + 待回复评论 + 最近对话。
func BuildPrompt(in PromptInput) string {
	kind := "Issue"
	if in.Issue.IsPull() {
		kind = "PR"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "仓库：%s\n", in.Repo)
	fmt.Fprintf(&sb, "条目：#%d（%s）%s\n", in.Issue.Number, kind, in.Issue.Title)
	if in.Issue.HTMLURL != "" {
		fmt.Fprintf(&sb, "链接：%s\n", in.Issue.HTMLURL)
	}
	if body := strings.TrimSpace(in.Issue.Body); body != "" {
		fmt.Fprintf(&sb, "\n条目正文（可能被截断）：\n%s\n", Clip(body, bodyMaxRunes))
	} else {
		sb.WriteString("\n条目正文：（空）\n")
	}
	fmt.Fprintf(&sb, "\n需要你回复的评论（来自 %s，%s）：\n%s\n",
		in.Comment.User.Login, in.Comment.AuthorAssociation, Clip(in.Comment.Body, commentMaxRunes))

	recent := make([]ghapi.Comment, 0, len(in.Thread))
	for _, item := range in.Thread {
		if item.ID == in.Comment.ID {
			continue
		}
		recent = append(recent, item)
	}
	if len(recent) > threadContext {
		recent = recent[len(recent)-threadContext:]
	}
	if len(recent) > 0 {
		sb.WriteString("\n最近的评论（按时间顺序，仅供上下文，不必逐条回应）：\n")
		for _, item := range recent {
			fmt.Fprintf(&sb, "- %s：%s\n", item.User.Login, Clip(strings.Join(strings.Fields(item.Body), " "), 400))
		}
	}
	sb.WriteString("\n只输出回复正文。")
	return sb.String()
}

// Finalize 裁剪模型输出并追加签名。
func Finalize(text, signature string) string {
	trimmed := strings.TrimSpace(text)
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
