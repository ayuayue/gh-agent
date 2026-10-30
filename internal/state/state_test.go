package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMarkKeepsRing(t *testing.T) {
	repo := &Repo{}
	for i := int64(1); i <= maxSeen+10; i++ {
		repo.Mark(i)
	}
	if len(repo.Seen) != maxSeen {
		t.Fatalf("seen 应保持 %d 条，实际 %d", maxSeen, len(repo.Seen))
	}
	if repo.Has(1) {
		t.Error("最早的 id 应被挤出")
	}
	if !repo.Has(maxSeen + 10) {
		t.Error("最新的 id 应保留")
	}
	repo.Mark(maxSeen + 10)
	if len(repo.Seen) != maxSeen {
		t.Error("重复 Mark 不应增长")
	}
}

func TestSinceAndAdvance(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	repo := &Repo{}
	if got := repo.Since(2*time.Minute, now); !got.Equal(now.Add(-2 * time.Minute)) {
		t.Errorf("无游标时应从 now-overlap 开始，得到 %v", got)
	}
	repo.Advance(now)
	if repo.Cursor == "" {
		t.Fatal("Advance 后应有游标")
	}
	// 游标记的是「上次跑完的时刻」，回退只在 Since 里做一次
	if got := repo.Since(time.Minute, now.Add(time.Hour)); !got.Equal(now.Add(-time.Minute)) {
		t.Errorf("有游标时应从游标-overlap 开始，得到 %v", got)
	}
}

func TestIgnorePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := Load(path)
	repo := st.For("o/r")
	repo.IgnoreComment(42, "重复提问")
	repo.IgnoreIssue(7, "已人工处理")
	if !repo.IsIgnoredComment(42) || repo.CommentNote(42) != "重复提问" {
		t.Fatal("评论忽略没记下来")
	}
	if !repo.IsIgnoredIssue(7) || repo.IssueNote(7) != "已人工处理" {
		t.Fatal("条目忽略没记下来")
	}
	if err := st.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	again := Load(path).For("o/r")
	if !again.IsIgnoredComment(42) || again.CommentNote(42) != "重复提问" {
		t.Error("评论忽略没落盘")
	}
	if !again.IsIgnoredIssue(7) || again.IssueNote(7) != "已人工处理" {
		t.Error("条目忽略没落盘")
	}

	again.UnignoreComment(42)
	again.UnignoreIssue(7)
	if again.IsIgnoredComment(42) || again.IsIgnoredIssue(7) {
		t.Error("取消忽略后不应再命中")
	}
}
