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
	"strings"
	"time"
)

// Provider 是模型端点配置。kind 目前只支持 openai（任何 OpenAI 兼容端点：
// magpie / ollama / vllm / 中转站）。
type Provider struct {
	Kind            string  `json:"kind"`
	BaseURL         string  `json:"base_url"`
	Model           string  `json:"model"`
	APIKeyEnv       string  `json:"api_key_env"`
	APIKey          string  `json:"api_key"`
	TimeoutSeconds  int     `json:"timeout_seconds"`
	MaxOutputTokens int     `json:"max_output_tokens"`
	Temperature     float64 `json:"temperature"`
}

// Config 是完整配置。所有字段都有默认值，缺字段的文件按默认补齐。
type Config struct {
	Repos               []string `json:"repos"`
	IntervalSeconds     int      `json:"interval_seconds"`
	OverlapSeconds      int      `json:"overlap_seconds"`
	Trigger             string   `json:"trigger"`
	MaxPerPoll          int      `json:"max_per_poll"`
	DryRun              bool     `json:"dry_run"`
	AllowedAssociations []string `json:"allowed_associations"`
	Provider            Provider `json:"provider"`
	Signature           string   `json:"signature"`
	InstructionsFile    string   `json:"instructions_file"`
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
		Provider: Provider{
			Kind:            "openai",
			BaseURL:         "http://127.0.0.1:3425/v1",
			Model:           "auto/deepseek-v4.1-flash",
			APIKeyEnv:       "MAGPIE_API_KEY",
			TimeoutSeconds:  180,
			MaxOutputTokens: 1200,
			Temperature:     0.2,
		},
		Signature:        "*本条由本地 AI 助手自动生成，结论请以代码为准。*",
		InstructionsFile: "",
	}
}

// Dir 返回配置目录（不存在则创建）。
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "gh-agent")
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
	return cfg, nil
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
	if len(c.Repos) == 0 {
		return errors.New("配置里 repos 为空：先填一个 owner/name")
	}
	for _, repo := range c.Repos {
		if strings.Count(repo, "/") != 1 || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") {
			return fmt.Errorf("仓库名必须形如 owner/name，收到 %q", repo)
		}
	}
	if strings.TrimSpace(c.Provider.BaseURL) == "" {
		return errors.New("provider.base_url 不能为空")
	}
	if strings.TrimSpace(c.Provider.Model) == "" {
		return errors.New("provider.model 不能为空")
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
	if c.Provider.TimeoutSeconds < 10 {
		c.Provider.TimeoutSeconds = 10
	}
	if c.Provider.MaxOutputTokens < 128 {
		c.Provider.MaxOutputTokens = 128
	}
	if c.Trigger == "" {
		c.Trigger = "@claude"
	}
	if len(c.AllowedAssociations) == 0 {
		c.AllowedAssociations = Default().AllowedAssociations
	}
	return nil
}

// Interval 是轮询间隔。
func (c Config) Interval() time.Duration {
	return time.Duration(c.IntervalSeconds) * time.Second
}

// Overlap 是游标回退窗口。
func (c Config) Overlap() time.Duration {
	return time.Duration(c.OverlapSeconds) * time.Second
}
