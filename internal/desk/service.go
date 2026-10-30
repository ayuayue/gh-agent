// Package desk 是桌面壳的服务层：把配置、轮询、待审草稿包成前端能调用的方法，
// 并把状态变化通过 mygo 事件推给页面。
//
// 轮询出来的回复不直接发帖，一律进待审队列，由用户在界面上批准或驳回。
// 想让它自己发，用 CLI 的 --live。
package desk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ayuayue/gh-agent/internal/acp"
	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/state"
	"github.com/ayuayue/gh-agent/internal/worktree"
	"github.com/egoist/mygo"
)

// Service 是绑定给前端的服务：mygo.Bind(desk.New(...))。
type Service struct {
	cfgPath   string
	statePath string

	mu     sync.Mutex
	cfg    config.Config
	state  *state.State
	drafts *draftStore
	logs   *logBuffer
	cancel context.CancelFunc
	// running 是「现在真的在轮询」；desired 是「用户想要轮询」。
	// 两者分开：启动时配置不完整会失败，但用户改完配置后应当自动再试一次。
	running  bool
	desired  bool
	lastPoll time.Time
	lastErr  string
	handled  int
	me       string
	// 每个条目一把锁：同一条目串行，不同条目可以同时跑。
	locks keyedLocks
	// tasks / taskCancel 是正在出稿的任务，key 是 repo#number。
	tasks      map[string]*TaskDTO
	taskCancel map[string]context.CancelFunc
	// probes 缓存 agent 参数探测结果（进程内）。
	probes map[string]AgentProbe

	// 条目列表的缓存：按需刷新，不跟着每轮轮询跑。
	items        []Item
	itemsLoading bool
	itemsErr     string
	itemsAt      time.Time
	itemsWanted  bool
}

// New 读配置、加载历史草稿。cfgPath 为空时用默认路径。
func New(cfgPath string) (*Service, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	path := cfgPath
	if path == "" {
		if path, err = config.DefaultPath(); err != nil {
			return nil, err
		}
	}
	statePath, err := bootstrap.StatePath()
	if err != nil {
		return nil, err
	}
	s := &Service{
		cfgPath:   path,
		statePath: statePath,
		cfg:       cfg,
		state:     state.Load(statePath),
		drafts:    loadDrafts(filepath.Join(dir, "drafts.json")),
		logs:      newLogBuffer(logLimit),
	}
	// 身份解析失败不阻断启动：界面里会显示原因，用户改完 token 再点开始。
	if token, err := bootstrap.ResolveToken(); err == nil {
		client := ghapi.New(token)
		if me, err := client.CurrentLogin(context.Background()); err == nil {
			s.me = me
		} else {
			s.lastErr = err.Error()
		}
	} else {
		s.lastErr = err.Error()
	}
	return s, nil
}

// Config 返回当前配置。
// 每次都从磁盘重读：用户直接改 config.json 也能在界面上看到。
func (s *Service) Config() ConfigDTO {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	return toDTO(s.cfg)
}

// reloadLocked 把磁盘上的配置读回内存；读失败就保持原样，不打断界面。
func (s *Service) reloadLocked() {
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		return
	}
	s.cfg = cfg
}

// SaveConfig 校验并写盘；之前想跑（desired）的话用新配置重启轮询。
func (s *Service) SaveConfig(in ConfigDTO) (ConfigDTO, error) {
	s.mu.Lock()
	s.reloadLocked()
	prev := s.cfg
	next := applyDTO(prev, in)
	if err := next.Validate(); err != nil {
		s.mu.Unlock()
		return toDTO(prev), err
	}
	if err := next.Save(s.cfgPath); err != nil {
		s.mu.Unlock()
		return toDTO(prev), err
	}
	s.cfg = next
	// 配置变了，之前那条「repos 为空」之类的提示就作废。
	s.lastErr = ""
	wantRun := s.desired
	s.mu.Unlock()
	EventStatus.Broadcast(s.Status())

	// 启动时配置不完整导致没跑起来的情况，改完保存就自动补跑一次。
	if wantRun {
		s.Stop()
		if _, err := s.Start(); err != nil {
			return toDTO(next), err
		}
	}
	return toDTO(next), nil
}

