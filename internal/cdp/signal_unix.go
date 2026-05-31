//go:build !windows

package cdp

import "syscall"

func startInspector(pid int) error {
	return syscall.Kill(pid, syscall.SIGUSR1)
}
