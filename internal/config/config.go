// Package config 负责 gh-agent 的配置读写与校验。
//
// 配置落在系统用户配置目录（Windows: %AppData%\gh-agent\config.json），
// 首次运行自动生成默认文件，用户直接改 JSON 即可。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Config 是完整配置。所有字段都有默认值，缺字段的文件按默认补齐。
type Config struct {
	Repos               []string `json:"repos"`
	IntervalSeconds     int      `json:"interval_seconds"`
	OverlapSeconds      int      `json:"overlap_seconds"`
	Trigger             string   `json:"trigger"`
	MaxPerPoll          int      `json:"max_per_poll"`
	DryRun              bool     `json:"dry_run"`
	AllowedAssociations []string `json:"allowed_associations"`
	Signature           string   `json:"signature"`
	InstructionsFile    string   `json:"instructions_file"`
	// Agent 决定谁出稿："llm"（默认，打 provider 端点）或本地 agent 的 id（pi / codebuddy）。
	Agent string `json:"agent"`
	// AgentPermission 控制本地 agent 能用哪些工具：
	// bypass（全部放行）/ write（读写文件，禁命令）/ read（只读）/ deny（全拒）。
	AgentPermission string `json:"agent_permission"`
	// RepoPaths 把仓库映射到本地工作目录；本地 agent 在这个目录里跑，
	// 才能读代码、跑命令。留空则用配置目录下的 work/<repo> 空目录。
	RepoPaths map[string]string `json:"repo_paths"`
	// AgentOptions 按 agent id 记调用参数：模型和模式（比如 CodeBuddy 的 PTC 模式）。
	// 空值表示用 agent 自己的默认。
	AgentOptions map[string]AgentOption `json:"agent_options"`
}

// AgentOption 是某个出稿方的调用参数。
type AgentOption struct {
	Model string `json:"model,omitempty"`
	Mode  string `json:"mode,omitempty"`
}

// Default 返回一份可直接使用的默认配置：仓库留空（必须先填），
// 模型指向本机 magpie。
func Default() Config {
	return Config{
		Repos:               []string{},
		IntervalSeconds:     120,
		OverlapSeconds:      120,
		Trigger:             "@claude",
		MaxPerPoll:          2,
		DryRun:              true,
		AllowedAssociations: []string{"OWNER", "MEMBER", "COLLABORATOR", "CONTRIBUTOR"},
		Signature:           "*由 agent 协助生成*",
		InstructionsFile:    "",
		Agent:               "",
		AgentPermission:     "bypass",
		RepoPaths:           map[string]string{},
	}
}

// Dir 返回配置目录（不存在则创建）。
// GH_AGENT_HOME 可以覆盖它，用来同时跑打包版和开发版：
// 两边各用各的 config / state / drafts，不会互相踩。
func Dir() (string, error) {
	dir := strings.TrimSpace(os.Getenv("GH_AGENT_HOME"))
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "gh-agent")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// DefaultPath 是默认配置文件路径。
func DefaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load 读取配置：文件不存在时生成默认配置并落盘（返回空仓库列表，跑起来会提示先填）。
// 缺失字段按默认值补齐——用户只需要写想改的那几项。
func Load(path string) (Config, error) {
	if path == "" {
		resolved, err := DefaultPath()
		if err != nil {
			return Config{}, err
		}
		path = resolved
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("读取配置 %s: %w", path, err)
		}
		cfg := Default()
		if err := cfg.Save(path); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	// 配了本地目录的仓库默认就监控，不用在 repos 里再写一遍。
	cfg.Normalize()
	return cfg, nil
}

// MonitoredRepos 返回实际要轮询的仓库：repos 加上 repo_paths 里的键。
// 顺序是 repos 在前，映射里多出来的按名字排。
func (c Config) MonitoredRepos() []string {
	out := make([]string, 0, len(c.Repos)+len(c.RepoPaths))
	seen := make(map[string]bool, cap(out))
	add := func(repo string) {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			return
		}
		key := strings.ToLower(repo)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, repo)
	}
	for _, repo := range c.Repos {
		add(repo)
	}
	keys := make([]string, 0, len(c.RepoPaths))
	for repo := range c.RepoPaths {
		keys = append(keys, repo)
	}
	sort.Strings(keys)
	for _, repo := range keys {
		add(repo)
	}
	return out
}

// Normalize 把 repo_paths 里的仓库并进 repos，并补齐 map 字段。Load 之后调用，
// 这样轮询、界面、状态文件看到的都是同一份仓库列表。
func (c *Config) Normalize() {
	c.Repos = c.MonitoredRepos()
	if c.RepoPaths == nil {
		c.RepoPaths = map[string]string{}
	}
	if c.AgentOptions == nil {
		c.AgentOptions = map[string]AgentOption{}
	}
}

// Save 原子写配置（同目录临时文件 + rename）。
func (c Config) Save(path string) error {
	if path == "" {
		resolved, err := DefaultPath()
		if err != nil {
			return err
		}
		path = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate 校验必填项，并把明显不合理的数值收回到安全范围。
func (c *Config) Validate() error {
	repos := c.MonitoredRepos()
	if len(repos) == 0 {
		return errors.New("配置里没有仓库：先填一个 owner/name")
	}
	for _, repo := range repos {
		if strings.Count(repo, "/") != 1 || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") {
			return fmt.Errorf("仓库名必须形如 owner/name，收到 %q", repo)
		}
	}
	if c.IntervalSeconds < 15 {
		c.IntervalSeconds = 15
	}
	if c.OverlapSeconds < 0 {
		c.OverlapSeconds = 0
	}
	if c.MaxPerPoll < 1 {
		c.MaxPerPoll = 1
	}
	// trigger 允许为空：空表示任何 issue / PR 评论都处理，不强制回填。
	if len(c.AllowedAssociations) == 0 {
		c.AllowedAssociations = Default().AllowedAssociations
	}
	c.Agent = strings.TrimSpace(c.Agent)
	if c.Agent == "" {
		return errors.New("还没选出稿方：在设置里选 CodeBuddy / Codex / Claude Code / pi")
	}
	c.AgentPermission = strings.TrimSpace(strings.ToLower(c.AgentPermission))
	switch c.AgentPermission {
	case "bypass", "write", "read":
		// ok
	case "deny":
		// ok
	default:
		// 老配置没这个字段，保持原有行为（全部放行），不要突然把 agent 卡死。
		c.AgentPermission = "bypass"
	}
	if c.RepoPaths == nil {
		c.RepoPaths = map[string]string{}
	}
	if c.AgentOptions == nil {
		c.AgentOptions = map[string]AgentOption{}
	}
	for id, opt := range c.AgentOptions {
		opt.Model = strings.TrimSpace(opt.Model)
		opt.Mode = strings.TrimSpace(opt.Mode)
		if opt.Model == "" && opt.Mode == "" {
			delete(c.AgentOptions, id)
			continue
		}
		c.AgentOptions[id] = opt
	}
	return nil
}

// AgentOptionFor 返回某个 agent 的调用参数。
func (c Config) AgentOptionFor(id string) AgentOption {
	return c.AgentOptions[id]
}

// Interval 是轮询间隔。
func (c Config) Interval() time.Duration {
	return time.Duration(c.IntervalSeconds) * time.Second
}

// Overlap 是游标回退窗口。
func (c Config) Overlap() time.Duration {
	return time.Duration(c.OverlapSeconds) * time.Second
}
