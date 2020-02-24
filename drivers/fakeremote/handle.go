package fakeremote

import (
	"context"
	"fmt"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/lib/fifo"
	"github.com/hashicorp/nomad/plugins/drivers"
)

type taskHandle struct {
	uuid   string
	logger hclog.Logger

	// stateLock syncs access to all fields below
	stateLock sync.RWMutex

	taskConfig  *drivers.TaskConfig
	procState   drivers.TaskState
	startedAt   time.Time
	completedAt time.Time
	exitResult  *drivers.ExitResult
	doneCh      chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
}

func newTaskHandle(logger hclog.Logger, ts TaskState) *taskHandle {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named("handle").With("uuid", ts.UUID)

	h := &taskHandle{
		uuid:       ts.UUID,
		taskConfig: ts.TaskConfig,
		procState:  drivers.TaskStateRunning,
		startedAt:  ts.StartedAt,
		exitResult: &drivers.ExitResult{},
		logger:     logger,
		doneCh:     make(chan struct{}),
		ctx:        ctx,
		cancel:     cancel,
	}

	return h
}

func (h *taskHandle) TaskStatus() *drivers.TaskStatus {
	h.stateLock.RLock()
	defer h.stateLock.RUnlock()

	return &drivers.TaskStatus{
		ID:          h.taskConfig.ID,
		Name:        h.taskConfig.Name,
		State:       h.procState,
		StartedAt:   h.startedAt,
		CompletedAt: h.completedAt,
		ExitResult:  h.exitResult,
		DriverAttributes: map[string]string{
			"uuid": h.uuid,
		},
	}
}

func (h *taskHandle) IsRunning() bool {
	h.stateLock.RLock()
	defer h.stateLock.RUnlock()
	return h.procState == drivers.TaskStateRunning
}

func (h *taskHandle) run() {
	defer close(h.doneCh)
	h.stateLock.Lock()
	if h.exitResult == nil {
		h.exitResult = &drivers.ExitResult{}
	}
	h.stateLock.Unlock()

	f, err := fifo.OpenWriter(h.taskConfig.StdoutPath)
	if err != nil {
		h.stateLock.Lock()
		defer h.stateLock.Unlock()
		h.completedAt = time.Now()
		h.exitResult.ExitCode = 1
		h.exitResult.Err = fmt.Errorf("failed to create stdout: %v", err)
		return
	}
	defer f.Close()

	// Block until stopped
	for h.ctx.Err() == nil {
		// Write periodically
		select {
		case <-time.After(5 * time.Second):
			now := time.Now().Format(time.RFC3339)
			if _, err := fmt.Fprintf(f, "[%s] - uuid:%s started_at:%s\n", now, h.uuid, h.startedAt); err != nil {
				h.stateLock.Lock()
				defer h.stateLock.Unlock()
				h.completedAt = time.Now()
				h.exitResult.ExitCode = 1
				h.exitResult.Err = fmt.Errorf("failed to create stdout: %v", err)
			}
		case <-h.ctx.Done():
		}
	}

	h.stateLock.Lock()
	defer h.stateLock.Unlock()

	h.procState = drivers.TaskStateExited
	h.exitResult.ExitCode = 0
	h.exitResult.Signal = 0
	h.completedAt = time.Now()
}

func (h *taskHandle) stop() {
	h.logger.Info("handle.stop()")
	h.cancel()
}
