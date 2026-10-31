package desk

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStatusNextPoll(t *testing.T) {
	s := &Service{
		running:  true,
		drafts:   loadDrafts(filepath.Join(t.TempDir(), "drafts.json")),
		nextPoll: time.Now().Add(2 * time.Minute),
	}
	if got := s.statusLocked().NextPoll; got == "" {
		t.Fatal("轮询中应带上下一轮时间")
	}

	// 停掉之后就不该再有「下次」。
	s.running = false
	if got := s.statusLocked().NextPoll; got != "" {
		t.Errorf("停止后 nextPoll 应为空，得到 %q", got)
	}
}
