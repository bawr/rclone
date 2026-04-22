//go:build plan9

package file

import "os"

// OpenFile is the generalized open call; most users will use Open or Create instead.
var OpenFile = os.OpenFile

func dup(f *os.File) (*os.File, error) {
	return nil, ErrDupNotSupported
}

// IsReserved checks if path contains a reserved name.
func IsReserved(path string) error {
	return nil
}
