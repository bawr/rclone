//go:build !openbsd && !plan9

package local

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/xattr"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

const (
	xattrPrefix         = "user." // FIXME is this correct for all unixes?
	xattrInternalPrefix = "rclone."
	xattrHashPrefix     = xattrInternalPrefix + "hash."
	xattrSupported      = xattr.XATTR_SUPPORTED
)

type hashXattr struct {
	Hash  string `json:"hash"`
	Mtime string `json:"mtime"`
	Size  int64  `json:"size"`
}

// Check to see if the error supplied is a not supported error, and if
// so, disable xattrs
func (f *Fs) xattrIsNotSupported(err error) bool {
	xattrErr, ok := err.(*xattr.Error)
	if !ok {
		return false
	}
	// Xattrs not supported can be ENOTSUP or ENOATTR or EINVAL (on Solaris)
	if xattrErr.Err == syscall.EINVAL || xattrErr.Err == syscall.ENOTSUP || xattrErr.Err == xattr.ENOATTR {
		// Show xattrs not supported
		if f.xattrSupported.CompareAndSwap(1, 0) {
			fs.Errorf(f, "xattrs not supported - disabling: %v", err)
		}
		return true
	}
	return false
}

func isReservedXattrKey(k string) bool {
	if _, found := systemMetadataInfo[k]; found {
		return true
	}
	return strings.HasPrefix(k, xattrInternalPrefix)
}

func isXattrMissing(err error) bool {
	xattrErr, ok := err.(*xattr.Error)
	return ok && xattrErr.Err == xattr.ENOATTR
}

func hashXattrName(t hash.Type) string {
	return xattrPrefix + xattrHashPrefix + t.String()
}

