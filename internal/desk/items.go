package desk

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
)

const (
	// itemLimit 是每个仓库最多列出的条目数：条目列表是给人看的，不需要全量。
	itemLimit = 30
	// itemThreadLimit 是最多给多少条拉完整评论，避免一次刷新打太多请求。
	itemThreadLimit = 30
	// itemReplyLimit 是每条条目最多保留的回复数（取最近的）。
	itemReplyLimit = 8
	// itemReplyRunes 是单条回复在列表里保留的字数。
	itemReplyRunes = 1200
)

var (
	commitRe = regexp.MustCompile(`/commit/([0-9a-fA-F]{7,40})`)
	branchRe = regexp.MustCompile(`gh-agent/issue-\d+(?:-\d+)?`)
)

// issueKind 把 issue / PR 归一成前端用的两种值。
func issueKind(issue ghapi.Issue) string {
	if issue.IsPull() {
		return "pr"
	}
	return "issue"
}

// 标签和标题里的关键词：很多仓库不打标签，标题里的 [Bug] / bug: 反而更常见。
var (
	bugLabelWords     = []string{"bug", "缺陷"}
	featureLabelWords = []string{"feature", "enhancement", "功能", "需求"}
	bugTitleWords     = []string{"[bug]", "bug:", "bug：", "缺陷"}
	featureTitleWords = []string{"[feature]", "feature:", "feature：", "[feat]", "feat:", "新功能", "需求"}
)

// issueType 猜 issue 的类别：bug / feature，猜不出来返回空。
// 先看标签，没有标签再看标题。
func issueType(issue ghapi.Issue) string {
	if issue.IsPull() {
		return ""
	}
	labels := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		labels = append(labels, strings.ToLower(label.Name))
	}
	joined := strings.Join(labels, " ")
	if containsAny(joined, bugLabelWords) {
		return "bug"
	}
	if containsAny(joined, featureLabelWords) {
		return "feature"
	}
	title := strings.ToLower(issue.Title)
	if containsAny(title, bugTitleWords) {
		return "bug"
	}
	if containsAny(title, featureTitleWords) {
		return "feature"
	}
	return ""
}

