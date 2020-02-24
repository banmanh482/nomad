package fakeremote

import (
	"context"
	"fmt"
	"strings"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/helper/pluginutils/loader"
	"github.com/hashicorp/nomad/helper/uuid"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
)

const (
	pluginName        = "fakeremote"
	fingerprintPeriod = 30 * time.Second
	taskHandleVersion = 1
)

var (
	PluginID = loader.PluginID{
		Name:       pluginName,
		PluginType: base.PluginTypeDriver,
	}

	PluginConfig = &loader.InternalPluginConfig{
		Config:  map[string]interface{}{},
		Factory: func(l hclog.Logger) interface{} { return NewFakeRemoteDriver(l) },
	}
)

func PluginLoader(opts map[string]string) (map[string]interface{}, error) {
	conf := map[string]interface{}{}
	return conf, nil
}

var (
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     "0.1.0",
		Name:              pluginName,
	}

	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"enabled": hclspec.NewDefault(
			hclspec.NewAttr("enabled", "bool", false),
			hclspec.NewLiteral("true"),
		),
	})

	taskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"command": hclspec.NewAttr("command", "string", true),
		"args":    hclspec.NewAttr("args", "list(string)", false),
	})

	// capabilities is returned by the Capabilities RPC and indicates what
	// optional features this driver supports
	capabilities = &drivers.Capabilities{
		SendSignals: true,
		Exec:        true,
		FSIsolation: drivers.FSIsolationNone,
		NetIsolationModes: []drivers.NetIsolationMode{
			drivers.NetIsolationModeHost,
		},
		RemoteTasks: true,
	}
)

type Driver struct {
	// eventer is used to handle multiplexing of TaskEvents calls such that an
	// event can be broadcast to all callers
	eventer *eventer.Eventer

	// config is the driver configuration set by the SetConfig RPC
	config *Config

	// nomadConfig is the client config from nomad
	nomadConfig *base.ClientDriverConfig

	// tasks is the in memory datastore mapping taskIDs to driverHandles
	tasks *taskStore

	// ctx is the context for the driver. It is passed to other subsystems to
	// coordinate shutdown
	ctx context.Context

	// signalShutdown is called when the driver is shutting down and cancels the
	// ctx passed to any subsystems
	signalShutdown context.CancelFunc

	// logger will log to the Nomad agent
	logger hclog.Logger
}

// Config is the driver configuration set by the SetConfig RPC call
type Config struct {
	// Enabled is set to true to enable the raw_exec driver
	Enabled bool `codec:"enabled"`
}

// TaskConfig is the driver configuration of a task within a job
type TaskConfig struct {
	Command string   `codec:"command"`
	Args    []string `codec:"args"`
}

// TaskState is the state which is encoded in the handle returned in
// StartTask. This information is needed to rebuild the task state and handler
// during recovery.
type TaskState struct {
	ReattachConfig *pstructs.ReattachConfig
	TaskConfig     *drivers.TaskConfig
	UUID           string
	StartedAt      time.Time
}

// NewFakeRemoteDriver returns a new DriverPlugin implementation
func NewFakeRemoteDriver(logger hclog.Logger) drivers.DriverPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)
	return &Driver{
		eventer:        eventer.NewEventer(ctx, logger),
		config:         &Config{},
		tasks:          newTaskStore(),
		ctx:            ctx,
		signalShutdown: cancel,
		logger:         logger,
	}
}

func (d *Driver) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

func (d *Driver) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

func (d *Driver) SetConfig(cfg *base.Config) error {
	var config Config
	if len(cfg.PluginConfig) != 0 {
		if err := base.MsgPackDecode(cfg.PluginConfig, &config); err != nil {
			return err
		}
	}

	d.config = &config
	if cfg.AgentConfig != nil {
		d.nomadConfig = cfg.AgentConfig.Driver
	}
	return nil
}

func (d *Driver) Shutdown() {
	d.signalShutdown()
}

func (d *Driver) TaskConfigSchema() (*hclspec.Spec, error) {
	return taskConfigSpec, nil
}

func (d *Driver) Capabilities() (*drivers.Capabilities, error) {
	return capabilities, nil
}

func (d *Driver) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	ch := make(chan *drivers.Fingerprint)
	go d.handleFingerprint(ctx, ch)
	return ch, nil
}

func (d *Driver) handleFingerprint(ctx context.Context, ch chan<- *drivers.Fingerprint) {
	defer close(ch)
	ticker := time.NewTimer(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			ticker.Reset(fingerprintPeriod)
			ch <- d.buildFingerprint()
		}
	}
}

func (d *Driver) buildFingerprint() *drivers.Fingerprint {
	var health drivers.HealthState
	var desc string
	attrs := map[string]*pstructs.Attribute{}
	if d.config.Enabled {
		health = drivers.HealthStateHealthy
		desc = drivers.DriverHealthy
		attrs["driver.fakeremote"] = pstructs.NewBoolAttribute(true)
	} else {
		health = drivers.HealthStateUndetected
		desc = "disabled"
	}

	return &drivers.Fingerprint{
		Attributes:        attrs,
		Health:            health,
		HealthDescription: desc,
	}
}

