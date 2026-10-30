package desk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/worktree"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestCommitDraftCreatesBranch(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("agent fixed it"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.RepoPaths = map[string]string{"o/r": dir}
	d := Draft{
		Repo:       "o/r",
		Number:     447,
		Title:      "某个问题",
		Agent:      "CodeBuddy",
		CommentURL: "https://github.com/o/r/issues/447#issuecomment-1",
		Changes:    &worktree.Result{Files: []string{"a.txt"}},
	}

	branch, sha, err := commitDraft(cfg, d)
	if err != nil {
		t.Fatalf("commitDraft: %v", err)
	}
	if branch != "gh-agent/issue-447" {
		t.Errorf("分支名不对：%q", branch)
	}
	if len(sha) != 40 {
		t.Errorf("应返回提交 sha，得到 %q", sha)
	}

	msg := gitOutIn(t, dir, "log", "-1", "--format=%s", branch)
	if !strings.Contains(msg, "#447") {
		t.Errorf("提交信息应带上 issue 号，得到 %q", msg)
	}
	if body := gitOutIn(t, dir, "show", branch+":a.txt"); body != "agent fixed it" {
		t.Errorf("分支上的内容不对：%q", body)
	}
}

func TestCommitDraftSuffixesWhenBranchExists(t *testing.T) {
	dir := initRepo(t)
	gitIn(t, dir, "branch", "gh-agent/issue-7")
	if got := nextBranchName(dir, "gh-agent/issue-7"); got != "gh-agent/issue-7-2" {
		t.Errorf("撞名时应加后缀，得到 %q", got)
	}
}

func TestCommitDraftWithoutWorkdir(t *testing.T) {
	cfg := config.Default()
	cfg.RepoPaths = map[string]string{}
	d := Draft{Repo: "o/r", Number: 1, Changes: &worktree.Result{Files: []string{"a.txt"}}}
	// 没有配本地目录时会用配置目录下的空目录，那里不是 git 仓库
	if _, _, err := commitDraft(cfg, d); err == nil {
		t.Error("不是 git 仓库时应该报错")
	}
}

func gitOutIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