func containsAny(text string, words []string) bool {
	for _, word := range words {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// Items 返回缓存的条目列表；没刷新过就是空的。
func (s *Service) Items() ItemsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.itemsSnapshotLocked()
}

// RefreshItems 在后台重新拉一遍条目列表。
// 旧数据还在就继续显示（不空白），拉完通过 EventItems 推一版新的整体替换。
func (s *Service) RefreshItems() ItemsSnapshot {
	s.mu.Lock()
	if s.itemsLoading {
		st := s.itemsSnapshotLocked()
		s.mu.Unlock()
		return st
	}
	cfg := s.cfg
	hasOld := len(s.items) > 0
	s.itemsWanted = true
	s.itemsLoading = true
	st := s.itemsSnapshotLocked()
	s.mu.Unlock()

	// 有旧数据时不清错误、不显示「加载中」——界面保持原样，刷完直接换新。
	if !hasOld {
		s.mu.Lock()
		s.itemsErr = ""
		st = s.itemsSnapshotLocked()
		s.mu.Unlock()
	}
	EventItems.Broadcast(st)
	go s.loadItems(cfg)
	return st
}

// refreshItemsIfWanted 只在界面已经用过条目列表时才刷新：
// 没打开过就别白花 GitHub 配额。
func (s *Service) refreshItemsIfWanted() {
	s.mu.Lock()
	wanted := s.itemsWanted
	s.mu.Unlock()
	if wanted {
		s.RefreshItems()
	}
}

func (s *Service) loadItems(cfg config.Config) {
	if len(cfg.Repos) == 0 {
		s.finishItems(nil, errors.New("配置里 repos 为空：先在设置里填一个 owner/name"), false)
		return
	}
	token, err := bootstrap.ResolveToken()
	if err != nil {
		s.finishItems(nil, err, false)
		return
	}
	client := ghapi.New(token)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	s.mu.Lock()
	me := s.me
	prev := append([]Item(nil), s.items...) // 增量合并的底
	s.mu.Unlock()
	if me == "" {
		// 启动时 token 可能还没就绪；这里补一次，免得「已回复」判断全落空。
		if login, err := client.CurrentLogin(ctx); err == nil {
			me = login
		}
	}
	prevIndex := make(map[string]Item, len(prev))
	for _, item := range prev {
		prevIndex[item.ID] = item
	}

	var (
		items    []Item
		firstErr error
	)
	for _, repo := range cfg.Repos {
		issues, err := client.ListOpenIssues(ctx, repo)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s 列条目失败: %w", repo, err)
			}
			continue
		}
		if len(issues) > itemLimit {
			issues = issues[:itemLimit]
		}
		threads := 0 // 每个仓库各有一份预算，别让第一个仓库把额度吃光
		for _, issue := range issues {
			item := s.newItem(repo, issue)
			// 评论没变多就复用上一次拉的回复，不打那次请求：
			// 大多数刷新里绝大多数条目都是没动的。
			if old, ok := prevIndex[item.ID]; ok && old.Comments == item.Comments {
				merged := item
				merged.Replies = old.Replies
				merged.Replied = old.Replied
				merged.RepliedAt = old.RepliedAt
				merged.Committed = old.Committed
				merged.Commit = old.Commit
				merged.Branch = old.Branch
				item = merged
			} else if issue.Comments > 0 && threads < itemThreadLimit {
				threads++
				comments, err := client.ListIssueComments(ctx, repo, issue.Number)
				if err != nil {
					s.logf("  ! %s#%d 取评论失败：%v", repo, issue.Number, err)
				} else {
					s.applyReplies(&item, comments, me)
				}
			}
			items = append(items, item)
		}
	}
	s.applyDrafts(items)
	// 按创建时间排（新的在前）：更新时间的顺序会随评论跳来跳去，看起来乱。
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	s.finishItems(items, firstErr, true)
}

func (s *Service) newItem(repo string, issue ghapi.Issue) Item {
	item := Item{
		ID:        fmt.Sprintf("%s#%d", repo, issue.Number),
		Repo:      repo,
		Number:    issue.Number,
		Title:     issue.Title,
		URL:       issue.HTMLURL,
		Kind:      issueKind(issue),
		Type:      issueType(issue),
		Labels:    []string{},
		Author:    issue.User.Login,
		Comments:  issue.Comments,
		CreatedAt: issue.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: issue.UpdatedAt.UTC().Format(time.RFC3339),
		Replies:   []ItemReply{},
	}
	for _, label := range issue.Labels {
		if label.Name != "" {
			item.Labels = append(item.Labels, label.Name)
		}
	}
	repoState := s.state.For(repo)
	if repoState.IsIgnoredIssue(issue.Number) {
		item.Ignored = true
		item.IgnoreNote = repoState.IssueNote(issue.Number)
	}
	return item
}

// applyReplies 把已有评论挂到条目上，同时推断「已回复 / 已提交」。
func (s *Service) applyReplies(item *Item, comments []ghapi.Comment, me string) {
	replies := make([]ItemReply, 0, len(comments))
	for _, comment := range comments {
		commit := ""
		if match := commitRe.FindStringSubmatch(comment.Body); match != nil {
			commit = match[1]
		}
		reply := ItemReply{
			Author:    comment.User.Login,
			Body:      agent.Clip(comment.Body, itemReplyRunes),
			URL:       comment.HTMLURL,
			CreatedAt: comment.CreatedAt.UTC().Format(time.RFC3339),
			Mine:      me != "" && comment.User.Login == me,
			Commit:    commit,
		}
		if reply.Mine {
			item.Replied = true
			item.RepliedAt = reply.CreatedAt
		}
		if commit != "" {
			item.Committed = true
			item.Commit = commit
		}
		if item.Branch == "" {
			if branch := branchRe.FindString(comment.Body); branch != "" {
				item.Branch = branch
			}
		}
		replies = append(replies, reply)
	}
	if len(replies) > itemReplyLimit {
		replies = replies[len(replies)-itemReplyLimit:]
	}
	item.Replies = replies
	if item.Comments < len(comments) {
		item.Comments = len(comments)
	}
}

