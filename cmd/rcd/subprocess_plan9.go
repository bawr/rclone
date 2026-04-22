//go:build plan9

package rcd

import "errors"

func subprocessParentPID() (int, error) {
	return 0, errors.New("--rc-subprocess is not supported on Plan 9")
}

func subprocessParentAlive(pid int) bool {
	return false
}
