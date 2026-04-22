//go:build !windows

package file

import (
	"os"

	"golang.org/x/sys/unix"
)

// OpenFile is the generalized open call; most users will use Open or Create
// instead. It opens the named file with specified flag (O_RDONLY etc.) and
// perm (before umask), if applicable. If successful, methods on the returned
// File can be used for I/O. If there is an error, it will be of type
// *PathError.
//
// Under both Unix and Windows this will allow open files to be
// renamed and or deleted.
var OpenFile = os.OpenFile

func dup(f *os.File) (*os.File, error) {
	fd, err := unix.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

// IsReserved checks if path contains a reserved name
func IsReserved(path string) error {
	return nil
}