// Status 返回轮询状态。
func (s *Service) Status() StatusDTO {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

// Start 开始轮询：先立刻跑一轮，之后按 interval_seconds。
// 配置每次从磁盘重读，用户在设置里改完保存、或直接改 config.json 都能生效。
func (s *Service) Start() (StatusDTO, error) {
	s.mu.Lock()
	s.desired = true
	if s.running {
		st := s.statusLocked()
		s.mu.Unlock()
		return st, nil
	}
	s.mu.Unlock()

	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		return s.failStart(err)
	}
	if err := cfg.Validate(); err != nil {
		return s.failStart(err)
	}

	// 审核模式：出稿进队列，不发帖。
	runCfg := cfg
	runCfg.DryRun = false
	runner, err := bootstrap.NewRunner(runCfg, bootstrap.Options{
		Logf:    s.logf,
		OnDraft: s.enqueue,
		State:   s.state,
	})
	if err != nil {
		return s.failStart(err)
	}
	runner.OnTask = s.onTask
	runner.OnItem = s.onItem
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.cfg = cfg
	s.cancel = cancel
	s.running = true
	s.me = runner.Me
	s.lastErr = ""
	interval := s.cfg.Interval()
	st := s.statusLocked()
	s.mu.Unlock()

	s.logf("开始轮询 %v，间隔 %s", cfg.Repos, interval)
	EventStatus.Broadcast(st)
	go s.loop(ctx, runner, interval)
	return st, nil
}

// failStart 记下启动失败的原因并推给界面；desired 保持 true，
// 这样用户补好配置再保存时 SaveConfig 会自动重试。
func (s *Service) failStart(err error) (StatusDTO, error) {
	s.mu.Lock()
	s.lastErr = err.Error()
	st := s.statusLocked()
	s.mu.Unlock()
	s.logf("启动失败：%v", err)
	EventStatus.Broadcast(st)
	return st, err
}

// Stop 停止轮询（正在跑的那一轮会跑完）。
func (s *Service) Stop() StatusDTO {
	s.mu.Lock()
	s.desired = false
	cancel := s.cancel
	s.cancel = nil
	s.running = false
	st := s.statusLocked()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		s.logf("已停止轮询")
	}
	EventStatus.Broadcast(st)
	return st
}

// Drafts 返回全部草稿，新的在前。
func (s *Service) Drafts() []Draft {
	return s.drafts.List()
}

// Approve 把一条待审草稿发到 GitHub。
// 如果这条草稿带着代码改动，先把改动提交到一个新分支，再发评论：
// 评论里说"改了"，就得真有东西可看。
func (s *Service) Approve(id string) (Draft, error) {
	d, ok := s.drafts.Get(id)
	if !ok {
		return Draft{}, errors.New("草稿不存在")
	}
	if d.Status != "pending" {
		return d, fmt.Errorf("这条草稿的状态是 %s，不能发送", d.Status)
	}

	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	// 审查出来的草稿在出稿时已经提交过，这里只补提交轮询出来的那种。
	branch, commit := d.Branch, d.Commit
	if commit == "" && d.Changes != nil && len(d.Changes.Files) > 0 {
		var err error
		branch, commit, err = commitDraft(cfg, d, false)
		if err != nil {
			out, _ := s.drafts.Update(id, func(x *Draft) {
				x.Status = "failed"
				x.Error = err.Error()
			})
			EventDraft.Broadcast(out)
			s.logf("提交改动失败 %s#%d：%v", d.Repo, d.Number, err)
			return out, err
		}
		s.logf("改动已提交到 %s（%s）", branch, commit[:8])
	}

	token, err := bootstrap.ResolveToken()
	if err != nil {
		return d, err
	}
	client := ghapi.New(token)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// PR 审查结论是「可以合并」：批准就同时合并；合并失败就不回帖。
	if d.Merge && d.Kind == "pr" {
		if err := client.MergePullRequest(ctx, d.Repo, d.Number, "merge"); err != nil {
			out, _ := s.drafts.Update(id, func(x *Draft) {
				x.Status = "failed"
				x.Error = "合并 PR 失败：" + err.Error()
			})
			EventDraft.Broadcast(out)
			s.logf("合并 PR 失败 %s#%d：%v", d.Repo, d.Number, err)
			return out, err
		}
		s.logf("已合并 PR %s#%d", d.Repo, d.Number)
	}

	if _, err := client.PostIssueComment(ctx, d.Repo, d.Number, d.Body); err != nil {
		out, _ := s.drafts.Update(id, func(x *Draft) {
			x.Status = "failed"
			x.Error = err.Error()
		})
		EventDraft.Broadcast(out)
		s.logf("发送失败 %s#%d：%v", d.Repo, d.Number, err)
		return out, err
	}
	out, _ := s.drafts.Update(id, func(x *Draft) {
		x.Status = "posted"
		x.Error = ""
		x.Branch = branch
		x.Commit = commit
	})
	EventDraft.Broadcast(out)
	s.logf("已发送 %s#%d", d.Repo, d.Number)
	// 发出去之后条目上的「已回复 / 已提交」就变了，顺手刷新一下列表。
	s.refreshItemsIfWanted()
	return out, nil
}

