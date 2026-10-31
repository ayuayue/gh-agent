// Package worktree 用 git 工作区的快照，算出"这一轮 agent 到底改了什么"。
//
// 只用 git 命令，不碰索引、不 stash、不提交：跑完只看差异，改不改由人决定。
package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"sort"
	"strings"

	"github.com/ayuayue/gh-agent/internal/winproc"
)

// Snapshot 是某个时刻工作区的状态。
type Snapshot struct {
	// Repo 表示目录确实在一个 git 仓库里。
	Repo bool
	// Files 是 porcelain 给出的"路径 -> 状态码"。
	Files map[string]string
	// DiffHash 是 git diff HEAD 的哈希，用来判断"到底变没变"。
	DiffHash string
}

// IsRepo 判断目录是不是 git 仓库的工作区（不含 .git 的普通目录也不算）。
func IsRepo(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	_, err := git(dir, "rev-parse", "--show-toplevel")
	return err == nil
}

// Capture 取一次快照。目录不是 git 仓库时返回零值。
func Capture(dir string) Snapshot {
	if strings.TrimSpace(dir) == "" {
		return Snapshot{}
	}
	if _, err := git(dir, "rev-parse", "--show-toplevel"); err != nil {
		return Snapshot{}
	}
	status, err := git(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Snapshot{}
	}
	diff, _ := git(dir, "diff", "HEAD")
	sum := sha256.Sum256([]byte(diff))
	files := parseStatus(status)
	// 状态码不足以判断"改没改"：一个本来就 M 的文件再被改，码还是 M。
	// 所以给每个可疑文件记一份内容哈希。
	for path := range files {
		files[path] = files[path] + "|" + fileHash(dir, path)
	}
	return Snapshot{
		Repo:     true,
		Files:    files,
		DiffHash: hex.EncodeToString(sum[:8]),
	}
}

// fileHash 用 git 自己算工作区文件的内容哈希，不做任何写入。
func fileHash(dir, path string) string {
	out, err := git(dir, "hash-object", "--", path)
	if err != nil {
		return "-"
	}
	return strings.TrimSpace(out)
}

// Changed 返回两次快照之间变动过的路径。
func Changed(before, after Snapshot) []string {
	if !before.Repo || !after.Repo {
		return nil
	}
	seen := make(map[string]bool)
	for path, code := range after.Files {
		if before.Files[path] != code {
			seen[path] = true
		}
	}
	for path := range before.Files {
		if _, ok := after.Files[path]; !ok {
			seen[path] = true // 被删掉或恢复原状
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// Result 是一次比对的结果，直接塞进草稿给界面。
type Result struct {
	// Files 是改动过的路径。
	Files []string `json:"files"`
	// Untracked 是其中新建、还没被 git 跟踪的文件（diff 里没有正文）。
	Untracked []string `json:"untracked"`
	// Diff 是 unified diff，已按上限截断。
	Diff string `json:"diff"`
	// DirtyBefore 表示跑之前工作区就有改动，diff 里可能混着人自己的修改。
	DirtyBefore bool `json:"dirtyBefore"`
}

// Collect 算出改动并取回 diff。paths 为空时返回 nil。
func Collect(dir string, before, after Snapshot, paths []string, limit int) *Result {
	if len(paths) == 0 {
		return nil
	}
	res := &Result{
		Files:       paths,
		DirtyBefore: len(before.Files) > 0,
	}
	tracked := make([]string, 0, len(paths))
	for _, path := range paths {
		if code, ok := after.Files[path]; ok && strings.HasPrefix(code, "??") {
			res.Untracked = append(res.Untracked, path)
			continue
		}
		tracked = append(tracked, path)
	}
	if len(tracked) > 0 {
		args := append([]string{"diff", "HEAD", "--"}, tracked...)
		if out, err := git(dir, args...); err == nil {
			res.Diff = clip(out, limit)
		}
	}
	return res
}

func parseStatus(raw string) map[string]string {
	out := make(map[string]string)
	fields := strings.Split(raw, "\x00")
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			continue
		}
		code := field[:2]
		path := strings.TrimSpace(field[3:])
		if path == "" {
			continue
		}
		out[path] = code
		// 重命名/复制在 -z 下后面还跟着原路径
		if strings.ContainsAny(code, "RC") && i+1 < len(fields) {
			i++
		}
	}
	return out
}

func clip(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "\n…（diff 过长已截断）"
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	winproc.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
