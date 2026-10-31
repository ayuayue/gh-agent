//go:build !windows

package winproc

import "os/exec"

// Hide 在其他平台上没有作用。
func Hide(cmd *exec.Cmd) {}
