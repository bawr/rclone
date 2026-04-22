package operations

import (
	"context"
	"fmt"
	"sync"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

type transferContextKey struct{}

type transferEntry struct {
	obj      fs.Object
	transfer fs.TransferReader
}

type transferMap struct {
	mu        sync.Mutex
	transfers map[string]transferEntry
}

func transferMapFromContext(ctx context.Context) (*transferMap, bool) {
	transfers, ok := ctx.Value(transferContextKey{}).(*transferMap)
	return transfers, ok
}

func transferKey(info fs.Info, remote string) string {
	return fmt.Sprintf("%s\x00%s\x00%s", info.Name(), info.Root(), remote)
}

func stashTransfer(ctx context.Context, info fs.Info, remote string, transfer fs.TransferReader) context.Context {
	transfers, ok := transferMapFromContext(ctx)
	if !ok {
		transfers = &transferMap{transfers: map[string]transferEntry{}}
		ctx = context.WithValue(ctx, transferContextKey{}, transfers)
	}
	transfers.mu.Lock()
	key := transferKey(info, remote)
	entry := transfers.transfers[key]
	entry.transfer = transfer
	transfers.transfers[key] = entry
	transfers.mu.Unlock()
	return ctx
}

func stashTransferObject(ctx context.Context, obj fs.Object, transfer fs.TransferReader) context.Context {
	ctx = stashTransfer(ctx, obj.Fs(), obj.Remote(), transfer)
	transfers, _ := transferMapFromContext(ctx)
	key := transferKey(obj.Fs(), obj.Remote())
	transfers.mu.Lock()
	entry := transfers.transfers[key]
	entry.obj = obj
	transfers.transfers[key] = entry
	transfers.mu.Unlock()
	return ctx
}

func contextSourceObject(ctx context.Context, info fs.Info, remote string) fs.Object {
	transfers, ok := transferMapFromContext(ctx)
	if !ok {
		return nil
	}
	key := transferKey(info, remote)
	transfers.mu.Lock()
	defer transfers.mu.Unlock()
	return transfers.transfers[key].obj
}

func claimTransfer(ctx context.Context, obj fs.Object) fs.TransferReader {
	transfers, ok := transferMapFromContext(ctx)
	if !ok {
		return nil
	}
	key := transferKey(obj.Fs(), obj.Remote())
	transfers.mu.Lock()
	defer transfers.mu.Unlock()
	entry := transfers.transfers[key]
	delete(transfers.transfers, key)
	return entry.transfer
}

func releaseTransfer(ctx context.Context, info fs.Info, remote string) error {
	transfers, ok := transferMapFromContext(ctx)
	if !ok {
		return nil
	}
	key := transferKey(info, remote)
	transfers.mu.Lock()
	entry := transfers.transfers[key]
	delete(transfers.transfers, key)
	transfers.mu.Unlock()
	if entry.transfer == nil {
		return nil
	}
	return entry.transfer.Close()
}

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
