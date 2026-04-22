package operations

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "github.com/rclone/rclone/backend/local"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fstest/mockfs"
	"github.com/rclone/rclone/fstest/mockobject"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testTransferReader struct {
	openCalls  int
	hashCalls  int
	closeCalls int
}

func (t *testTransferReader) Open(options ...fs.OpenOption) (io.ReadCloser, error) {
	t.openCalls++
	return io.NopCloser(&emptyReader{}), nil
}

func (t *testTransferReader) Hash(ctx context.Context, ty hash.Type) (string, error) {
	t.hashCalls++
	return "sum", nil
}

func (t *testTransferReader) Close() error {
	t.closeCalls++
	return nil
}

type emptyReader struct{}

func (r *emptyReader) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

type transferTestObject struct {
	fs.Object
	transfer        fs.TransferReader
	hash            string
	openTransferErr error
	closeCalls      int
}

func (o *transferTestObject) OpenTransfer(ctx context.Context) (fs.TransferReader, error) {
	return o.transfer, o.openTransferErr
}

func (o *transferTestObject) HashFromTransfer(ctx context.Context, ty hash.Type) (string, bool, error) {
	return o.hash, true, nil
}

func (o *transferTestObject) CloseTransfer() error {
	o.closeCalls++
	return nil
}

type wrappedTransferObject struct {
	fs.Object
	inner fs.Object
}

func (o wrappedTransferObject) UnWrap() fs.Object {
	return o.inner
}

type countingTransferObject struct {
	fs.Object
	mu                 sync.Mutex
	openTransferCalls  int
	transferOpenCalls  int
	transferHashCalls  int
	transferCloseCalls int
}

func (o *countingTransferObject) OpenTransfer(ctx context.Context) (fs.TransferReader, error) {
	o.mu.Lock()
	o.openTransferCalls++
	o.mu.Unlock()
	opener, ok := o.Object.(fs.TransferOpener)
	if !ok {
		return nil, fs.ErrorNotImplemented
	}
	transfer, err := opener.OpenTransfer(ctx)
	if err != nil {
		return nil, err
	}
	return &countingTransferReader{TransferReader: transfer, parent: o}, nil
}

type countingTransferReader struct {
	fs.TransferReader
	parent *countingTransferObject
}

func (t *countingTransferReader) Open(options ...fs.OpenOption) (io.ReadCloser, error) {
	t.parent.mu.Lock()
	t.parent.transferOpenCalls++
	t.parent.mu.Unlock()
	return t.TransferReader.Open(options...)
}

func (t *countingTransferReader) Hash(ctx context.Context, ty hash.Type) (string, error) {
	t.parent.mu.Lock()
	t.parent.transferHashCalls++
	t.parent.mu.Unlock()
	return t.TransferReader.Hash(ctx, ty)
}

func (t *countingTransferReader) Close() error {
	t.parent.mu.Lock()
	t.parent.transferCloseCalls++
	t.parent.mu.Unlock()
	return t.TransferReader.Close()
}

func newMockObject(t *testing.T, ctx context.Context, remote string) fs.Object {
	t.Helper()
	f, err := mockfs.NewFs(ctx, "mock", "", nil)
	require.NoError(t, err)
	obj := mockobject.New(remote).WithContent([]byte("hello world"), mockobject.SeekModeRegular)
	obj.SetFs(f)
	return obj
}

func newLocalObject(t *testing.T, ctx context.Context, root, remote, contents string) (fs.Fs, fs.Object) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, remote)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, remote), []byte(contents), 0o666))
	f, err := fs.NewFs(ctx, root)
	require.NoError(t, err)
	obj, err := f.NewObject(ctx, remote)
	require.NoError(t, err)
	return f, obj
}

func TestTransferHelpers(t *testing.T) {
	ctx := context.Background()

	t.Run("stash-claim-release", func(t *testing.T) {
		obj := newMockObject(t, ctx, "file.txt")
		transfer := &testTransferReader{}

		ctx := stashTransferObject(ctx, obj, transfer)
		assert.Same(t, obj, contextSourceObject(ctx, obj.Fs(), obj.Remote()))
		assert.Same(t, transfer, claimTransfer(ctx, obj))
		assert.Nil(t, claimTransfer(ctx, obj))
		require.NoError(t, releaseTransfer(ctx, obj.Fs(), obj.Remote()))
		assert.Equal(t, 0, transfer.closeCalls)
	})

	t.Run("release-closes-unclaimed", func(t *testing.T) {
		obj := newMockObject(t, ctx, "file.txt")
		transfer := &testTransferReader{}

		ctx := stashTransferObject(ctx, obj, transfer)
		require.NoError(t, releaseTransfer(ctx, obj.Fs(), obj.Remote()))
		assert.Equal(t, 1, transfer.closeCalls)
		require.NoError(t, releaseTransfer(ctx, obj.Fs(), obj.Remote()))
		assert.Equal(t, 1, transfer.closeCalls)
	})

	t.Run("unwraps-transfer-interfaces", func(t *testing.T) {
		transfer := &testTransferReader{}
		inner := &transferTestObject{
			Object:   newMockObject(t, ctx, "wrapped.txt"),
			transfer: transfer,
			hash:     "wrapped-sum",
		}
		obj := wrappedTransferObject{Object: inner, inner: inner}

		gotTransfer, ok, err := openTransfer(ctx, obj)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Same(t, transfer, gotTransfer)

		sum, ok, err := transferHash(ctx, obj, hash.MD5)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "wrapped-sum", sum)

		require.NoError(t, closeTransferObject(obj))
		assert.Equal(t, 1, inner.closeCalls)
	})
}

