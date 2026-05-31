//go:build !windows

package simctl

import (
	"os/exec"
	"syscall"
)

func setLaunchSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
