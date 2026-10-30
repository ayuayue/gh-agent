package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/llm"
	"github.com/ayuayue/gh-agent/internal/state"
)

// Runner 把配置、GitHub、模型、状态串成"跑一轮"。
type Runner struct {
	Config       config.Config
	Instructions string
	Me           string
	Live         bool
	GH           *ghapi.Client
	LLM          *llm.Client
	State        *state.State
	StatePath    string
	Logf         func(format string, args ...any)
	// OnDraft 非空时，出稿不再直接发帖，而是交给它处置（进待审队列）。
	// 它优先于 Live：人工审核模式下 Live 应保持 false。
	OnDraft func(repo string, issue ghapi.Issue, comment ghapi.Comment, body string) error
}

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
		since := repoState.Since(r.Config.Overlap(), time.Now())
		issues, err := r.GH.ListIssuesUpdatedSince(ctx, repo, since)
		if err != nil {
			return handled, fmt.Errorf("%s 列条目失败: %w", repo, err)
		}
		if len(issues) == 0 {
			repoState.Advance(time.Now())
			continue
		}
		r.logf("%s：since=%s 有 %d 条更新", repo, since.Format(time.RFC3339), len(issues))
		for _, issue := range issues {
			if handled >= r.Config.MaxPerPoll {
				break
			}
			comments, err := r.GH.ListIssueCommentsSince(ctx, repo, issue.Number, since)
			if err != nil {
				return handled, fmt.Errorf("%s#%d 列评论失败: %w", repo, issue.Number, err)
			}
			filter := Filter{Me: r.Me, Trigger: r.Config.Trigger, Allowed: allowed, Seen: repoState.Has}
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
		repoState.Advance(time.Now())
	}
	if err := r.State.Save(r.StatePath); err != nil {
		return handled, err
	}
	return handled, nil
}

func (r *Runner) handle(ctx context.Context, repo string, issue ghapi.Issue, comment ghapi.Comment, thread []ghapi.Comment, repoState *state.Repo) error {
	r.logf("  命中 #%d 的评论 %d（%s）", issue.Number, comment.ID, comment.User.Login)
	prompt := BuildPrompt(PromptInput{Repo: repo, Issue: issue, Comment: comment, Thread: thread, Instructions: r.Instructions})
	reply, err := r.LLM.Chat(ctx, r.Instructions, prompt)
	if err != nil {
		return err
	}
	body := Finalize(reply, r.Config.Signature)
	if body == "" {
		repoState.Mark(comment.ID)
		return nil
	}
	if r.OnDraft != nil {
		if err := r.OnDraft(repo, issue, comment, body); err != nil {
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
