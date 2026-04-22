//go:build windows

package local

import (
	"os"
	"syscall"
	"time"
)

func setFileMetadataTimes(fd *os.File, atime, mtime, btime time.Time) error {
	handle := syscall.Handle(fd.Fd())
	var patime, pmtime, pbtime *syscall.Filetime
	if !atime.IsZero() {
		t := syscall.NsecToFiletime(atime.UnixNano())
		patime = &t
	}
	if !mtime.IsZero() {
		t := syscall.NsecToFiletime(mtime.UnixNano())
		pmtime = &t
	}
	if !btime.IsZero() {
		t := syscall.NsecToFiletime(btime.UnixNano())
		pbtime = &t
	}
	return syscall.SetFileTime(handle, pbtime, patime, pmtime)
}
