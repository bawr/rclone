//go:build plan9 || js

package local

import (
	"os"
	"time"
)

func setFileMetadataTimes(fd *os.File, atime, mtime, btime time.Time) error {
	return nil
}
