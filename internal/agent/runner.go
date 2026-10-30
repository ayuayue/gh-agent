package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/state"
	"github.com/ayuayue/gh-agent/internal/worktree"
)

// Runner 把配置、GitHub、模型、状态串成"跑一轮"。
type Runner struct {
	Config       config.Config
	Instructions string
	Me           string
	Live         bool
	GH           *ghapi.Client
	Responder    Responder
	State        *state.State
	StatePath    string
	Logf         func(format string, args ...any)
	// OnDraft 非空时，出稿不再直接发帖，而是交给它处置（进待审队列）。
	// 它优先于 Live：人工审核模式下 Live 应保持 false。
	OnDraft OnDraftFunc
	// OnTask 在开始出稿时收到这条任务，结束时收到 nil。
	// 界面靠它显示「正在处理谁」，ACP 跑一分钟也不至于看起来像卡死。
	OnTask func(*Task)
	// OnItem 是触发词留空时的审查回调：桌面壳拿它跑完整审查并出稿。
	// 为空（比如 CLI）时跳过这个模式。
	OnItem func(repo string, issue ghapi.Issue) error
}

// Task 是正在出稿的那一条评论。
type Task struct {
	Repo       string
	Number     int
	CommentID  int64
	CommentURL string
	Agent      string
	StartedAt  time.Time
	// Done 表示这一轮结束（成功、失败或被停掉都会发一次）。
	Done bool
	// Cancel 用来中止这一轮出稿：ACP 子进程会被一起杀掉。
	Cancel context.CancelFunc
}

// DraftInput 是 Runner 交给上层的一条待审草稿。
// 带上 Prompt 是为了之后能用别的 agent 重做这一条。
type DraftInput struct {
	Repo    string
	Issue   ghapi.Issue
	Comment ghapi.Comment
	Body    string
	Agent   string
	Prompt  string
	// Changes 非空表示这一轮动过工作区。
	Changes *worktree.Result
}

// OnDraftFunc 接管出稿结果。
type OnDraftFunc func(in DraftInput) error

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// RunOnce 跑一轮，返回本轮处理的评论条数。
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	allowed := make(map[string]bool, len(r.Config.AllowedAssociations))
	for _, assoc := range r.Config.AllowedAssociations {
		allowed[assoc] = true
	}
	handled := 0
	for _, repo := range r.Config.Repos {
		if handled >= r.Config.MaxPerPoll {
			break
		}
		repoState := r.State.For(repo)
		// 触发词留空 = 逐条审查模式：所有还没有评论的条目都审一遍，
		// 有评论的说明人已经表过态，跳过。
		if strings.TrimSpace(r.Config.Trigger) == "" {
			handled += r.runItems(ctx, repo, repoState)
			continue
		}
		since := repoState.Since(r.Config.Overlap(), time.Now())
		issues, err := r.GH.ListIssuesUpdatedSince(ctx, repo, since)
		if err != nil {
			return handled, fmt.Errorf("%s 列条目失败: %w", repo, err)
		}
		if len(issues) == 0 {
			r.logf("%s：没有新动态（since %s）", repo, since.Format("01-02 15:04:05"))
			repoState.Advance(time.Now())
			continue
		}
		r.logf("%s：since=%s 有 %d 条更新", repo, since.Format(time.RFC3339), len(issues))
		before := handled
		for _, issue := range issues {
			if handled >= r.Config.MaxPerPoll {
				break
			}
			// 条目级忽略：整条不再处理，连评论都不拉。
			if repoState.IsIgnoredIssue(issue.Number) {
				continue
			}
			comments, err := r.GH.ListIssueCommentsSince(ctx, repo, issue.Number, since)
			if err != nil {
				return handled, fmt.Errorf("%s#%d 列评论失败: %w", repo, issue.Number, err)
			}
			filter := Filter{Me: r.Me, Trigger: r.Config.Trigger, Allowed: allowed, Seen: func(id int64) bool {
				return repoState.Has(id) || repoState.IsIgnoredComment(id)
			}}
			for _, comment := range comments {
				if handled >= r.Config.MaxPerPoll {
					break
				}
				if !filter.Match(comment) {
					continue
				}
				if err := r.handle(ctx, repo, issue, comment, comments, repoState); err != nil {
					r.logf("  ✗ #%d 处理失败：%v", issue.Number, err)
					continue
				}
				handled++
			}
		}
		if handled == before {
			r.logf("%s：%d 条有更新，但没有命中触发规则的评论", repo, len(issues))
		}
		repoState.Advance(time.Now())
	}
	if err := r.State.Save(r.StatePath); err != nil {
		return handled, err
	}
	return handled, nil
}

