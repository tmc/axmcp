//go:build windows

package simctl

import "os/exec"

func setLaunchSysProcAttr(*exec.Cmd) {}
