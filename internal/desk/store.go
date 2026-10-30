package desk

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

const (
	draftLimit = 200
	logLimit   = 500
)

// draftStore 是待审草稿的环形存储，落盘在 drafts.json，重启不丢。
type draftStore struct {
	path  string
	mu    sync.Mutex
	items []Draft // 新的在前
}

func loadDrafts(path string) *draftStore {
	s := &draftStore{path: path}
	raw, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(raw, &s.items)
	}
	return s
}

func (s *draftStore) saveLocked() error {
	raw, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
		if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Add 插入一条草稿（置顶）并落盘。
func (s *draftStore) Add(d Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append([]Draft{d}, s.items...)
	if len(s.items) > draftLimit {
		s.items = s.items[:draftLimit]
	}
	return s.saveLocked()
}

// List 返回全部草稿的副本。
func (s *draftStore) List() []Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Draft, len(s.items))
	copy(out, s.items)
	return out
}

// Get 按 id 取一条草稿。
func (s *draftStore) Get(id string) (Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.items {
		if d.ID == id {
			return d, true
		}
	}
	return Draft{}, false
}

// Update 就地修改一条草稿并落盘，返回修改后的副本。
func (s *draftStore) Update(id string, fn func(*Draft)) (Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		fn(&s.items[i])
		_ = s.saveLocked()
		return s.items[i], true
	}
	return Draft{}, false
}

// Pending 是还在等审核的条数。
func (s *draftStore) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.items {
		if d.Status == "pending" {
			n++
		}
	}
	return n
}

// logBuffer 是内存日志环形缓冲，不落盘。
type logBuffer struct {
	mu    sync.Mutex
	limit int
	items []LogLine
}

func newLogBuffer(limit int) *logBuffer {
	return &logBuffer{limit: limit}
}

func (b *logBuffer) Add(level, text string) LogLine {
	line := LogLine{At: time.Now().Format("15:04:05"), Level: level, Text: text}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = append(b.items, line)
	if len(b.items) > b.limit {
		b.items = b.items[len(b.items)-b.limit:]
	}
	return line
}

func (b *logBuffer) List() []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]LogLine, len(b.items))
	copy(out, b.items)
	return out
}
