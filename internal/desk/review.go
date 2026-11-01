package desk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/acp"
	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/config"
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

	return s.reviewIssue(ctx, cfg, me, repo, issue, comments, cancel)
}

// onItem 是轮询在「触发词留空」模式下的回调：条目列表已经拉好了，
// 直接走同一套审查流程，不动评论。
func (s *Service) onItem(repo string, issue ghapi.Issue) error {
	s.mu.Lock()
	cfg := s.cfg
	me := s.me
	s.mu.Unlock()

	key := taskKey(repo, issue.Number)
	if !s.locks.TryLock(key) {
		return errors.New("这个条目正在处理中")
	}
	defer s.locks.Unlock(key)

	ctx, cancel := context.WithTimeout(context.Background(), reviewTimeout)
	defer cancel()
	_, err := s.reviewIssue(ctx, cfg, me, repo, issue, nil, cancel)
	return err
}

// reviewIssue 是审查的共用实现；调用方负责持有这个条目的锁。
func (s *Service) reviewIssue(ctx context.Context, cfg config.Config, me, repo string, issue ghapi.Issue, comments []ghapi.Comment, cancel context.CancelFunc) (Draft, error) {
	// 在独立的 worktree 里跑：agent 改文件、切分支都碰不到用户的主工作区。
	// 本地目录不是 git 仓库时退回共用目录，只是少了隔离。
	ws, wsErr := s.newWorkspace(cfg, repo, issue.Number)
	if wsErr != nil {
		s.logf("  ! %s#%d 建隔离 worktree 失败（%v），退回共用目录", repo, issue.Number, wsErr)
	} else {
		defer ws.close(false) // 失败路径：摘掉 worktree、删掉空分支
		s.logf("隔离工作区 %s（分支 %s，基于 %s）", ws.dir, ws.branch, ws.base)
	}

	responder, err := bootstrap.ResponderFor(cfg, "", s.logf)
	if err != nil {
		return Draft{}, err
	}
	if ws != nil {
		if acpResponder, ok := responder.(*acp.Responder); ok {
			acpResponder.Workdir = func(string) string { return ws.dir }
		}
	}
	label := responder.Label()
	s.logf("审查 %s#%d，交给 %s", repo, issue.Number, label)
	s.startTask(taskKey(repo, issue.Number), &TaskDTO{
		Repo:       repo,
		Number:     issue.Number,
		CommentURL: issue.HTMLURL,
		Agent:      label,
		StartedAt:  time.Now().Format(time.RFC3339),
	}, cancel)
	defer s.finishTask(taskKey(repo, issue.Number))

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
			s.logf("审查已停止：%s#%d", repo, issue.Number)
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
		ID:         fmt.Sprintf("review-%s-%d-%d", strings.ReplaceAll(repo, "/", "_"), issue.Number, time.Now().UnixNano()),
		Repo:       repo,
		Number:     issue.Number,
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

	// 审查的约定是「先改代码、先提交」：在隔离工作区里提交，
	// 然后摘掉 worktree；主仓库干净且正停在基准分支时才合回去。
	if ws != nil {
		sha, err := ws.commit(commitMessage(draft, true))
		if err != nil {
			s.logf("自动提交失败（改动还在隔离工作区）：%v", err)
		} else if sha != "" {
			draft.Branch, draft.Commit = ws.branch, sha
			s.logf("改动已提交到 %s（%s）", ws.branch, sha[:8])
		}
		ws.close(true) // 先摘 worktree，合并要在主仓库里做
		if draft.Commit != "" {
			mergedTo, blocked := ws.autoMerge("[gh-agent] 合并 " + ws.branch + " 到 " + ws.base)
			if blocked != "" {
				draft.MergeBlocked = blocked
				s.logf("没有自动合到 %s：%s（分支 %s 保留，等确认是否合并）", ws.base, blocked, ws.branch)
			} else {
				draft.MergedTo = mergedTo
				s.logf("已合并 %s 到 %s", ws.branch, mergedTo)
			}
		}
	} else if out.Changes != nil && len(out.Changes.Files) > 0 {
		// 没有 git 仓库可用：沿用旧的「从共用目录拷文件提交」。
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
	s.logf("审查完成，已进待审：%s#%d", repo, issue.Number)
	s.notifyPending()
	EventStatus.Broadcast(s.Status())
	s.refreshItemsIfWanted()
	return draft, nil
}
