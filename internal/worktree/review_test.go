package worktree_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayuayue/gh-agent/internal/worktree"
)

func TestIsolatedWorkspaceLifecycle(t *testing.T) {
	repo := setupRepo(t)
	initial, err := worktree.CurrentBranch(repo)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	gitRun(t, repo, "branch", "dev")

	base, err := worktree.BaseBranch(repo)
	if err != nil {
		t.Fatalf("BaseBranch: %v", err)
	}
	if base != "dev" {
		t.Fatalf("有 dev 时应优先用 dev，得到 %q", base)
	}

	dir := filepath.Join(t.TempDir(), "wt")
	if err := worktree.AddWorktree(repo, dir, "gh-agent/issue-1", base); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sha, err := worktree.CommitAll(dir, "[gh-agent] fix")
	if err != nil || sha == "" {
		t.Fatalf("CommitAll: sha=%q err=%v", sha, err)
	}

	// 主仓库不能被切走。
	if current, _ := worktree.CurrentBranch(repo); current != initial {
		t.Errorf("主仓库分支被改成了 %q，应保持 %q", current, initial)
	}
	// 主仓库的工作区也不能被动过。
	if body, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(body) != "one\n" {
		t.Errorf("主仓库文件被改了：%q", body)
	}

	worktree.RemoveWorktree(repo, dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("worktree 目录应被删掉")
	}
	if !worktree.BranchExists(repo, "gh-agent/issue-1") {
		t.Error("分支应保留")
	}

	// 主仓库还在原分支：不允许合到 dev。
	if err := worktree.CanMerge(repo, "dev"); err == nil {
		t.Error("不在 dev 上时不该允许合并")
	}

	gitRun(t, repo, "checkout", "-q", "dev")
	if err := worktree.CanMerge(repo, "dev"); err != nil {
		t.Fatalf("干净且在 dev 上应允许合并：%v", err)
	}
	if err := worktree.MergeBranch(repo, "gh-agent/issue-1", "[gh-agent] merge"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(repo, "a.txt")); strings.ReplaceAll(string(body), "\r\n", "\n") != "fixed\n" {
		t.Errorf("合并后内容不对：%q", body)
	}

	// 有未提交改动时拒绝合并。
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := worktree.CanMerge(repo, "dev"); err == nil {
		t.Error("脏工作区不该允许合并")
	}
}