func (d *Driver) RecoverTask(handle *drivers.TaskHandle) error {
	d.logger.Info("RecoverTask() called", "handle", handle)
	if handle == nil {
		return fmt.Errorf("handle cannot be nil")
	}

	// If already attached to handle there's nothing to recover.
	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		d.logger.Info("nothing to recover; task already exists",
			"task_id", handle.Config.ID,
			"task_name", handle.Config.Name,
		)
		return nil
	}

	// Handle doesn't already exist, try to reattach
	var taskState TaskState
	if err := handle.GetDriverState(&taskState); err != nil {
		d.logger.Error("failed to decode task state from handle", "error", err, "task_id", handle.Config.ID)
		return fmt.Errorf("failed to decode task state from handle: %v", err)
	}

	d.logger.Info("RecoverTask() -> Reattached", "uuid", taskState.UUID, "started_at", taskState.StartedAt)

	h := newTaskHandle(d.logger, taskState)

	d.tasks.Set(taskState.TaskConfig.ID, h)

	go h.run()
	return nil
}

func (d *Driver) StartTask(cfg *drivers.TaskConfig) (*drivers.TaskHandle, *drivers.DriverNetwork, error) {
	d.logger.Info("StartTask() called")
	if !d.config.Enabled {
		return nil, nil, fmt.Errorf("disabled")
	}

	if _, ok := d.tasks.Get(cfg.ID); ok {
		return nil, nil, fmt.Errorf("task with ID %q already started", cfg.ID)
	}

	var driverConfig TaskConfig
	if err := cfg.DecodeDriverConfig(&driverConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode driver config: %v", err)
	}

	d.logger.Info("starting task", "driver_cfg", hclog.Fmt("%+v", driverConfig))
	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = cfg

	driverState := TaskState{
		TaskConfig: cfg,
		StartedAt:  time.Now(),
		UUID:       uuid.Generate()[:8],
	}

	d.logger.Info("StartTask() -> started", "uuid", driverState.UUID, "started_at", driverState.StartedAt)

	h := newTaskHandle(d.logger, driverState)

	if err := handle.SetDriverState(&driverState); err != nil {
		d.logger.Error("failed to start task, error setting driver state", "error", err)
		h.stop()
		return nil, nil, fmt.Errorf("failed to set driver state: %v", err)
	}

	d.tasks.Set(cfg.ID, h)
	go h.run()
	return handle, nil, nil
}

func (d *Driver) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	d.logger.Info("WaitTask() called", "task_id", taskID)
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	ch := make(chan *drivers.ExitResult)
	go d.handleWait(ctx, handle, ch)

	return ch, nil
}

func (d *Driver) handleWait(ctx context.Context, handle *taskHandle, ch chan *drivers.ExitResult) {
	defer close(ch)

	var result *drivers.ExitResult
	select {
	case <-ctx.Done():
		return
	case <-d.ctx.Done():
		return
	case <-handle.doneCh:
		result = &drivers.ExitResult{
			ExitCode: handle.exitResult.ExitCode,
			Signal:   handle.exitResult.Signal,
			Err:      nil,
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-d.ctx.Done():
		return
	case ch <- result:
	}
}

func (d *Driver) StopTask(taskID string, timeout time.Duration, signal string) error {
	d.logger.Info("StopTask() called", "task_id", taskID, "timeout", timeout, "signal", signal)
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	handle.stop()

	// Wait for handle to finish
	<-handle.doneCh

	return nil
}

func (d *Driver) DestroyTask(taskID string, force bool) error {
	d.logger.Info("DestroyTask() called", "task_id", taskID, "force", force)
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if handle.IsRunning() && !force {
		return fmt.Errorf("cannot destroy running task")
	}

	handle.stop()

	d.tasks.Delete(taskID)
	return nil
}

func (d *Driver) InspectTask(taskID string) (*drivers.TaskStatus, error) {
	d.logger.Info("InspectTask() called", "task_id", taskID)
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return handle.TaskStatus(), nil
}

func (d *Driver) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	d.logger.Info("TaskStats() called", "task_id", taskID)
	_, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	ch := make(chan *drivers.TaskResourceUsage)

	go func() {
		defer close(ch)
		for {
			select {
			case <-time.After(interval):
				//FIXME It's a bit silly to setup all of this
				// machinery to just emit nils but ¯\_(ツ)_/¯
				select {
				case ch <- nil:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}

		}
	}()

	return ch, nil
}

func (d *Driver) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	d.logger.Info("TaskEvents() called")
	return d.eventer.TaskEvents(ctx)
}

func (d *Driver) SignalTask(taskID string, signal string) error {
	d.logger.Info("SignalTask() called", "task_id", taskID, "signal", signal)
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	handle.stop()

	d.logger.Info("SignalTask() sent", "task_id", taskID, "signal", signal, "uuid", handle.uuid)

	return nil
}

func (d *Driver) ExecTask(taskID string, cmd []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	d.logger.Info("ExecTask() called", "cmd", strings.Join(cmd, " "), "timeout", timeout.String())

	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return &drivers.ExecTaskResult{
		Stdout: []byte(handle.uuid),
		ExitResult: &drivers.ExitResult{
			ExitCode: 0,
		},
	}, nil
}