// getXattr returns the extended attributes for an object
//
// It doesn't return any attributes owned by this backend in
// metadataKeys
func (o *Object) getXattr() (metadata fs.Metadata, err error) {
	if !xattrSupported || o.fs.xattrSupported.Load() == 0 {
		return nil, nil
	}
	var list []string
	if o.fs.opt.FollowSymlinks {
		list, err = xattr.List(o.path)
	} else {
		list, err = xattr.LList(o.path)
	}
	if err != nil {
		if o.fs.xattrIsNotSupported(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read xattr: %w", err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	metadata = make(fs.Metadata, len(list))
	for _, k := range list {
		var v []byte
		if o.fs.opt.FollowSymlinks {
			v, err = xattr.Get(o.path, k)
		} else {
			v, err = xattr.LGet(o.path, k)
		}
		if err != nil {
			if o.fs.xattrIsNotSupported(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("failed to read xattr key %q: %w", k, err)
		}
		k = strings.ToLower(k)
		if !strings.HasPrefix(k, xattrPrefix) {
			continue
		}
		k = k[len(xattrPrefix):]
		if isReservedXattrKey(k) {
			continue
		}
		metadata[k] = string(v)
	}
	if len(metadata) == 0 {
		return nil, nil
	}
	return metadata, nil
}

// setXattr sets the metadata on the file Xattrs
//
// It doesn't set any attributes owned by this backend in metadataKeys
func (o *Object) setXattr(metadata fs.Metadata) (err error) {
	if !o.translatedLink {
		fd, ok, dupErr := o.dupTransferFD()
		if dupErr != nil {
			return dupErr
		}
		if ok {
			defer fs.CheckClose(fd, &err)
			return o.setXattrWithFD(fd, metadata)
		}
	}
	if !xattrSupported || o.fs.xattrSupported.Load() == 0 {
		return nil
	}
	for k, value := range metadata {
		k = strings.ToLower(k)
		if isReservedXattrKey(k) {
			continue
		}
		k = xattrPrefix + k
		v := []byte(value)
		if o.fs.opt.FollowSymlinks {
			err = xattr.Set(o.path, k, v)
		} else {
			err = xattr.LSet(o.path, k, v)
		}
		if err != nil {
			if o.fs.xattrIsNotSupported(err) {
				return nil
			}
			return fmt.Errorf("failed to set xattr key %q: %w", k, err)
		}
	}
	return nil
}

func (o *Object) setXattrWithFD(fd *os.File, metadata fs.Metadata) (err error) {
	if !xattrSupported || o.fs.xattrSupported.Load() == 0 {
		return nil
	}
	for k, value := range metadata {
		k = strings.ToLower(k)
		if isReservedXattrKey(k) {
			continue
		}
		k = xattrPrefix + k
		err = xattr.FSet(fd, k, []byte(value))
		if err != nil {
			if o.fs.xattrIsNotSupported(err) {
				return nil
			}
			return fmt.Errorf("failed to set xattr key %q: %w", k, err)
		}
	}
	return nil
}

func (o *Object) getCachedHash(t hash.Type) (string, bool, error) {
	if t == hash.None || o.translatedLink || !o.fs.opt.XattrHashes || !xattrSupported || o.fs.xattrSupported.Load() == 0 {
		return "", false, nil
	}
	name := hashXattrName(t)
	var (
		buf []byte
		err error
	)
	fd, ok, dupErr := o.dupTransferFD()
	if dupErr != nil {
		return "", false, dupErr
	}
	if ok {
		defer fs.CheckClose(fd, &err)
		buf, err = xattr.FGet(fd, name)
	} else if o.fs.opt.FollowSymlinks {
		buf, err = xattr.Get(o.path, name)
	} else {
		buf, err = xattr.LGet(o.path, name)
	}
	if err != nil {
		if isXattrMissing(err) || os.IsNotExist(err) {
			return "", false, nil
		}
		if o.fs.xattrIsNotSupported(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read cached %s hash xattr: %w", t, err)
	}
	var cached hashXattr
	err = json.Unmarshal(buf, &cached)
	if err != nil {
		return "", false, fmt.Errorf("failed to parse cached %s hash xattr: %w", t, err)
	}
	if cached.Hash == "" || len(cached.Hash) != hash.Width(t, false) {
		return "", false, nil
	}
	cachedMtime, err := time.Parse(metadataTimeFormat, cached.Mtime)
	if err != nil {
		return "", false, fmt.Errorf("failed to parse cached %s hash mtime %q: %w", t, cached.Mtime, err)
	}
	o.fs.objectMetaMu.RLock()
	currentMtime := o.modTime
	currentSize := o.size
	o.fs.objectMetaMu.RUnlock()
	if currentSize != cached.Size || !currentMtime.Equal(cachedMtime) {
		return "", false, nil
	}
	return strings.ToLower(cached.Hash), true, nil
}

func (o *Object) setCachedHashes(hashes map[hash.Type]string) (err error) {
	if len(hashes) == 0 || o.translatedLink || !o.fs.opt.XattrHashes || !xattrSupported || o.fs.xattrSupported.Load() == 0 {
		return nil
	}
	o.fs.objectMetaMu.RLock()
	cachedMtime := o.modTime
	cachedSize := o.size
	o.fs.objectMetaMu.RUnlock()
	fd, ok, dupErr := o.dupTransferFD()
	if dupErr != nil {
		return dupErr
	}
	if ok {
		defer fs.CheckClose(fd, &err)
	}
	for t, hashValue := range hashes {
		if t == hash.None || hashValue == "" {
			continue
		}
		buf, marshalErr := json.Marshal(hashXattr{
			Hash:  strings.ToLower(hashValue),
			Mtime: cachedMtime.Format(metadataTimeFormat),
			Size:  cachedSize,
		})
		if marshalErr != nil {
			return marshalErr
		}
		name := hashXattrName(t)
		if ok {
			err = xattr.FSet(fd, name, buf)
		} else if o.fs.opt.FollowSymlinks {
			err = xattr.Set(o.path, name, buf)
		} else {
			err = xattr.LSet(o.path, name, buf)
		}
		if err != nil {
			if o.fs.xattrIsNotSupported(err) {
				return nil
			}
			return fmt.Errorf("failed to set cached %s hash xattr: %w", t, err)
		}
	}
	return nil
}

func (o *Object) clearCachedHashes() (err error) {
	if o.translatedLink || !o.fs.opt.XattrHashes || !xattrSupported || o.fs.xattrSupported.Load() == 0 {
		return nil
	}
	var keys []string
	fd, ok, dupErr := o.dupTransferFD()
	if dupErr != nil {
		return dupErr
	}
	if ok {
		defer fs.CheckClose(fd, &err)
		keys, err = xattr.FList(fd)
	} else if o.fs.opt.FollowSymlinks {
		keys, err = xattr.List(o.path)
	} else {
		keys, err = xattr.LList(o.path)
	}
	if err != nil {
		if o.fs.xattrIsNotSupported(err) || isXattrMissing(err) || os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to list cached hash xattrs: %w", err)
	}
	for _, key := range keys {
		if !strings.HasPrefix(strings.ToLower(key), xattrPrefix+xattrHashPrefix) {
			continue
		}
		if ok {
			err = xattr.FRemove(fd, key)
		} else if o.fs.opt.FollowSymlinks {
			err = xattr.Remove(o.path, key)
		} else {
			err = xattr.LRemove(o.path, key)
		}
		if err != nil && !isXattrMissing(err) {
			if o.fs.xattrIsNotSupported(err) {
				return nil
			}
			return fmt.Errorf("failed to remove cached hash xattr %q: %w", key, err)
		}
	}
	return nil
}
