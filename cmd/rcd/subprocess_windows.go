//go:build windows

package rcd

import "errors"

func subprocessParentPID() (int, error) {
	return 0, errors.New("--rc-subprocess is not supported on Windows")
}

func subprocessParentAlive(pid int) bool {
	return false
}
