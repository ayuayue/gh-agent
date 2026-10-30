package desk

import (
	"strings"
	"testing"

	"github.com/ayuayue/gh-agent/internal/ghapi"
)

func TestIssueType(t *testing.T) {
	cases := []struct {
		name   string
		issue  ghapi.Issue
		expect string
	}{
		{"bug 标签", ghapi.Issue{Labels: []ghapi.Label{{Name: "bug"}}}, "bug"},
		{"中文缺陷", ghapi.Issue{Labels: []ghapi.Label{{Name: "缺陷"}}}, "bug"},
		{"feature 标签", ghapi.Issue{Labels: []ghapi.Label{{Name: "Feature"}}}, "feature"},
		{"enhancement", ghapi.Issue{Labels: []ghapi.Label{{Name: "enhancement"}}}, "feature"},
		{"都有时 bug 优先", ghapi.Issue{Labels: []ghapi.Label{{Name: "bug"}, {Name: "feature"}}}, "bug"},
		{"无关标签", ghapi.Issue{Labels: []ghapi.Label{{Name: "docs"}}}, ""},
		{"没标签但标题写了 Bug", ghapi.Issue{Title: "[Bug] 松手后不动"}, "bug"},
		{"中文标题带 bug 前缀", ghapi.Issue{Title: "bug: 面板打不开"}, "bug"},
		{"标题写了新功能", ghapi.Issue{Title: "希望加一个新功能"}, "feature"},
		{"标签优先于标题", ghapi.Issue{Title: "bug: x", Labels: []ghapi.Label{{Name: "feature"}}}, "feature"},
		{"PR 不分类", ghapi.Issue{Labels: []ghapi.Label{{Name: "bug"}}, PullRequest: &struct{}{}}, ""},
	}
	for _, c := range cases {
		if got := issueType(c.issue); got != c.expect {
			t.Errorf("%s: 期望 %q，得到 %q", c.name, c.expect, got)
		}
	}
}

func TestApplyRepliesMarksRepliedAndCommit(t *testing.T) {
	s := &Service{me: "me"}
	item := Item{Comments: 3}
	s.applyReplies(&item, []ghapi.Comment{
		{Body: "看起来是这个问题", User: ghapi.User{Login: "friend"}},
		{
			Body: "改好了，见 https://github.com/o/r/commit/abcdef1234567",
			User: ghapi.User{Login: "me"},
		},
	}, "me")

	if !item.Replied {
		t.Error("自己发过评论应标记已回复")
	}
	if !item.Committed || item.Commit != "abcdef1234567" {
		t.Errorf("应识别出回复里的提交，得到 %+v", item)
	}
	if len(item.Replies) != 2 || !item.Replies[1].Mine {
		t.Errorf("回复列表不对：%+v", item.Replies)
	}
	if item.Replies[1].Body == "" || strings.Contains(item.Replies[1].Body, "…") {
		t.Errorf("短正文不该被截断：%q", item.Replies[1].Body)
	}
}

func TestApplyDraftsMarksCommitted(t *testing.T) {
	s := &Service{}
	s.drafts = loadDrafts(t.TempDir() + "/drafts.json")
	if err := s.drafts.Add(Draft{
		ID: "1", Repo: "o/r", Number: 7, Status: "posted", Branch: "gh-agent/issue-7", Commit: "deadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	items := []Item{{ID: "o/r#7", Repo: "o/r", Number: 7}}
	s.applyDrafts(items)
	if !items[0].Replied || !items[0].Committed || items[0].Commit != "deadbeef" {
		t.Errorf("草稿状态没并进条目：%+v", items[0])
	}
	if items[0].DraftStatus != "posted" {
		t.Errorf("应记下最近一条草稿的状态，得到 %q", items[0].DraftStatus)
	}
}
