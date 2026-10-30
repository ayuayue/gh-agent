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

// RefreshItems 在后台重新拉一遍条目列表，立刻返回「加载中」的快照，
// 拉完通过 EventItems 推一版完整的。
func (s *Service) RefreshItems() ItemsSnapshot {
	s.mu.Lock()
	if s.itemsLoading {
		st := s.itemsSnapshotLocked()
		s.mu.Unlock()
		return st
	}
	cfg := s.cfg
	s.itemsWanted = true
	s.itemsLoading = true
	s.itemsErr = ""
	st := s.itemsSnapshotLocked()
	s.mu.Unlock()

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
		s.finishItems(nil, errors.New("配置里 repos 为空：先在设置里填一个 owner/name"))
		return
	}
	token, err := bootstrap.ResolveToken()
	if err != nil {
		s.finishItems(nil, err)
		return
	}
	client := ghapi.New(token)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	s.mu.Lock()
	me := s.me
	s.mu.Unlock()
	if me == "" {
		// 启动时 token 可能还没就绪；这里补一次，免得「已回复」判断全落空。
		if login, err := client.CurrentLogin(ctx); err == nil {
			me = login
		}
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
			if issue.Comments > 0 && threads < itemThreadLimit {
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
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt > items[j].UpdatedAt })
	s.finishItems(items, firstErr)
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

func (s *Service) finishItems(items []Item, err error) {
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
	EventItems.Broadcast(st)
}

func (s *Service) itemsSnapshotLocked() ItemsSnapshot {
	items := make([]Item, len(s.items))
	copy(items, s.items)
	st := ItemsSnapshot{Items: items, Loading: s.itemsLoading, Error: s.itemsErr}
	if !s.itemsAt.IsZero() {
		st.FetchedAt = s.itemsAt.Format("15:04:05")
	}
	return st
}
