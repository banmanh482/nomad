package ecs

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/helper/pluginutils/loader"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
)

const (
	// pluginName is the name of the plugin
	pluginName = "ecs"

	// fingerprintPeriod is the interval at which the driver will send fingerprint responses
	fingerprintPeriod = 30 * time.Second

	// taskHandleVersion is ...
	taskHandleVersion = 1
)

var (
	// pluginInfo is the response returned for the PluginInfo RPC
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     "0.1.0",
		Name:              pluginName,
	}

	PluginID = loader.PluginID{
		Name:       pluginName,
		PluginType: base.PluginTypeDriver,
	}

	PluginConfig = &loader.InternalPluginConfig{
		Config:  map[string]interface{}{},
		Factory: func(l hclog.Logger) interface{} { return NewPlugin(l) },
	}

	// configSpec is the hcl specification returned by the ConfigSchema RPC
	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"enabled":    hclspec.NewAttr("enabled", "bool", false),
		"region":     hclspec.NewAttr("region", "string", false),
		"cluster":    hclspec.NewAttr("cluster", "string", false),
		"access_key": hclspec.NewAttr("access_key", "string", false),
		"secret_key": hclspec.NewAttr("secret_key", "string", false),
	})

	// taskConfigSpec is the hcl specification for the driver config section of
	// a task within a job. It is returned in the TaskConfigSchema RPC
	taskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"mode":  hclspec.NewAttr("mode", "string", false),
		"image": hclspec.NewAttr("image", "string", false),
	})

	// capabilities is returned by the Capabilities RPC and indicates what
	// optional features this driver supports
	capabilities = &drivers.Capabilities{
		SendSignals: false,
		Exec:        false,
		FSIsolation: drivers.FSIsolationImage,
		RemoteTasks: true,
	}
)

// Driver is a driver for running ECS containers
type Driver struct {
	// eventer is used to handle multiplexing of TaskEvents calls such that an
	// event can be broadcast to all callers
	eventer *eventer.Eventer

	// config is the driver configuration set by the SetConfig RPC
	config *DriverConfig

	// nomadConfig is the client config from nomad
	nomadConfig *base.ClientDriverConfig

	// tasks is the in memory datastore mapping taskIDs to rawExecDriverHandles
	tasks *taskStore

	// ctx is the context for the driver. It is passed to other subsystems to
	// coordinate shutdown
	ctx context.Context

	// signalShutdown is called when the driver is shutting down and cancels the
	// ctx passed to any subsystems
	signalShutdown context.CancelFunc

	// logger will log to the Nomad agent
	logger hclog.Logger

	// ecsClientInterface is the interface used for communicating with AWS ECS
	client ecsClientInterface
}

// DriverConfig is the driver configuration set by the SetConfig RPC call
type DriverConfig struct {
	Enabled   bool   `codec:"enabled"`
	Region    string `coded:"region"`
	Cluster   string `coded:"cluster"`
	AccessKey string `coded:"access_key"`
	SecretKey string `coded:"secret_key"`
}

// TaskConfig is the driver configuration of a task within a job
type TaskConfig struct {
	Mode  string `codec:"mode"`
	Image string `codec:"image"`
}

// TaskState is the state which is encoded in the handle returned in
// StartTask. This information is needed to rebuild the task state and handler
// during recovery.
type TaskState struct {
	TaskConfig    *drivers.TaskConfig
	ContainerName string
	ARN           string
	StartedAt     time.Time
}

// NewECSDriver returns a new DriverPlugin implementation
func NewPlugin(logger hclog.Logger) drivers.DriverPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)
	return &Driver{
		eventer:        eventer.NewEventer(ctx, logger),
		config:         &DriverConfig{},
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
	var config DriverConfig
	if len(cfg.PluginConfig) != 0 {
		if err := base.MsgPackDecode(cfg.PluginConfig, &config); err != nil {
			return err
		}
	}

	d.logger.Info("-------> config", "config", config)

	d.config = &config
	if cfg.AgentConfig != nil {
		d.nomadConfig = cfg.AgentConfig.Driver
	}

	client, err := d.getAwsSdk()
	if err != nil {
		return fmt.Errorf("failed to get AWS SDK client: %v", err)
	}
	d.client = client

	return nil
}

func (d *Driver) getAwsSdk() (ecsClientInterface, error) {
	awsCfg, err := external.LoadDefaultAWSConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %v", err)
	}

	//if d.config.Region != "" {
	//	awsCfg.Region = d.config.Region
	//}

	awsCfg.Region = "us-east-1"

	return awsEcsClient{
		ecsClient: ecs.New(awsCfg),
	}, nil
}

func (d *Driver) Shutdown(ctx context.Context) error {
	d.signalShutdown()
	return nil
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
			ch <- d.buildFingerprint(ctx)
		}
	}
}

func (d *Driver) buildFingerprint(ctx context.Context) *drivers.Fingerprint {
	var health drivers.HealthState
	var desc string
	attrs := map[string]*pstructs.Attribute{}

	if d.config.Enabled {
		if _, err := d.client.ListClusters(ctx); err != nil {
			health = drivers.HealthStateUnhealthy
			desc = err.Error()
			attrs["driver.ecs"] = pstructs.NewBoolAttribute(false)
		} else {
			health = drivers.HealthStateHealthy
			desc = "ready"
			attrs["driver.ecs"] = pstructs.NewBoolAttribute(true)
		}
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
	d.logger.Info("RecoverTask() called", "version", handle.Version, "task_config.id", handle.Config.ID, "task_state", handle.State, "driver_state_bytes", len(handle.DriverState))
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

	d.logger.Info("RecoverTask() -> Reattached", "arn", taskState.ARN, "started_at", taskState.StartedAt)

	h := newTaskHandle(d.logger, taskState, handle.Config, d.client)

	d.tasks.Set(handle.Config.ID, h)

	go h.run()
	d.logger.Info("RecoverTask() DONE", "arn", taskState.ARN)
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

	arn, err := d.client.RunTask(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start ECS task: %v", err)
	}

	driverState := TaskState{
		TaskConfig: cfg,
		StartedAt:  time.Now(),
		ARN:        arn,
	}

	d.logger.Info("StartTask() -> started", "arn", driverState.ARN, "started_at", driverState.StartedAt)

	h := newTaskHandle(d.logger, driverState, cfg, d.client)

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
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}
	return handle.TaskStatus(), nil
}

func (d *Driver) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *structs.TaskResourceUsage, error) {
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
	return fmt.Errorf("ECS driver does not support signals")
}

func (d *Driver) ExecTask(taskID string, cmd []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	return nil, fmt.Errorf("ECS driver does not support exec")
}
