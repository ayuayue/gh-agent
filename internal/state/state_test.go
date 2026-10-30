package state

import (
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
