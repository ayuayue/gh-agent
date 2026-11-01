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

// mergeBranchIntoBase 把 branch 合回 base，并把「能不能自动合」说清楚：
// 合上了返回合到哪个分支；合不了返回人看得懂的阻塞原因（空串表示已合上）。
func mergeBranchIntoBase(repoDir, branch, base, message string) (string, string) {
	if repoDir == "" || branch == "" || base == "" {
		return "", "缺仓库或分支信息，本地没法合并"
	}
	// 先看主仓库能不能动手（不是在 base、或者有未提交改动），
	// 再算冲突：否则白算一遍。
	if err := worktree.CanMerge(repoDir, base); err != nil {
		return "", err.Error()
	}
	conflicts, err := worktree.ProbeMerge(repoDir, base, branch)
	if err != nil {
		return "", "判断不了会不会冲突：" + err.Error()
	}
	if len(conflicts) > 0 {
		return "", "和 " + base + " 冲突：" + strings.Join(conflicts, "、")
	}
	if err := worktree.MergeBranch(repoDir, branch, message); err != nil {
		return "", err.Error()
	}
	return base, ""
}

// autoMerge 试着自己合回基准分支；合不了就把原因交给上层写进草稿等人决定。
func (w *agentWorkspace) autoMerge(message string) (string, string) {
	if w == nil {
		return "", ""
	}
	return mergeBranchIntoBase(w.repoDir, w.branch, w.base, message)
}
