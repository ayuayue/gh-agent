package acp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayuayue/gh-agent/internal/acp"
	"github.com/ayuayue/gh-agent/internal/agent"
)

func smokeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		full := append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# demo" + string(rune(10))), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

// TestSmokeAgentEditsRepo 验证"agent 真的改了代码并且改动被捕获"。
// 手动跑：ACP_EDIT=codebuddy go test ./internal/acp -run TestSmokeAgentEditsRepo -v
func TestSmokeAgentEditsRepo(t *testing.T) {
	id := os.Getenv("ACP_EDIT")
	if id == "" {
		t.Skip("设置 ACP_EDIT=codebuddy 或 pi 才跑")
	}
	dir := smokeRepo(t)

	resp := &acp.Responder{
		AgentID: id,
		Workdir: func(string) string { return dir },
		Timeout: 6 * time.Minute,
		Logf:    t.Logf,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	reply, err := resp.Respond(ctx, agent.Request{
		Repo:   "o/r",
		System: "你在一个仓库里工作。",
		User:   "在当前仓库根目录新建一个文件 NOTES.md，内容写一行 hello。然后只回复一句话说明你做了什么。",
	})
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	t.Logf("回复：%q", reply.Text)

	if reply.Changes == nil {
		t.Fatal("agent 改了文件但没捕获到改动")
	}
	t.Logf("改动文件：%v，未跟踪：%v", reply.Changes.Files, reply.Changes.Untracked)

	if len(reply.Changes.Files) == 0 {
		t.Fatal("改动的文件列表为空")
	}
	found := false
	for _, f := range reply.Changes.Files {
		if strings.Contains(f, "NOTES.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("改动里应该有 NOTES.md，实际 %v", reply.Changes.Files)
	}
	if _, err := os.Stat(filepath.Join(dir, "NOTES.md")); err != nil {
		t.Errorf("文件应该真的落在工作区：%v", err)
	}
}
