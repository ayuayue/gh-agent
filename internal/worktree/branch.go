package worktree

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CommitToBranch 把 paths 的当前内容提交到一个新分支。
//
// 实现方式是临时 worktree：在旁边的临时目录里检出 HEAD、把改动的文件拷过去、
// 提交，然后把这个 worktree 摘掉。整个过程不碰用户的分支、索引和工作区，
// 所以哪怕工作区本来是脏的，也不会把用户未提交的东西卷进这次提交。
func CommitToBranch(repo, branch, message string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("没有要提交的文件")
	}
	if _, err := git(repo, "rev-parse", "--show-toplevel"); err != nil {
		return "", errors.New("工作目录不是 git 仓库")
	}

	tmp, err := os.MkdirTemp("", "gh-agent-wt-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if _, err := git(repo, "worktree", "add", "-b", branch, tmp, "HEAD"); err != nil {
		return "", fmt.Errorf("建分支 %s 失败：%w", branch, err)
	}
	defer git(repo, "worktree", "remove", "--force", tmp)

	if err := copyPaths(repo, tmp, paths); err != nil {
		return "", err
	}

	addArgs := append([]string{"add", "-A", "--"}, paths...)
	if _, err := git(tmp, addArgs...); err != nil {
		return "", fmt.Errorf("暂存改动失败：%w", err)
	}

	// 没有实际变更时不要造空提交。
	if _, err := git(tmp, "diff", "--cached", "--quiet"); err == nil {
		return "", errors.New("改动与 HEAD 一致，没有可提交内容")
	}

	if _, err := git(tmp, "commit", "-q", "-m", message); err != nil {
		// 仓库没配 user.name / user.email 时兜底，别让整件事失败。
		if _, err2 := git(tmp, "-c", "user.name=gh-agent", "-c", "user.email=gh-agent@users.noreply.github.com", "commit", "-q", "-m", message); err2 != nil {
			return "", fmt.Errorf("提交失败：%w", err2)
		}
	}

	sha, err := git(tmp, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

// BranchExists 判断分支是否已存在。
func BranchExists(repo, branch string) bool {
	_, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// copyPaths 把源工作区里这些路径的当前内容搬进目标 worktree。
// 源里已删除的文件，在目标里同样删掉，这样 add -A 能记上删除。
func copyPaths(srcRoot, dstRoot string, paths []string) error {
	for _, rel := range paths {
		clean := filepath.Clean(rel)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("非法路径 %q", rel)
		}
		src := filepath.Join(srcRoot, clean)
		dst := filepath.Join(dstRoot, clean)

		info, err := os.Stat(src)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("%q 是目录，暂不支持整体提交", rel)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
