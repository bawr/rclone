//go:build !windows

package rcd

import (
	"fmt"
	"syscall"
)

func subprocessParentPID() (int, error) {
	pid := syscall.Getppid()
	if pid <= 0 {
		return 0, fmt.Errorf("couldn't determine parent pid")
	}
	return pid, nil
}

func subprocessParentAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if syscall.Getppid() != pid {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