// applyDrafts 用本地草稿补上「已发送 / 已提交 / 忽略备注」，这些 GitHub 上看不出来。
func (s *Service) applyDrafts(items []Item) {
	index := make(map[string]int, len(items))
	for i := range items {
		index[items[i].ID] = i
	}
	for _, draft := range s.drafts.List() { // 新的在前
		i, ok := index[fmt.Sprintf("%s#%d", draft.Repo, draft.Number)]
		if !ok {
			continue
		}
		item := &items[i]
		if item.DraftID == "" {
			item.DraftID = draft.ID
			item.DraftStatus = draft.Status
		}
		if draft.Status == "posted" {
			item.Replied = true
			if draft.CreatedAt != "" {
				item.RepliedAt = draft.CreatedAt
			}
		}
		if draft.Commit != "" {
			item.Committed = true
			item.Commit = draft.Commit
			item.Branch = draft.Branch
		}
	}
}

// IgnoreItem 忽略整个条目：之后轮询不再处理它的任何评论。
// 这个条目下还在待审的草稿也一并忽略，免得「列表说忽略了，待审里还能发」。
func (s *Service) IgnoreItem(repo string, number int, note string) (ItemsSnapshot, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" || number <= 0 {
		return s.Items(), errors.New("条目参数不对")
	}
	note = strings.TrimSpace(note)
	s.state.For(repo).IgnoreIssue(number, note)
	if err := s.state.Save(s.statePath); err != nil {
		return s.Items(), err
	}
	for _, draft := range s.drafts.List() {
		if draft.Repo != repo || draft.Number != number || draft.Status != "pending" {
			continue
		}
		out, _ := s.drafts.Update(draft.ID, func(x *Draft) {
			x.Status = "ignored"
			x.Note = note
		})
		EventDraft.Broadcast(out)
	}
	EventStatus.Broadcast(s.Status())
	s.logf("已忽略条目 %s#%d", repo, number)
	return s.markItemIgnored(repo, number, note, true), nil
}

// UnignoreItem 撤销条目级忽略，之后的新评论会重新处理。
func (s *Service) UnignoreItem(repo string, number int) (ItemsSnapshot, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" || number <= 0 {
		return s.Items(), errors.New("条目参数不对")
	}
	s.state.For(repo).UnignoreIssue(number)
	if err := s.state.Save(s.statePath); err != nil {
		return s.Items(), err
	}
	s.logf("已取消忽略条目 %s#%d", repo, number)
	return s.markItemIgnored(repo, number, "", false), nil
}

