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
	if err := os.WriteFile(path, []byte(`{"repos":["o/r"],"interval_seconds":30}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0] != "o/r" {
		t.Errorf("repos 未生效: %+v", cfg.Repos)
	}
	if cfg.IntervalSeconds != 30 {
		t.Errorf("interval_seconds 未覆盖: %d", cfg.IntervalSeconds)
	}
	if cfg.Trigger != "@claude" {
		t.Errorf("未写的字段应保留默认值: %q", cfg.Trigger)
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
	cfg.Agent = "codebuddy"
	cfg.IntervalSeconds = 1
	cfg.MaxPerPoll = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
	if cfg.IntervalSeconds < 15 || cfg.MaxPerPoll < 1 {
		t.Errorf("越界值应收回安全范围: %+v", cfg)
	}
}

func TestMonitoredReposMergesPaths(t *testing.T) {
	cfg := Default()
	cfg.Repos = []string{"o/explicit"}
	cfg.RepoPaths = map[string]string{"o/mapped": "D:/x"}
	got := cfg.MonitoredRepos()
	if len(got) != 2 || got[0] != "o/explicit" || got[1] != "o/mapped" {
		t.Fatalf("应把映射里的仓库并进来，得到 %v", got)
	}

	// 只有映射、没有 repos 也应该能跑。
	cfg = Default()
	cfg.Agent = "codebuddy"
	cfg.RepoPaths = map[string]string{"o/mapped": "D:/x"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("只有 repo_paths 时应通过校验: %v", err)
	}

	// 重复（大小写不同）只保留一个。
	cfg = Default()
	cfg.Repos = []string{"o/r"}
	cfg.RepoPaths = map[string]string{"O/R": "D:/x"}
	if got := cfg.MonitoredRepos(); len(got) != 1 {
		t.Errorf("重复仓库应去重，得到 %v", got)
	}
}

func TestLoadMergesRepoPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"repo_paths":{"o/mapped":"D:/x"}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0] != "o/mapped" {
		t.Errorf("映射里的仓库应并进 repos，得到 %v", cfg.Repos)
	}
}

func TestValidateRequiresAgent(t *testing.T) {
	cfg := Default()
	cfg.Repos = []string{"o/r"}
	if err := cfg.Validate(); err == nil {
		t.Error("没选出稿方应报错")
	}
	cfg.Agent = "codebuddy"
	if err := cfg.Validate(); err != nil {
		t.Errorf("选了出稿方应通过: %v", err)
	}
}
