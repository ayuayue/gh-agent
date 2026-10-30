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
	"path/filepath"
	"sync"
	"time"

	"github.com/ayuayue/gh-agent/internal/agent"
	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/ghapi"
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
func (s *Service) Approve(id string) (Draft, error) {
	d, ok := s.drafts.Get(id)
	if !ok {
		return Draft{}, errors.New("草稿不存在")
	}
	if d.Status != "pending" {
		return d, fmt.Errorf("这条草稿的状态是 %s，不能发送", d.Status)
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
	})
	EventDraft.Broadcast(out)
	s.logf("已发送 %s#%d", d.Repo, d.Number)
	return out, nil
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
func (s *Service) enqueue(repo string, issue ghapi.Issue, comment ghapi.Comment, body string) error {
	d := Draft{
		ID:         fmt.Sprintf("%d", comment.ID),
		Repo:       repo,
		Number:     issue.Number,
		Title:      issue.Title,
		IssueURL:   issue.HTMLURL,
		CommentURL: comment.HTMLURL,
		Author:     comment.User.Login,
		Body:       body,
		CreatedAt:  time.Now().Format(time.RFC3339),
		Status:     "pending",
	}
	if err := s.drafts.Add(d); err != nil {
		return err
	}
	EventDraft.Broadcast(d)
	s.logf("待审 %s#%d（%s）", repo, issue.Number, d.Author)
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
	return c
}