// commitDraft 把草稿涉及的改动提交到一个新分支，返回分支名和提交 sha。
func commitDraft(cfg config.Config, d Draft, auto bool) (string, string, error) {
	dir := bootstrap.RepoWorkdir(cfg, d.Repo)
	if dir == "" {
		return "", "", errors.New("找不到这个仓库的本地目录")
	}
	branch := nextBranchName(dir, "gh-agent/issue-"+strconv.Itoa(d.Number))
	sha, err := worktree.CommitToBranch(dir, branch, commitMessage(d, auto), d.Changes.Files)
	if err != nil {
		return "", "", err
	}
	return branch, sha, nil
}

// nextBranchName 撞名就加后缀：gh-agent/issue-447-2、-3…
func nextBranchName(repo, base string) string {
	if !worktree.BranchExists(repo, base) {
		return base
	}
	for i := 2; i < 100; i++ {
		name := base + "-" + strconv.Itoa(i)
		if !worktree.BranchExists(repo, name) {
			return name
		}
	}
	return base + "-" + strconv.FormatInt(time.Now().Unix(), 10)
}

// commitMessage 的消息标题和 trailer 都带 gh-agent 标记：
// 自动提交的分支一眼能从 git log 里认出来。
func commitMessage(d Draft, auto bool) string {
	head := "[gh-agent] 回复 #" + strconv.Itoa(d.Number)
	if d.Title != "" {
		head += " " + d.Title
	}
	note := "由 " + d.Agent + " 生成，经人工批准后提交。\n"
	if auto {
		note = "由 " + d.Agent + " 自动修改并提交，回复待人工批准后发布。\n"
	}
	return head + "\n\n" + note + "Auto-Committed-By: gh-agent\n" + d.CommentURL + "\n"
}

// Reject 丢弃一条待审草稿，不会发到 GitHub。
func (s *Service) Reject(id string) (Draft, error) {
	d, ok := s.drafts.Get(id)
	if !ok {
		return Draft{}, errors.New("草稿不存在")
	}
	if d.Status != "pending" {
		return d, fmt.Errorf("这条草稿的状态是 %s，不能驳回", d.Status)
	}
	out, _ := s.drafts.Update(id, func(x *Draft) { x.Status = "rejected" })
	EventDraft.Broadcast(out)
	s.logf("已驳回 %s#%d", d.Repo, d.Number)
	s.refreshItemsIfWanted()
	return out, nil
}

