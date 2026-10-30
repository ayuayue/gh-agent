package desk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/ghapi"
)

// reviewTimeout 是手动审查的单次上限：本地 agent 要读代码、可能还要改，给足时间。
const reviewTimeout = 10 * time.Minute

// ReviewItem 立刻让默认出稿方审查一条 issue / PR：改代码、提交、写回复，结果进待审。
// 手动触发，不用等轮询。锁按条目分：同一条目串行，不同条目可以同时跑。
func (s *Service) ReviewItem(repo string, number int) (Draft, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" || number <= 0 {
		return Draft{}, errors.New("条目参数不对")
	}

	s.mu.Lock()
	cfg := s.cfg
	me := s.me
	s.mu.Unlock()

	key := taskKey(repo, number)
	if !s.locks.TryLock(key) {
		return Draft{}, errors.New("这个条目正在处理中，等它跑完再试")
	}
	defer s.locks.Unlock(key)

	token, err := bootstrap.ResolveToken()
	if err != nil {
		return Draft{}, err
	}
	client := ghapi.New(token)
	ctx, cancel := context.WithTimeout(context.Background(), reviewTimeout)
	defer cancel()

	issue, err := client.GetIssue(ctx, repo, number)
	if err != nil {
		return Draft{}, fmt.Errorf("读取条目失败：%w", err)
	}
	comments, err := client.ListIssueComments(ctx, repo, number)
	if err != nil {
		s.logf("  ! %s#%d 取评论失败，只按条目正文审查：%v", repo, number, err)
	}

	responder, err := bootstrap.ResponderFor(cfg, "", s.logf)
	if err != nil {
		return Draft{}, err
	}
	label := responder.Label()
	s.logf("手动审查 %s#%d，交给 %s", repo, number, label)
	s.startTask(key, &TaskDTO{
		Repo:       repo,
		Number:     number,
		CommentURL: issue.HTMLURL,
		Agent:      label,
		StartedAt:  time.Now().Format(time.RFC3339),
	}, cancel)
	defer s.finishTask(key)

	system := bootstrap.ReviewInstructions(cfg)
	prompt := agent.BuildReviewPrompt(agent.PromptInput{
		Repo:         repo,
		Issue:        issue,
		Thread:       comments,
		Instructions: system,
		Me:           me,
	})
	out, err := responder.Respond(ctx, agent.Request{Repo: repo, System: system, User: prompt})
	if err != nil {
		if ctx.Err() != nil {
			s.logf("审查已停止：%s#%d", repo, number)
			return Draft{}, errors.New("已停止")
		}
		return Draft{}, err
	}
	// PR 说「可以合并」时会留一个 HTML 注释标记，摘掉它再进正文。
	text, wantsMerge := agent.SplitMergeVerdict(out.Text)
	body := agent.Finalize(text, cfg.Signature)
	if body == "" {
		return Draft{}, errors.New("agent 返回了空正文")
	}

	draft := Draft{
		ID:         fmt.Sprintf("review-%s-%d-%d", strings.ReplaceAll(repo, "/", "_"), number, time.Now().UnixNano()),
		Repo:       repo,
		Number:     number,
		Title:      issue.Title,
		IssueURL:   issue.HTMLURL,
		CommentURL: issue.HTMLURL,
		Author:     issue.User.Login,
		Body:       body,
		Kind:       issueKind(issue),
		Type:       issueType(issue),
		Agent:      label,
		Prompt:     prompt,
		Changes:    out.Changes,
		Merge:      wantsMerge && issue.IsPull(),
		CreatedAt:  time.Now().Format(time.RFC3339),
		Status:     "pending",
	}

	// 审查的约定是「先改代码、先提交」，批准只负责回帖（PR 还会先合并）。
	if out.Changes != nil && len(out.Changes.Files) > 0 {
		branch, sha, err := commitDraft(cfg, draft, true)
		if err != nil {
			s.logf("自动提交失败（改动仍在工作区，批准时会再试）：%v", err)
		} else {
			draft.Branch, draft.Commit = branch, sha
			s.logf("改动已自动提交到 %s（%s）", branch, sha[:8])
		}
	}

	if err := s.drafts.Add(draft); err != nil {
		return Draft{}, err
	}
	EventDraft.Broadcast(draft)
	s.logf("审查完成，已进待审：%s#%d", repo, number)
	EventStatus.Broadcast(s.Status())
	s.refreshItemsIfWanted()
	return draft, nil
}
