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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ayuayue/gh-agent/internal/acp"
	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
	"github.com/ayuayue/gh-agent/internal/worktree"
	"github.com/egoist/mygo"
)

// Service 是绑定给前端的服务：mygo.Bind(desk.New(...))。
type Service struct {
	cfgPath string

	mu       sync.Mutex
	cfg      config.Config
	drafts   *draftStore
	logs     *logBuffer
	cancel   context.CancelFunc
	running  bool
	lastPoll time.Time
	lastErr  string
	handled  int
	me       string
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
	s := &Service{
		cfgPath: path,
		cfg:     cfg,
		drafts:  loadDrafts(filepath.Join(dir, "drafts.json")),
		logs:    newLogBuffer(logLimit),
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
func (s *Service) Config() ConfigDTO {
	s.mu.Lock()
	defer s.mu.Unlock()
	return toDTO(s.cfg)
}

// SaveConfig 校验并写盘；正在跑的话用新配置重启轮询。
func (s *Service) SaveConfig(in ConfigDTO) (ConfigDTO, error) {
	s.mu.Lock()
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
	wasRunning := s.running
	s.mu.Unlock()

	if wasRunning {
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
func (s *Service) Start() (StatusDTO, error) {
	s.mu.Lock()
	if s.running {
		st := s.statusLocked()
		s.mu.Unlock()
		return st, nil
	}
	cfg := s.cfg
	if err := cfg.Validate(); err != nil {
		s.lastErr = err.Error()
		st := s.statusLocked()
		s.mu.Unlock()
		return st, err
	}
	s.cfg = cfg
	// 审核模式：出稿进队列，不发帖。
	cfg.DryRun = false
	runner, err := bootstrap.NewRunner(cfg, bootstrap.Options{Logf: s.logf, OnDraft: s.enqueue})
	if err != nil {
		s.lastErr = err.Error()
		st := s.statusLocked()
		s.mu.Unlock()
		s.logf("启动失败：%v", err)
		return st, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true
	s.me = runner.Me
	s.lastErr = ""
	interval := s.cfg.Interval()
	st := s.statusLocked()
	s.mu.Unlock()

	s.logf("开始轮询 %v，间隔 %s", s.cfg.Repos, interval)
	EventStatus.Broadcast(st)
	go s.loop(ctx, runner, interval)
	return st, nil
}

// Stop 停止轮询（正在跑的那一轮会跑完）。
func (s *Service) Stop() StatusDTO {
	s.mu.Lock()
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

	branch, commit := "", ""
	if d.Changes != nil && len(d.Changes.Files) > 0 {
		var err error
		branch, commit, err = commitDraft(cfg, d)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ghapi.New(token).PostIssueComment(ctx, d.Repo, d.Number, d.Body); err != nil {
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
	return out, nil
}

// commitDraft 把草稿涉及的改动提交到一个新分支，返回分支名和提交 sha。
func commitDraft(cfg config.Config, d Draft) (string, string, error) {
	dir := bootstrap.RepoWorkdir(cfg, d.Repo)
	if dir == "" {
		return "", "", errors.New("找不到这个仓库的本地目录")
	}
	branch := nextBranchName(dir, "gh-agent/issue-"+strconv.Itoa(d.Number))
	sha, err := worktree.CommitToBranch(dir, branch, commitMessage(d), d.Changes.Files)
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

func commitMessage(d Draft) string {
	head := "gh-agent: 回复 #" + strconv.Itoa(d.Number)
	if d.Title != "" {
		head += " " + d.Title
	}
	return head + "\n\n" +
		"由 " + d.Agent + " 生成，经人工审阅后提交。\n" +
		d.CommentURL + "\n"
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

	out := []AgentInfo{{
		ID:        "llm",
		Name:      "模型端点",
		Desc:      cfg.Provider.Model + " @ " + cfg.Provider.BaseURL,
		Available: strings.TrimSpace(cfg.Provider.BaseURL) != "" && strings.TrimSpace(cfg.Provider.Model) != "",
		Detail:    "读不到代码，只能看 prompt 里的上下文",
		Current:   cfg.Agent == "" || cfg.Agent == "llm",
	}}
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

	responder, err := bootstrap.ResponderFor(cfg, agentID, s.logf)
	if err != nil {
		return d, err
	}
	s.logf("用 %s 重做 %s#%d…", responder.Label(), d.Repo, d.Number)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
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
	return StatusDTO{
		Running:    s.running,
		Me:         s.me,
		LastPoll:   last,
		LastError:  s.lastErr,
		Handled:    s.handled,
		Pending:    s.drafts.Pending(),
		ConfigPath: s.cfgPath,
	}
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
		BaseURL:             c.Provider.BaseURL,
		Model:               c.Provider.Model,
		APIKeyEnv:           c.Provider.APIKeyEnv,
		TimeoutSeconds:      c.Provider.TimeoutSeconds,
		MaxOutputTokens:     c.Provider.MaxOutputTokens,
		Temperature:         c.Provider.Temperature,
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
	c.Provider.BaseURL = in.BaseURL
	c.Provider.Model = in.Model
	c.Provider.APIKeyEnv = in.APIKeyEnv
	c.Provider.TimeoutSeconds = in.TimeoutSeconds
	c.Provider.MaxOutputTokens = in.MaxOutputTokens
	c.Provider.Temperature = in.Temperature
	c.Agent = in.Agent
	c.AgentPermission = in.AgentPermission
	if in.RepoPaths != nil {
		c.RepoPaths = in.RepoPaths
	}
	return c
}
