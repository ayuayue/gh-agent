package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayuayue/gh-agent/internal/worktree"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func setupRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestCollectDetectsEditAndNewFile(t *testing.T) {
	dir := setupRepo(t)

	before := worktree.Capture(dir)
	if !before.Repo {
		t.Fatal("应识别成 git 仓库")
	}
	if len(before.Files) != 0 {
		t.Fatalf("干净的仓库不该有改动，得到 %v", before.Files)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	after := worktree.Capture(dir)
	changed := worktree.Changed(before, after)
	if len(changed) != 2 {
		t.Fatalf("应检测到 2 个文件变动，得到 %v", changed)
	}

	res := worktree.Collect(dir, before, after, changed, 0)
	if res == nil {
		t.Fatal("Collect 不该返回 nil")
	}
	if !strings.Contains(res.Diff, "+two") {
		t.Errorf("diff 里应有新增行，得到 %q", res.Diff)
	}
	if len(res.Untracked) != 1 || res.Untracked[0] != "b.txt" {
		t.Errorf("新文件应归到 untracked，得到 %v", res.Untracked)
	}
	if res.DirtyBefore {
		t.Error("开始时是干净的，DirtyBefore 应为 false")
	}
}

func TestCollectSkipsWhenNothingChanged(t *testing.T) {
	dir := setupRepo(t)
	before := worktree.Capture(dir)
	after := worktree.Capture(dir)
	if changed := worktree.Changed(before, after); len(changed) != 0 {
		t.Fatalf("没动过不该有改动，得到 %v", changed)
	}
	if res := worktree.Collect(dir, before, after, nil, 0); res != nil {
		t.Error("没有改动时应返回 nil")
	}
}

func TestCaptureMarksDirtyBefore(t *testing.T) {
	dir := setupRepo(t)
	// 先弄脏再跑，模拟"工作区本来就有人没提交的改动"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := worktree.Capture(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("local\nagent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := worktree.Capture(dir)
	res := worktree.Collect(dir, before, after, worktree.Changed(before, after), 0)
	if res == nil || !res.DirtyBefore {
		t.Error("跑之前就有改动时 DirtyBefore 应为 true")
	}
}

func TestCaptureNonRepo(t *testing.T) {
	dir := t.TempDir()
	if snap := worktree.Capture(dir); snap.Repo {
		t.Error("不是 git 仓库时 Repo 应为 false")
	}
}

func TestCommitToBranchLeavesUserWorkAlone(t *testing.T) {
	dir := setupRepo(t)

	// 用户自己的未提交改动（跟这次提交无关）
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("user wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	// agent 改的文件
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("agent change"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	sha, err := worktree.CommitToBranch(dir, "gh-agent/issue-447", "test commit", []string{"a.txt"})
	if err != nil {
		t.Fatalf("CommitToBranch: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("应返回完整 sha，得到 %q", sha)
	}

	// 当前分支不能变
	if after := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); after != before {
		t.Errorf("当前分支被改动了：%s -> %s", before, after)
	}

	// 用户的脏工作区要原封不动
	if status := gitOut(t, dir, "status", "--porcelain"); !strings.Contains(status, "local.txt") {
		t.Errorf("用户未提交的文件应还在，status=%q", status)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(body) != "agent change" {
		t.Errorf("工作区里 agent 的改动应保留，得到 %q err=%v", body, err)
	}

	// 新分支上只有这次改动的文件
	if got := gitOut(t, dir, "show", "gh-agent/issue-447:a.txt"); got != "agent change" {
		t.Errorf("分支上的 a.txt 应为 agent 的内容，得到 %q", got)
	}
	if _, err := exec.Command("git", "-C", dir, "cat-file", "-e", "gh-agent/issue-447:local.txt").CombinedOutput(); err == nil {
		t.Error("用户自己的 local.txt 不该被卷进这次提交")
	}

	// 临时 worktree 要清理掉
	if list := gitOut(t, dir, "worktree", "list"); strings.Count(list, "\n") > 2 {
		t.Errorf("临时 worktree 没清理干净：%s", list)
	}
}

func TestCommitToBranchNoChanges(t *testing.T) {
	dir := setupRepo(t)
	if _, err := worktree.CommitToBranch(dir, "gh-agent/empty", "msg", []string{"a.txt"}); err == nil {
		t.Error("没有实际改动时不该造空提交")
	}
}

// TestProbeMergeReportsConflicts 探针要能看出「合进来会不会冲突」，且不动工作区。
func TestProbeMergeReportsConflicts(t *testing.T) {
	dir := setupRepo(t)
	base := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	// 分支上改 a.txt
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("branch side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-q", "-am", "branch")
	gitRun(t, dir, "checkout", "-q", base)

	// 基准分支上改同一行，两边合起来必冲突
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "commit", "-q", "-am", "base")

	head := gitOut(t, dir, "rev-parse", "HEAD")
	conflicts, err := worktree.ProbeMerge(dir, base, "feature")
	if err != nil {
		t.Skipf("git 太老，没有 merge-tree --write-tree：%v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != "a.txt" {
		t.Errorf("应报出冲突文件 a.txt，得到 %v", conflicts)
	}
	// 探针碰过的东西：不能切分支、不能建提交、不能留脏文件
	if now := gitOut(t, dir, "rev-parse", "HEAD"); now != head {
		t.Errorf("探针不该动 HEAD：%s -> %s", head, now)
	}
	if now := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); now != base {
		t.Errorf("探针不该切分支，得到 %s", now)
	}
	if status := gitOut(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("探针不该留下未提交改动：%q", status)
	}
}

// TestProbeMergeCleanBranch 不冲突时返回空，调用方据此直接合。
func TestProbeMergeCleanBranch(t *testing.T) {
	dir := setupRepo(t)
	base := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "b.txt")
	gitRun(t, dir, "commit", "-q", "-m", "branch")
	gitRun(t, dir, "checkout", "-q", base)

	conflicts, err := worktree.ProbeMerge(dir, base, "feature")
	if err != nil {
		t.Skipf("git 太老，没有 merge-tree --write-tree：%v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("不该报冲突，得到 %v", conflicts)
	}
}

// gitOut 跑一条 git 命令并返回输出（去尾部空白）。
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
