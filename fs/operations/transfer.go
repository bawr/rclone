package operations

import (
	"context"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

func unwrapTransferObject(obj fs.Object) fs.Object {
	for {
		unwrapper, ok := obj.(fs.ObjectUnWrapper)
		if !ok {
			return obj
		}
		next := unwrapper.UnWrap()
		if next == nil {
			return obj
		}
		obj = next
	}
}

func openTransfer(ctx context.Context, obj fs.Object) (fs.TransferReader, bool, error) {
	obj = unwrapTransferObject(obj)
	opener, ok := obj.(fs.TransferOpener)
	if !ok {
		return nil, false, nil
	}
	transfer, err := opener.OpenTransfer(ctx)
	if err != nil {
		if err == fs.ErrorNotImplemented {
			return nil, false, nil
		}
		return nil, true, err
	}
	return transfer, true, nil
}

func transferHash(ctx context.Context, obj fs.Object, ht hash.Type) (sum string, ok bool, err error) {
	obj = unwrapTransferObject(obj)
	hasher, ok := obj.(fs.TransferHasher)
	if !ok {
		return "", false, nil
	}
	return hasher.HashFromTransfer(ctx, ht)
}

func closeTransferObject(obj fs.Object) error {
	obj = unwrapTransferObject(obj)
	closer, ok := obj.(fs.TransferCloser)
	if !ok {
		return nil
	}
	return closer.CloseTransfer()
}
