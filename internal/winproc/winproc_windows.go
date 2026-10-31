//go:build windows

package winproc

import (
	"os/exec"
	"syscall"
)

// Hide 让子进程不弹控制台窗口。GUI 应用里每跑一个 cmd/npx/git 都会闪一下黑框，
// 加上 CREATE_NO_WINDOW 就不闪了。
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