func TestCopyResetSrcTransfer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_, src := newLocalObject(t, ctx, filepath.Join(root, "src"), "file.txt", "hello world")
	wrapped := &countingTransferObject{Object: src}
	c := &copy{src: wrapped}

	require.NoError(t, c.ensureSrcTransfer(ctx))
	assert.Equal(t, 1, wrapped.openTransferCalls)
	assert.NotNil(t, c.srcTransfer)

	require.NoError(t, c.resetSrcTransfer())
	assert.Nil(t, c.srcTransfer)
	assert.Equal(t, 1, wrapped.transferCloseCalls)

	require.NoError(t, c.ensureSrcTransfer(ctx))
	assert.Equal(t, 2, wrapped.openTransferCalls)
}

func TestCopyUsesRetainedTransferForVerify(t *testing.T) {
	ctx, ci := fs.AddConfig(context.Background())
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")

	_, src := newLocalObject(t, ctx, srcRoot, "file.txt", "hello world")
	require.NoError(t, os.MkdirAll(dstRoot, 0o755))
	dstFs, err := fs.NewFs(ctx, dstRoot)
	require.NoError(t, err)
	dstFs.Features().Copy = nil

	oldStreams := ci.MultiThreadStreams
	oldCutoff := ci.MultiThreadCutoff
	oldSet := ci.MultiThreadSet
	t.Cleanup(func() {
		ci.MultiThreadStreams = oldStreams
		ci.MultiThreadCutoff = oldCutoff
		ci.MultiThreadSet = oldSet
	})
	ci.MultiThreadStreams = 0
	ci.MultiThreadCutoff = 0
	ci.MultiThreadSet = false

	wrapped := &countingTransferObject{Object: src}
	newDst, err := Copy(ctx, dstFs, nil, "copied.txt", wrapped)
	require.NoError(t, err)
	require.NotNil(t, newDst)

	assert.Equal(t, 1, wrapped.openTransferCalls)
	assert.Greater(t, wrapped.transferOpenCalls, 0)
	assert.Greater(t, wrapped.transferHashCalls, 0)
	assert.Equal(t, 1, wrapped.transferCloseCalls)

	require.NoError(t, newDst.Remove(ctx))
}

func TestCopyMultiThreadUsesSingleTransferHandle(t *testing.T) {
	ctx, ci := fs.AddConfig(context.Background())
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")

	_, src := newLocalObject(t, ctx, srcRoot, "file.txt", strings.Repeat("x", 1024))
	require.NoError(t, os.MkdirAll(dstRoot, 0o755))
	dstFs, err := fs.NewFs(ctx, dstRoot)
	require.NoError(t, err)
	dstFs.Features().Copy = nil

	oldStreams := ci.MultiThreadStreams
	oldCutoff := ci.MultiThreadCutoff
	oldSet := ci.MultiThreadSet
	t.Cleanup(func() {
		ci.MultiThreadStreams = oldStreams
		ci.MultiThreadCutoff = oldCutoff
		ci.MultiThreadSet = oldSet
	})
	ci.MultiThreadStreams = 2
	ci.MultiThreadCutoff = 1
	ci.MultiThreadSet = true

	wrapped := &countingTransferObject{Object: src}
	if !doMultiThreadCopy(ctx, dstFs, wrapped) {
		t.Skip("multi-thread copy not supported for this destination")
	}

	newDst, err := Copy(ctx, dstFs, nil, "copied.txt", wrapped)
	require.NoError(t, err)
	require.NotNil(t, newDst)

	assert.Equal(t, 1, wrapped.openTransferCalls)
	assert.Greater(t, wrapped.transferOpenCalls, 0)
	assert.Greater(t, wrapped.transferHashCalls, 0)
	assert.Equal(t, 1, wrapped.transferCloseCalls)

	require.NoError(t, newDst.Remove(ctx))
}
