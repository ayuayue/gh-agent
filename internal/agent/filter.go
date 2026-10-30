// Package agent 负责「哪些评论要处理、怎么组装 prompt、怎么跑一轮」。
package agent

import (
	"strings"

	"github.com/ayuayue/gh-agent/internal/ghapi"
)

// Filter 描述一次匹配需要用到的静态条件。
type Filter struct {
	// Me 是自己的 login：自己发的评论一律跳过，避免自回环。
	Me string
	// Trigger 是触发词（大小写不敏感）。
	Trigger string
	// Allowed 是允许触发的 author_association 集合。
	Allowed map[string]bool
	// Seen 报告这个 comment id 是否已处理过。
	Seen func(id int64) bool
}

// Match 判断一条评论是否需要处理。
func (f Filter) Match(comment ghapi.Comment) bool {
	if comment.ID == 0 || comment.Body == "" {
		return false
	}
	if f.Seen != nil && f.Seen(comment.ID) {
		return false
	}
	login := comment.User.Login
	if login == "" || login == f.Me {
		return false
	}
	if comment.User.Type == "Bot" {
		return false
	}
	if !f.Allowed[comment.AuthorAssociation] {
		return false
	}
	// 没配触发词就是"全接"：任何评论都处理。
	if strings.TrimSpace(f.Trigger) == "" {
		return true
	}
	return ContainsTrigger(comment.Body, f.Trigger)
}

// ContainsTrigger 是大小写不敏感的触发词判定。
func ContainsTrigger(body, trigger string) bool {
	if body == "" || trigger == "" {
		return false
	}
	lowerBody := []rune(toLower(body))
	lowerTrigger := []rune(toLower(trigger))
	if len(lowerTrigger) > len(lowerBody) {
		return false
	}
	for i := 0; i+len(lowerTrigger) <= len(lowerBody); i++ {
		match := true
		for j := range lowerTrigger {
			if lowerBody[i+j] != lowerTrigger[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func toLower(text string) string {
	out := make([]rune, 0, len(text))
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}
