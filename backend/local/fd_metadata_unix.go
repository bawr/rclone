//go:build !windows && !plan9 && !js

package local

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

func setFileMetadataTimes(fd *os.File, atime, mtime, btime time.Time) error {
	if atime.IsZero() && mtime.IsZero() {
		return nil
	}
	fdPath := fmt.Sprintf("/dev/fd/%d", fd.Fd())
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		fdPath = fmt.Sprintf("/proc/self/fd/%d", fd.Fd())
	}
	return os.Chtimes(fdPath, atime, mtime)
}
