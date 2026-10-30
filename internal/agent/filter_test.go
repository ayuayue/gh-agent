package agent

import (
	"testing"

	"github.com/ayuayue/gh-agent/internal/ghapi"
)

func allowedAll() map[string]bool {
	return map[string]bool{"OWNER": true, "MEMBER": true, "COLLABORATOR": true, "CONTRIBUTOR": true}
}

func comment(id int64, login, assoc, body string) ghapi.Comment {
	return ghapi.Comment{
		ID:                id,
		Body:              body,
		AuthorAssociation: assoc,
		User:              ghapi.User{Login: login, Type: "User"},
	}
}

func TestContainsTrigger(t *testing.T) {
	cases := []struct {
		body, trigger string
		want          bool
	}{
		{"请 @claude 看一下", "@claude", true},
		{"请 @CLAUDE 看一下", "@claude", true},
		{"@Claude 看一下", "@CLAUDE", true},
		{"没有触发词", "@claude", false},
		{"", "@claude", false},
		{"@claude", "", false},
		{"中文里夹@claude和英文", "@claude", true},
	}
	for _, c := range cases {
		if got := ContainsTrigger(c.body, c.trigger); got != c.want {
			t.Errorf("ContainsTrigger(%q, %q) = %v, want %v", c.body, c.trigger, got, c.want)
		}
	}
}

func TestMatchSkipsSelfBotAndStrangers(t *testing.T) {
	seen := map[int64]bool{}
	filter := Filter{Me: "me", Trigger: "@claude", Allowed: allowedAll(), Seen: func(id int64) bool { return seen[id] }}

	if filter.Match(comment(1, "me", "OWNER", "@claude 帮我")) {
		t.Error("自己的评论不应命中")
	}
	bot := comment(2, "someone", "OWNER", "@claude")
	bot.User.Type = "Bot"
	if filter.Match(bot) {
		t.Error("bot 的评论不应命中")
	}
	if filter.Match(comment(3, "stranger", "NONE", "@claude 帮我")) {
		t.Error("非协作者不应命中")
	}
	if !filter.Match(comment(4, "friend", "OWNER", "@claude 帮我")) {
		t.Error("协作者 + 触发词应命中")
	}
	seen[5] = true
	if filter.Match(comment(5, "friend", "OWNER", "@claude 帮我")) {
		t.Error("已处理的评论不应重复命中")
	}
	if filter.Match(comment(6, "friend", "OWNER", "")) {
		t.Error("空正文不应命中")
	}
}

// 触发词留空时，任何评论都该命中（其余规则照旧）。
func TestMatchEmptyTriggerAcceptsEverything(t *testing.T) {
	filter := Filter{Me: "me", Trigger: "", Allowed: allowedAll(), Seen: func(int64) bool { return false }}

	if !filter.Match(comment(1, "friend", "OWNER", "随便说点什么")) {
		t.Error("没配触发词时应命中任何正文")
	}
	if !filter.Match(comment(2, "friend", "MEMBER", "a")) {
		t.Error("没配触发词时应命中任何正文")
	}
	if filter.Match(comment(3, "stranger", "NONE", "随便说点什么")) {
		t.Error("非协作者仍然不该命中")
	}
	if filter.Match(comment(4, "me", "OWNER", "自己")) {
		t.Error("自己的评论仍然不该命中")
	}
}
