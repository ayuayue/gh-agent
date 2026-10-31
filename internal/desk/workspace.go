package desk

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ayuayue/gh-agent/internal/bootstrap"
	"github.com/ayuayue/gh-agent/internal/config"
	"github.com/ayuayue/gh-agent/internal/worktree"
)

// agentWorkspace 是一次审查的隔离工作区：独立 worktree、独立分支，
// 不碰用户当前检出的分支。跑完摘掉 worktree，分支留着（合不合并另说）。
type agentWorkspace struct {
	repoDir string
	dir     string
	branch  string
	base    string
	closed  bool
}

// newWorkspace 在仓库旁边建一个隔离 worktree，基于 dev（没有就用当前分支）。
// 仓库不是 git 仓库时返回错误，调用方可以退回共用目录。
func (s *Service) newWorkspace(cfg config.Config, repo string, number int) (*agentWorkspace, error) {
	repoDir := bootstrap.RepoWorkdir(cfg, repo)
	if repoDir == "" || !worktree.IsRepo(repoDir) {
		return nil, errors.New("本地目录不是 git 仓库")
	}
	base, err := worktree.BaseBranch(repoDir)
	if err != nil {
		return nil, err
	}
	branch := nextBranchName(repoDir, fmt.Sprintf("gh-agent/issue-%d", number))
	root, err := config.Dir()
	if err != nil {
		return nil, err
	}
	slug := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(repo)
	dir := filepath.Join(root, "worktrees", slug, fmt.Sprintf("issue-%d-%d", number, time.Now().UnixNano()))
	if err := worktree.AddWorktree(repoDir, dir, branch, base); err != nil {
		return nil, err
	}
	return &agentWorkspace{repoDir: repoDir, dir: dir, branch: branch, base: base}, nil
}

// close 摘掉 worktree；keepBranch=false 时连空分支一起删掉。
// 可以重复调用（成功路径先 close，失败路径的 defer 就变成空操作）。
func (w *agentWorkspace) close(keepBranch bool) {
	if w == nil || w.closed {
		return
	}
	w.closed = true
	worktree.RemoveWorktree(w.repoDir, w.dir)
	if !keepBranch {
		_ = worktree.DeleteBranch(w.repoDir, w.branch)
	}
}

// commit 提交工作区里的改动，返回 sha；没有改动返回空串。
func (w *agentWorkspace) commit(message string) (string, error) {
	return worktree.CommitAll(w.dir, message)
}

// mergeIntoBase 把分支合回基准分支。只在主仓库正检出于基准分支、
// 且工作区干净时才动手；否则保持分支不动，让用户自己合。
func (w *agentWorkspace) mergeIntoBase(message string) error {
	if err := worktree.CanMerge(w.repoDir, w.base); err != nil {
		return err
	}
	return worktree.MergeBranch(w.repoDir, w.branch, message)
}
