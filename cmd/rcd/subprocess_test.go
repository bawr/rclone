package rcd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testShutdowner struct {
	shutdown chan struct{}
}

func (s *testShutdowner) Shutdown() error {
	close(s.shutdown)
	return nil
}

func TestSubprocessControllerShutdownsOnParentExit(t *testing.T) {
	shutdowner := &testShutdowner{shutdown: make(chan struct{})}
	checks := 0
	controller := &subprocessController{
		originalParentPID: 42,
		checkInterval:     time.Millisecond,
		parentAlive: func(pid int) bool {
			assert.Equal(t, 42, pid)
			checks++
			return checks < 2
		},
		waitForJobs: func(ctx context.Context) error { return nil },
		stop:        make(chan struct{}),
	}

	controller.Start(shutdowner)
	t.Cleanup(controller.Stop)

	select {
	case <-shutdowner.shutdown:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subprocess shutdown")
	}

	assert.True(t, controller.parentGone.Load())
}

func TestSubprocessControllerWaitForJobsOnlyAfterParentExit(t *testing.T) {
	waitCalls := 0
	controller := &subprocessController{
		waitForJobs: func(ctx context.Context) error {
			waitCalls++
			return nil
		},
		stop: make(chan struct{}),
	}

	require.NoError(t, controller.WaitForJobs(context.Background()))
	assert.Equal(t, 0, waitCalls)

	controller.parentGone.Store(true)
	require.NoError(t, controller.WaitForJobs(context.Background()))
	assert.Equal(t, 1, waitCalls)
}
