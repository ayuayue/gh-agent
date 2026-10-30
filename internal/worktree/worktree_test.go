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
