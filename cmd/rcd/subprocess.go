package rcd

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/rc/jobs"
)

const subprocessParentCheckInterval = 5 * time.Second

type shutdowner interface {
	Shutdown() error
}

type subprocessController struct {
	originalParentPID int
	checkInterval     time.Duration
	parentAlive       func(pid int) bool
	waitForJobs       func(ctx context.Context) error

	parentGone atomic.Bool
	stopOnce   sync.Once
	stop       chan struct{}
}

func newSubprocessController() (*subprocessController, error) {
	parentPID, err := subprocessParentPID()
	if err != nil {
		return nil, err
	}
	return &subprocessController{
		originalParentPID: parentPID,
		checkInterval:     subprocessParentCheckInterval,
		parentAlive:       subprocessParentAlive,
		waitForJobs:       jobs.WaitForJobs,
		stop:              make(chan struct{}),
	}, nil
}

func (c *subprocessController) Start(s shutdowner) {
	go c.monitor(s)
}

func (c *subprocessController) Stop() {
	c.stopOnce.Do(func() {
		close(c.stop)
	})
}

func (c *subprocessController) WaitForJobs(ctx context.Context) error {
	if !c.parentGone.Load() {
		return nil
	}
	fs.Infof(nil, "Original parent exited, waiting for queued jobs to finish")
	err := c.waitForJobs(ctx)
	if err == nil {
		fs.Infof(nil, "Queued jobs finished, exiting")
	}
	return err
}

func (c *subprocessController) monitor(s shutdowner) {
	ticker := time.NewTicker(c.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if c.parentAlive(c.originalParentPID) {
				continue
			}
			if c.parentGone.CompareAndSwap(false, true) {
				fs.Logf(nil, "Original parent process %d exited, shutting down after queued jobs finish", c.originalParentPID)
				if err := s.Shutdown(); err != nil {
					fs.Errorf(nil, "Failed to shut down rc server after parent exit: %v", err)
				}
			}
			return
		case <-c.stop:
			return
		}
	}
}