// CloseItem 关闭条目：填了原因就先发一条说明评论，再把 issue / PR 关掉。
// 关闭后把这条从列表和缓存里摘掉，条目下待审的草稿一并忽略。
func (s *Service) CloseItem(repo string, number int, note string) (ItemsSnapshot, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" || number <= 0 {
		return s.Items(), errors.New("条目参数不对")
	}
	note = strings.TrimSpace(note)

	token, err := bootstrap.ResolveToken()
	if err != nil {
		return s.Items(), err
	}
	client := ghapi.New(token)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	issue, err := client.GetIssue(ctx, repo, number)
	if err != nil {
		return s.Items(), fmt.Errorf("读取条目失败：%w", err)
	}
	if strings.EqualFold(issue.State, "closed") {
		s.dropClosedItem(repo, number)
		return s.Items(), errors.New("这个条目已经关闭了")
	}

	// 先发说明再关闭：关闭失败时评论已经在，不会出现「说关了其实没关」。
	if note != "" {
		if _, err := client.PostIssueComment(ctx, repo, number, note); err != nil {
			return s.Items(), fmt.Errorf("说明评论没发出去，条目未关闭：%w", err)
		}
	}
	// issue 标成「不打算修」；PR 没有这个字段，直接关。
	reason := "not_planned"
	if issue.IsPull() {
		reason = ""
	}
	if err := client.CloseIssue(ctx, repo, number, reason); err != nil {
		if note != "" {
			return s.Items(), fmt.Errorf("关闭失败（说明评论已发）：%w", err)
		}
		return s.Items(), fmt.Errorf("关闭失败：%w", err)
	}

	s.logf("已关闭 %s#%d", repo, number)
	s.dropClosedItem(repo, number)
	for _, draft := range s.drafts.List() {
		if draft.Repo != repo || draft.Number != number || draft.Status != "pending" {
			continue
		}
		out, _ := s.drafts.Update(draft.ID, func(x *Draft) {
			x.Status = "ignored"
			x.Note = "条目已关闭"
		})
		EventDraft.Broadcast(out)
	}
	return s.Items(), nil
}

// dropClosedItem 把已关闭的条目从内存列表和磁盘缓存里摘掉。
func (s *Service) dropClosedItem(repo string, number int) {
	s.mu.Lock()
	kept := make([]Item, 0, len(s.items))
	for _, item := range s.items {
		if item.Repo == repo && item.Number == number {
			continue
		}
		kept = append(kept, item)
	}
	s.items = kept
	s.itemsAt = time.Now()
	st := s.itemsSnapshotLocked()
	s.mu.Unlock()
	EventItems.Broadcast(st)
	s.itemsCache.save(st.Items, s.itemsAt)
}

func (s *Service) markItemIgnored(repo string, number int, note string, ignored bool) ItemsSnapshot {
	s.mu.Lock()
	for i := range s.items {
		if s.items[i].Repo != repo || s.items[i].Number != number {
			continue
		}
		s.items[i].Ignored = ignored
		s.items[i].IgnoreNote = ""
		if ignored {
			s.items[i].IgnoreNote = note
			// 条目下还在待审的草稿刚才已经一起忽略了，列表里的标记也跟着走。
			if s.items[i].DraftStatus == "pending" {
				s.items[i].DraftStatus = "ignored"
			}
		}
	}
	st := s.itemsSnapshotLocked()
	s.mu.Unlock()
	EventItems.Broadcast(st)
	return st
}

// finishItems 收尾：有新列表就整体替换并落盘缓存；拉取失败时保留旧列表，
// 只把错误挂上——宁可看旧数据也不看空白。
func (s *Service) finishItems(items []Item, err error, saveCache bool) {
	s.mu.Lock()
	if items != nil {
		s.items = items
	}
	s.itemsLoading = false
	s.itemsErr = ""
	if err != nil {
		s.itemsErr = err.Error()
	}
	s.itemsAt = time.Now()
	st := s.itemsSnapshotLocked()
	s.mu.Unlock()

	if err != nil {
		s.logf("条目列表刷新失败：%v", err)
	}
	if saveCache && items != nil && err == nil {
		s.itemsCache.save(st.Items, s.itemsAt)
	}
	EventItems.Broadcast(st)
}

func (s *Service) itemsSnapshotLocked() ItemsSnapshot {
	items := make([]Item, len(s.items))
	copy(items, s.items)
	st := ItemsSnapshot{Items: items, Loading: s.itemsLoading, Error: s.itemsErr}
	if !s.itemsAt.IsZero() {
		st.FetchedAt = s.itemsAt.Format("15:04:05")
	}
	// 列表来自磁盘缓存且还没刷成功过 → 标记为旧数据。
	if _, cacheAt := s.itemsCache.get(); !cacheAt.IsZero() && s.itemsAt.Equal(cacheAt) {
		st.Stale = true
	}
	return st
}