// Ignore 手动忽略一条待审草稿：之后轮询不再处理这条评论，备注会留在列表里。
func (s *Service) Ignore(id, note string) (Draft, error) {
	d, ok := s.drafts.Get(id)
	if !ok {
		return Draft{}, errors.New("草稿不存在")
	}
	if d.Status != "pending" {
		return d, fmt.Errorf("这条草稿的状态是 %s，不能忽略", d.Status)
	}
	note = strings.TrimSpace(note)
	out, _ := s.drafts.Update(id, func(x *Draft) {
		x.Status = "ignored"
		x.Note = note
		x.Error = ""
	})
	if commentID, err := strconv.ParseInt(d.ID, 10, 64); err == nil {
		s.state.For(d.Repo).IgnoreComment(commentID, note)
		if err := s.state.Save(s.statePath); err != nil {
			s.logf("忽略状态没写进磁盘：%v", err)
		}
	}
	EventDraft.Broadcast(out)
	EventStatus.Broadcast(s.Status())
	s.logf("已忽略 %s#%d（评论 %s）", d.Repo, d.Number, d.ID)
	s.refreshItemsIfWanted()
	return out, nil
}

// Unignore 撤销忽略，把草稿放回待审。
func (s *Service) Unignore(id string) (Draft, error) {
	d, ok := s.drafts.Get(id)
	if !ok {
		return Draft{}, errors.New("草稿不存在")
	}
	if d.Status != "ignored" {
		return d, fmt.Errorf("这条草稿的状态是 %s，不是忽略状态", d.Status)
	}
	out, _ := s.drafts.Update(id, func(x *Draft) {
		x.Status = "pending"
		x.Note = ""
	})
	if commentID, err := strconv.ParseInt(d.ID, 10, 64); err == nil {
		s.state.For(d.Repo).UnignoreComment(commentID)
		if err := s.state.Save(s.statePath); err != nil {
			s.logf("忽略状态没写进磁盘：%v", err)
		}
	}
	EventDraft.Broadcast(out)
	EventStatus.Broadcast(s.Status())
	s.logf("已取消忽略 %s#%d", d.Repo, d.Number)
	s.refreshItemsIfWanted()
	return out, nil
}

// CheckPath 检查一个本地目录能不能当 agent 的工作目录用。
func (s *Service) CheckPath(path string) PathCheck {
	out := PathCheck{Path: strings.TrimSpace(path)}
	if out.Path == "" {
		out.Detail = "留空则用配置目录下的空目录，agent 读不到代码"
		return out
	}
	info, err := os.Stat(out.Path)
	if err != nil {
		out.Detail = "目录不存在"
		return out
	}
	if !info.IsDir() {
		out.Detail = "不是目录"
		return out
	}
	out.Exists = true
	if !worktree.IsRepo(out.Path) {
		out.Detail = "不是 git 仓库，agent 改了什么不会被记录"
		return out
	}
	out.IsGit = true
	return out
}

// Agents 列出可选的出稿方：模型端点 + 检测到的本地 agent。
func (s *Service) Agents() []AgentInfo {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	var out []AgentInfo
	for _, info := range acp.Detect() {
		entry := AgentInfo{
			ID:        info.ID,
			Name:      info.Name,
			Desc:      info.Desc,
			Available: info.Available,
			Detail:    info.Detail,
			Current:   cfg.Agent == info.ID,
		}
		if entry.Available {
			entry.Detail = ""
		}
		out = append(out, entry)
	}
	return out
}

