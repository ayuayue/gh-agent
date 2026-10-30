// Package state 保存轮询游标、已处理过的评论 id，以及手动忽略的条目 / 评论。
//
// 每个仓库一份记录：cursor 是上次轮询结束时间（回退 overlap 后作为下次的 since），
// seen 是处理过的 comment id 环形列表——重复轮询不重复回复；
// ignored / ignored_issues 是手动忽略的评论与条目，带一句备注，轮询会直接跳过。
//
// Runner 在后台轮询、界面在主线程改忽略，两边会同时读写同一份状态，所以
// Repo 自带读写锁，所有访问都走方法。
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxSeen 是每个仓库保留的已处理 id 数量上限。
const maxSeen = 1000

// Repo 是单仓库的轮询状态。
type Repo struct {
	mu sync.RWMutex

	Cursor string  `json:"cursor,omitempty"`
	Seen   []int64 `json:"seen,omitempty"`
	// Ignored 是手动忽略的评论：id → 备注。
	Ignored map[int64]string `json:"ignored,omitempty"`
	// IgnoredIssues 是手动忽略的条目：issue/PR 编号 → 备注，整个条目不再处理。
	IgnoredIssues map[int]string `json:"ignored_issues,omitempty"`
}

// State 是所有仓库的状态集合。
type State struct {
	mu    sync.RWMutex
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

// repoJSON 是写盘时的仓库快照：不带锁，可以安全复制和序列化。
type repoJSON struct {
	Cursor        string           `json:"cursor,omitempty"`
	Seen          []int64          `json:"seen,omitempty"`
	Ignored       map[int64]string `json:"ignored,omitempty"`
	IgnoredIssues map[int]string   `json:"ignored_issues,omitempty"`
}

// Save 原子写状态（临时文件 + rename）。
// 先给每个仓库拍一份快照，避免边写盘边被轮询线程改。
func (s *State) Save(path string) error {
	snapshot := struct {
		Repos map[string]repoJSON `json:"repos"`
	}{Repos: map[string]repoJSON{}}

	s.mu.RLock()
	for name, repo := range s.Repos {
		if repo != nil {
			snapshot.Repos[name] = repo.snapshot()
		}
	}
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
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
	s.mu.Lock()
	defer s.mu.Unlock()
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

// snapshot 复制一份可安全序列化的副本（不带锁）。
func (r *Repo) snapshot() repoJSON {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := repoJSON{Cursor: r.Cursor}
	out.Seen = append(out.Seen, r.Seen...)
	if len(r.Ignored) > 0 {
		out.Ignored = make(map[int64]string, len(r.Ignored))
		for id, note := range r.Ignored {
			out.Ignored[id] = note
		}
	}
	if len(r.IgnoredIssues) > 0 {
		out.IgnoredIssues = make(map[int]string, len(r.IgnoredIssues))
		for number, note := range r.IgnoredIssues {
			out.IgnoredIssues[number] = note
		}
	}
	return out
}

// Has 报告该 comment id 是否处理过。
func (r *Repo) Has(id int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hasLocked(id)
}

func (r *Repo) hasLocked(id int64) bool {
	for _, seen := range r.Seen {
		if seen == id {
			return true
		}
	}
	return false
}

// Mark 记录已处理（保持环形上限）。
func (r *Repo) Mark(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasLocked(id) {
		return
	}
	r.Seen = append(r.Seen, id)
	if len(r.Seen) > maxSeen {
		r.Seen = r.Seen[len(r.Seen)-maxSeen:]
	}
}

// Since 返回本轮轮询的起点：有游标就用「游标 - overlap」，否则用「现在 - overlap」。
func (r *Repo) Since(overlap time.Duration, now time.Time) time.Time {
	r.mu.RLock()
	cursor := r.Cursor
	r.mu.RUnlock()
	base := now
	if cursor != "" {
		if parsed, err := time.Parse(time.RFC3339, cursor); err == nil {
			base = parsed
		}
	}
	return base.Add(-overlap)
}

// Advance 把游标推进到 now。回退窗口只在 Since 里算一次——
// 两处都减会让实际窗口变成 2×overlap，语义含混。
func (r *Repo) Advance(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Cursor = now.UTC().Format(time.RFC3339)
}

// IsIgnoredComment 报告这条评论是否被手动忽略。
func (r *Repo) IsIgnoredComment(id int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.Ignored[id]
	return ok
}

// IgnoreComment 记下一条被忽略的评论和备注（空备注也记，表示忽略过）。
func (r *Repo) IgnoreComment(id int64, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Ignored == nil {
		r.Ignored = map[int64]string{}
	}
	r.Ignored[id] = note
}

// UnignoreComment 撤销一条评论的忽略。
func (r *Repo) UnignoreComment(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.Ignored, id)
}

// CommentNote 返回被忽略评论的备注。
func (r *Repo) CommentNote(id int64) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Ignored[id]
}

// IsIgnoredIssue 报告整个条目是否被忽略。
func (r *Repo) IsIgnoredIssue(number int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.IgnoredIssues[number]
	return ok
}

// IgnoreIssue 忽略整个条目：之后的轮询不会再处理它的任何评论。
func (r *Repo) IgnoreIssue(number int, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.IgnoredIssues == nil {
		r.IgnoredIssues = map[int]string{}
	}
	r.IgnoredIssues[number] = note
}

// UnignoreIssue 撤销条目级忽略。
func (r *Repo) UnignoreIssue(number int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.IgnoredIssues, number)
}

// IssueNote 返回被忽略条目的备注。
func (r *Repo) IssueNote(number int) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.IgnoredIssues[number]
}

// ErrNilState 用于保护调用方传空指针的情况（目前仅在测试里用来断言）。
var ErrNilState = errors.New("state is nil")
