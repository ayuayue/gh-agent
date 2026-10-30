package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("应生成默认配置文件: %v", err)
	}
	if cfg.IntervalSeconds != 120 || cfg.Trigger != "@claude" {
		t.Errorf("默认值不对: %+v", cfg)
	}
}

func TestLoadMergesPartialConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"repos":["o/r"],"provider":{"model":"m"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0] != "o/r" {
		t.Errorf("repos 未生效: %+v", cfg.Repos)
	}
	if cfg.Provider.Model != "m" {
		t.Errorf("provider.model 未覆盖: %q", cfg.Provider.Model)
	}
	if cfg.Provider.BaseURL == "" {
		t.Error("未写的字段应保留默认值")
	}
}

func TestValidate(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err == nil {
		t.Error("空 repos 应报错")
	}
	cfg.Repos = []string{"no-slash"}
	if err := cfg.Validate(); err == nil {
		t.Error("非法仓库名应报错")
	}
	cfg.Repos = []string{"owner/name"}
	cfg.IntervalSeconds = 1
	cfg.MaxPerPoll = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
	if cfg.IntervalSeconds < 15 || cfg.MaxPerPoll < 1 {
		t.Errorf("越界值应收回安全范围: %+v", cfg)
	}
}