// Redraft 用指定的 agent 把同一条评论重新出一遍稿，覆盖原来那条。
func (s *Service) Redraft(id, agentID string) (Draft, error) {
	d, ok := s.drafts.Get(id)
	if !ok {
		return Draft{}, errors.New("草稿不存在")
	}
	if d.Status != "pending" {
		return d, fmt.Errorf("这条草稿的状态是 %s，不能重做", d.Status)
	}
	if strings.TrimSpace(d.Prompt) == "" {
		return d, errors.New("这条草稿没存下当时的 prompt，无法重做")
	}

	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	key := taskKey(d.Repo, d.Number)
	if !s.locks.TryLock(key) {
		return d, errors.New("这个条目正在处理中，等它跑完再试")
	}
	defer s.locks.Unlock(key)

	responder, err := bootstrap.ResponderFor(cfg, agentID, s.logf)
	if err != nil {
		return d, err
	}
	s.logf("用 %s 重做 %s#%d…", responder.Label(), d.Repo, d.Number)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	s.startTask(key, &TaskDTO{
		Repo:       d.Repo,
		Number:     d.Number,
		CommentURL: d.CommentURL,
		Agent:      responder.Label(),
		StartedAt:  time.Now().Format(time.RFC3339),
	}, cancel)
	defer s.finishTask(key)
	out, err := responder.Respond(ctx, agent.Request{Repo: d.Repo, System: bootstrap.Instructions(cfg), User: d.Prompt})
	if err != nil {
		return d, err
	}
	body := agent.Finalize(out.Text, cfg.Signature)
	if body == "" {
		return d, errors.New("agent 返回了空正文")
	}

	updated, _ := s.drafts.Update(id, func(x *Draft) {
		x.Body = body
		x.Agent = responder.Label()
		x.CreatedAt = time.Now().Format(time.RFC3339)
		x.Changes = out.Changes
		x.Error = ""
	})
	EventDraft.Broadcast(updated)
	s.logf("%s 重做完成", responder.Label())
	return updated, nil
}

// UpdateDraft 改一条待审草稿的正文（人工润色后再发）。
func (s *Service) UpdateDraft(id, body string) (Draft, error) {
	d, ok := s.drafts.Get(id)
	if !ok {
		return Draft{}, errors.New("草稿不存在")
	}
	if d.Status != "pending" {
		return d, fmt.Errorf("这条草稿的状态是 %s，不能修改", d.Status)
	}
	if strings.TrimSpace(body) == "" {
		return d, errors.New("正文不能为空")
	}
	out, _ := s.drafts.Update(id, func(x *Draft) { x.Body = body })
	EventDraft.Broadcast(out)
	return out, nil
}

// Logs 返回运行日志，旧的在前。
func (s *Service) Logs() []LogLine {
	return s.logs.List()
}

// OpenExternal 用系统浏览器打开链接（webview 里直接点链接会把页面导航走）。
func (s *Service) OpenExternal(url string) error {
	if url == "" {
		return errors.New("链接为空")
	}
	return mygo.Shell.OpenExternal(url)
}

