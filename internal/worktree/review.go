package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BaseBranch 返回合并的基准分支：有 dev 就用 dev，否则用当前检出的分支。
func BaseBranch(repo string) (string, error) {
	if BranchExists(repo, "dev") {
		return "dev", nil
	}
	out, err := git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "", errors.New("当前是 detached HEAD，找不到基准分支")
	}
	return branch, nil
}

// AddWorktree 在 dir 建一个独立工作区，检出到新分支 branch（基于 base）。
// 这个过程不碰用户当前的分支和工作区。
func AddWorktree(repo, dir, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	_, _ = git(repo, "worktree", "prune")
	if _, err := git(repo, "worktree", "add", "-b", branch, dir, base); err != nil {
		return fmt.Errorf("建 worktree 失败：%w", err)
	}
	return nil
}

// RemoveWorktree 摘掉工作区并清掉目录（失败也继续，留个残留目录比报错好）。
func RemoveWorktree(repo, dir string) {
	_, _ = git(repo, "worktree", "remove", "--force", dir)
	_ = os.RemoveAll(dir)
	_, _ = git(repo, "worktree", "prune")
}

// DeleteBranch 删掉一个本地分支。
func DeleteBranch(repo, branch string) error {
	_, err := git(repo, "branch", "-D", branch)
	return err
}

// CommitAll 把工作区里的全部改动提交掉，返回 sha；没改动返回空串。
// 只在隔离 worktree 里用：那里一开始是干净的，add -A 不会卷进别人的改动。
func CommitAll(dir, message string) (string, error) {
	if _, err := git(dir, "add", "-A"); err != nil {
		return "", fmt.Errorf("暂存改动失败：%w", err)
	}
	if _, err := git(dir, "diff", "--cached", "--quiet"); err == nil {
		return "", nil
	}
	if _, err := git(dir, "commit", "-q", "-m", message); err != nil {
		if _, fallback := git(dir, "-c", "user.name=gh-agent", "-c", "user.email=gh-agent@users.noreply.github.com", "commit", "-q", "-m", message); fallback != nil {
			return "", fmt.Errorf("提交失败：%w", fallback)
		}
	}
	sha, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

// CurrentBranch 返回当前检出的分支名。
func CurrentBranch(repo string) (string, error) {
	out, err := git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CanMerge 判断能不能把分支合进 base：主仓库必须正检出于 base，
// 且没有未提交改动——否则合并会把人的半成品一起搅进来。
func CanMerge(repo, base string) error {
	current, err := CurrentBranch(repo)
	if err != nil {
		return err
	}
	if current != base {
		return fmt.Errorf("当前在 %s，不是 %s", current, base)
	}
	status, err := git(repo, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("工作区有未提交改动")
	}
	return nil
}

// MergeBranch 把 branch 合并进当前分支（--no-ff，保留合并记录）。
func MergeBranch(repo, branch, message string) error {
	_, err := git(repo, "merge", "--no-ff", "--no-edit", "-m", message, branch)
	return err
}

// ProbeMerge 只算不合：把 branch 合进 base 会不会冲突，不切分支、不动工作区、不建提交。
// 返回冲突的文件路径，空表示能自动合（也就是「合了不影响基准分支」）。
// git 太老（没有 merge-tree --write-tree）时返回错误，调用方当「判断不了」处理。
func ProbeMerge(repo, base, branch string) ([]string, error) {
	out, code, err := gitRun(repo, "merge-tree", "--write-tree", "--name-only", base, branch)
	if err != nil {
		return nil, err
	}
	switch code {
	case 0:
		return nil, nil
	case 1:
		return conflictPaths(out), nil
	default:
		return nil, fmt.Errorf("git merge-tree 返回 %d", code)
	}
}

// conflictPaths 从 merge-tree --write-tree 的输出里取冲突文件：
// 第一行是结果树 oid，之后到第一个空行为止才是冲突路径（再往后是给人看的说明）。
func conflictPaths(out string) []string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	files := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		files = append(files, line)
	}
	if len(files) == 0 {
		files = []string{"（git 没给出冲突文件名）"}
	}
	return files
}
