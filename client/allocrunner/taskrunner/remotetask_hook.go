package taskrunner

import (
	"context"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/kr/pretty"
)

// remoteTaskHook reattaches to remotely executing tasks.
//
//FIXME(schmichael) super leaky abstraction with taskrunner
type remoteTaskHook struct {
	tr *TaskRunner

	logger hclog.Logger
}

func newRemoteTaskHook(tr *TaskRunner, logger hclog.Logger) *remoteTaskHook {
	h := &remoteTaskHook{
		tr: tr,
	}
	h.logger = logger.Named(h.Name())
	return h
}

func (h *remoteTaskHook) Name() string {
	return "remote_task"
}

func (h *remoteTaskHook) Prestart(ctx context.Context, req *interfaces.TaskPrestartRequest, resp *interfaces.TaskPrestartResponse) error {
	if h.tr.getDriverHandle() != nil {
		//FIXME remove log line
		h.logger.Info("----> loadTaskHandle skipping: driver handle already exists")
		resp.Done = true
		return nil
	}

	h.tr.stateLock.Lock()
	th := drivers.NewTaskHandleFromState(h.tr.state)
	h.tr.stateLock.Unlock()

	if th == nil {
		//FIXME remove
		h.logger.Info("----> loadTaskHandle did NOT find a task handle", "state", pretty.Sprint(h.tr.state))

		resp.Done = true
		return nil
	}

	// The task config is unique per invocation so recreate it here
	th.Config = h.tr.buildTaskConfig()

	if err := h.tr.driver.RecoverTask(th); err != nil {
		//FIXME(schmichael) soft error here to let a new instance get
		//started?
		h.logger.Error("error recovering task state", "error", err)
		return nil
	}

	taskInfo, err := h.tr.driver.InspectTask(th.Config.ID)
	if err != nil {
		//FIXME(schmichael) soft error here to let a new instance get
		//started?
		h.logger.Error("error inspecting recovered task state", "error", err)
		return nil
	}

	//FIXME remove
	h.logger.Info("----> loadTaskHandle DID find a task handle", "id", th.Config.ID)

	h.tr.setDriverHandle(NewDriverHandle(h.tr.driver, th.Config.ID, h.tr.Task(), taskInfo.NetworkOverride))

	h.tr.stateLock.Lock()
	h.tr.localState.TaskHandle = th
	h.tr.localState.DriverNetwork = taskInfo.NetworkOverride
	h.tr.stateLock.Unlock()

	h.tr.UpdateState(structs.TaskStateRunning, structs.NewTaskEvent(structs.TaskStarted))

	h.tr.logger.Info("----> loadTaskHandle done")

	resp.Done = true
	return nil
}