// runItems 扫一遍 open 条目，把「还没有任何评论」的交给 OnItem 审查。
// 每轮最多 MaxPerPoll 条；审过的记在 state 里，不会重复审。
func (r *Runner) runItems(ctx context.Context, repo string, repoState *state.Repo) int {
	if r.OnItem == nil {
		r.logf("%s：触发词为空，但当前入口没有审查回调，跳过", repo)
		return 0
	}
	issues, err := r.GH.ListOpenIssues(ctx, repo)
	if err != nil {
		r.logf("%s：列条目失败：%v", repo, err)
		return 0
	}
	handled := 0
	for _, issue := range issues {
		if handled >= r.Config.MaxPerPoll {
			break
		}
		if issue.Comments > 0 || repoState.IsIgnoredIssue(issue.Number) || repoState.HasIssue(issue.Number) {
			continue
		}
		r.logf("%s#%d 没有评论，交给 %s 审查", repo, issue.Number, r.Responder.Label())
		if err := r.OnItem(repo, issue); err != nil {
			r.logf("  ✗ #%d 审查失败：%v", issue.Number, err)
			continue
		}
		repoState.MarkIssue(issue.Number)
		handled++
	}
	if handled == 0 {
		r.logf("%s：没有待审查的条目（共 %d 条 open）", repo, len(issues))
	}
	repoState.Advance(time.Now())
	return handled
}

func (r *Runner) handle(ctx context.Context, repo string, issue ghapi.Issue, comment ghapi.Comment, thread []ghapi.Comment, repoState *state.Repo) error {
	agentLabel := r.Responder.Label()
	r.logf("  命中 #%d 的评论 %d（%s），交给 %s 出稿", issue.Number, comment.ID, comment.User.Login, agentLabel)
	if r.OnTask != nil {
		// 每条任务一个可取消的 context：界面上点「停止」只中止这一条。
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = runCtx
		r.OnTask(&Task{
			Repo:       repo,
			Number:     issue.Number,
			CommentID:  comment.ID,
			CommentURL: comment.HTMLURL,
			Agent:      agentLabel,
			StartedAt:  time.Now(),
			Cancel:     cancel,
		})
		defer r.OnTask(&Task{Repo: repo, Number: issue.Number, Done: true})
	}
	// 本轮窗口里的评论只够找新评论；出稿要完整上下文——之前谁回过什么，
	// 模型得看得到，才不会把已经说过的话再说一遍。
	if full, err := r.GH.ListIssueComments(ctx, repo, issue.Number); err == nil && len(full) > 0 {
		thread = full
	} else if err != nil {
		r.logf("  ! #%d 取完整评论失败，只用本轮窗口：%v", issue.Number, err)
	}
	prompt := BuildPrompt(PromptInput{Repo: repo, Issue: issue, Comment: comment, Thread: thread, Instructions: r.Instructions, Me: r.Me})
	reply, err := r.Responder.Respond(ctx, Request{Repo: repo, System: r.Instructions, User: prompt})
	if err != nil {
		// 用户在界面上点了「停止」：这条评论记成已处理，别下一轮又自动重跑。
		if ctx.Err() != nil {
			r.logf("  ⏹ #%d 已停止，不再自动重试", issue.Number)
			repoState.Mark(comment.ID)
			return nil
		}
		return err
	}
	body := Finalize(reply.Text, r.Config.Signature)
	if body == "" {
		repoState.Mark(comment.ID)
		return nil
	}
	if r.OnDraft != nil {
		if err := r.OnDraft(DraftInput{
			Repo:    repo,
			Issue:   issue,
			Comment: comment,
			Body:    body,
			Agent:   r.Responder.Label(),
			Prompt:  prompt,
			Changes: reply.Changes,
		}); err != nil {
			return err
		}
		r.logf("  ⤷ 已进待审：%s", comment.HTMLURL)
		repoState.Mark(comment.ID)
		return nil
	}
	if !r.Live {
		r.logf("  ⤷ [dry-run] %s", comment.HTMLURL)
		r.logf("----- 将要发送 -----\n%s\n-------------------", body)
		repoState.Mark(comment.ID)
		return nil
	}
	if _, err := r.GH.PostIssueComment(ctx, repo, issue.Number, body); err != nil {
		return err
	}
	r.logf("  ✓ 已回复 #%d", issue.Number)
	repoState.Mark(comment.ID)
	return nil
}