func (s *Service) loop(ctx context.Context, r *agent.Runner, interval time.Duration) {
	defer func() {
		s.mu.Lock()
		if s.cancel != nil {
			// 只有自己还是当前那一轮时才清状态，避免 Stop 后重启被旧循环覆盖。
			s.running = false
		}
		st := s.statusLocked()
		s.mu.Unlock()
		EventStatus.Broadcast(st)
	}()
	for {
		handled, err := r.RunOnce(ctx)
		s.mu.Lock()
		s.lastPoll = time.Now()
		s.handled += handled
		if err != nil {
			s.lastErr = err.Error()
		} else {
			s.lastErr = ""
		}
		st := s.statusLocked()
		s.mu.Unlock()
		if err != nil {
			if ctx.Err() == nil {
				s.logf("本轮失败：%v", err)
			}
		} else {
			EventStatus.Broadcast(st)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// enqueue 是 Runner 的 OnDraft 回调：草稿进待审队列。
func (s *Service) enqueue(in agent.DraftInput) error {
	issue, comment := in.Issue, in.Comment
	d := Draft{
		ID:         fmt.Sprintf("%d", comment.ID),
		Repo:       in.Repo,
		Number:     issue.Number,
		Title:      issue.Title,
		IssueURL:   issue.HTMLURL,
		CommentURL: comment.HTMLURL,
		Author:     comment.User.Login,
		Body:       in.Body,
		Kind:       issueKind(issue),
		Type:       issueType(issue),
		Agent:      in.Agent,
		Prompt:     in.Prompt,
		Changes:    in.Changes,
		CreatedAt:  time.Now().Format(time.RFC3339),
		Status:     "pending",
	}
	if err := s.drafts.Add(d); err != nil {
		return err
	}
	EventDraft.Broadcast(d)
	s.logf("待审 %s#%d（%s），来自 %s", in.Repo, issue.Number, d.Author, d.Agent)
	s.mu.Lock()
	st := s.statusLocked()
	s.mu.Unlock()
	EventStatus.Broadcast(st)
	return nil
}

func (s *Service) logf(format string, args ...any) {
	line := s.logs.Add("info", fmt.Sprintf(format, args...))
	EventLog.Broadcast(line)
}

func (s *Service) statusLocked() StatusDTO {
	last := ""
	if !s.lastPoll.IsZero() {
		last = s.lastPoll.Format("15:04:05")
	}
	tasks := make([]TaskDTO, 0, len(s.tasks))
	for _, task := range s.tasks {
		tasks = append(tasks, *task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].StartedAt < tasks[j].StartedAt })
	return StatusDTO{
		Running:    s.running,
		Me:         s.me,
		LastPoll:   last,
		LastError:  s.lastErr,
		Handled:    s.handled,
		Pending:    s.drafts.Pending(),
		ConfigPath: s.cfgPath,
		Tasks:      tasks,
	}
}

// onTask 接住 Runner 的上报：开始出稿时拿这条目的锁，结束时放锁并清状态。
// 只堵同一个条目；手动审查别的条目时，轮询照常跑。
func (s *Service) onTask(t *agent.Task) {
	key := taskKey(t.Repo, t.Number)
	if t.Done {
		s.locks.Unlock(key)
		s.finishTask(key)
		return
	}
	s.locks.Lock(key)
	s.startTask(key, &TaskDTO{
		Repo:       t.Repo,
		Number:     t.Number,
		CommentURL: t.CommentURL,
		Agent:      t.Agent,
		StartedAt:  t.StartedAt.Format(time.RFC3339),
	}, t.Cancel)
}

func (s *Service) startTask(key string, task *TaskDTO, cancel context.CancelFunc) {
	s.mu.Lock()
	if s.tasks == nil {
		s.tasks = map[string]*TaskDTO{}
	}
	if s.taskCancel == nil {
		s.taskCancel = map[string]context.CancelFunc{}
	}
	s.tasks[key] = task
	if cancel != nil {
		s.taskCancel[key] = cancel
	}
	st := s.statusLocked()
	s.mu.Unlock()
	EventStatus.Broadcast(st)
}

func (s *Service) finishTask(key string) {
	s.mu.Lock()
	delete(s.tasks, key)
	delete(s.taskCancel, key)
	st := s.statusLocked()
	s.mu.Unlock()
	EventStatus.Broadcast(st)
}

// StopTask 停掉某个条目正在跑的出稿：轮询和手动审查都能停。
func (s *Service) StopTask(repo string, number int) error {
	key := taskKey(repo, number)
	s.mu.Lock()
	cancel := s.taskCancel[key]
	s.mu.Unlock()
	if cancel == nil {
		return errors.New("这个任务已经结束了")
	}
	cancel()
	s.logf("已请求停止 %s#%d", repo, number)
	return nil
}

func toDTO(c config.Config) ConfigDTO {
	perm := c.AgentPermission
	if perm == "" {
		perm = "bypass"
	}
	return ConfigDTO{
		Repos:               c.Repos,
		IntervalSeconds:     c.IntervalSeconds,
		OverlapSeconds:      c.OverlapSeconds,
		Trigger:             c.Trigger,
		MaxPerPoll:          c.MaxPerPoll,
		DryRun:              c.DryRun,
		AllowedAssociations: c.AllowedAssociations,
		Signature:           c.Signature,
		Agent:               c.Agent,
		AgentPermission:     perm,
		RepoPaths:           c.RepoPaths,
	}
}

func applyDTO(c config.Config, in ConfigDTO) config.Config {
	c.Repos = in.Repos
	c.IntervalSeconds = in.IntervalSeconds
	c.OverlapSeconds = in.OverlapSeconds
	c.Trigger = in.Trigger
	c.MaxPerPoll = in.MaxPerPoll
	c.DryRun = in.DryRun
	c.AllowedAssociations = in.AllowedAssociations
	c.Signature = in.Signature
	c.Agent = in.Agent
	c.AgentPermission = in.AgentPermission
	if in.RepoPaths != nil {
		c.RepoPaths = in.RepoPaths
	}
	return c
}
