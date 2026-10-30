// Package state 保存轮询游标与已处理过的评论 id。
//
// 每个仓库一份记录：cursor 是上次轮询结束时间（回退 overlap 后作为下次的 since），
// seen 是处理过的 comment id 环形列表——重复轮询不重复回复。
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// maxSeen 是每个仓库保留的已处理 id 数量上限。
const maxSeen = 1000

// Repo 是单仓库的轮询状态。
type Repo struct {
	Cursor string  `json:"cursor,omitempty"`
	Seen   []int64 `json:"seen,omitempty"`
}

// State 是所有仓库的状态集合。
type State struct {
	Repos map[string]*Repo `json:"repos"`
}

// Load 读取状态文件，不存在或损坏都返回空状态（游标为空时由调用方取 now-overlap）。
func Load(path string) *State {
	s := &State{Repos: map[string]*Repo{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(raw, s); err != nil || s.Repos == nil {
		return &State{Repos: map[string]*Repo{}}
	}
	return s
}

// Save 原子写状态（临时文件 + rename）。
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// For 取单仓库状态，不存在则创建。
func (s *State) For(repo string) *Repo {
	if s.Repos == nil {
		s.Repos = map[string]*Repo{}
	}
	r, ok := s.Repos[repo]
	if !ok || r == nil {
		r = &Repo{}
		s.Repos[repo] = r
	}
	return r
}

// Has 报告该 comment id 是否处理过。
func (r *Repo) Has(id int64) bool {
	for _, seen := range r.Seen {
		if seen == id {
			return true
		}
	}
	return false
}

// Mark 记录已处理（保持环形上限）。
func (r *Repo) Mark(id int64) {
	if r.Has(id) {
		return
	}
	r.Seen = append(r.Seen, id)
	if len(r.Seen) > maxSeen {
		r.Seen = r.Seen[len(r.Seen)-maxSeen:]
	}
}

// Since 返回本轮轮询的起点：有游标就用「游标 - overlap」，否则用「现在 - overlap」。
func (r *Repo) Since(overlap time.Duration, now time.Time) time.Time {
	base := now
	if r.Cursor != "" {
		if parsed, err := time.Parse(time.RFC3339, r.Cursor); err == nil {
			base = parsed
		}
	}
	return base.Add(-overlap)
}

// Advance 把游标推进到 now。回退窗口只在 Since 里算一次——
// 两处都减会让实际窗口变成 2×overlap，语义含混。
func (r *Repo) Advance(now time.Time) {
	r.Cursor = now.UTC().Format(time.RFC3339)
}

// ErrNilState 用于保护调用方传空指针的情况（目前仅在测试里用来断言）。
var ErrNilState = errors.New("state is nil")
