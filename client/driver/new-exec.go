package driver

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/client/fingerprint"
	"github.com/hashicorp/nomad/client/stats"
	"github.com/hashicorp/nomad/helper"
	"github.com/hashicorp/nomad/helper/discover"
	"github.com/hashicorp/nomad/helper/fields"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/mitchellh/mapstructure"
	"github.com/opencontainers/runc/libcontainer"

	dstructs "github.com/hashicorp/nomad/client/driver/structs"
	cstructs "github.com/hashicorp/nomad/client/structs"
	lconfigs "github.com/opencontainers/runc/libcontainer/configs"
)

const (
	// The key populated in Node Attributes to indicate the presence of the Exec
	// driver
	execerDriverAttr = "driver.execv2"
)

var (
	// The statistics the executor exposes when using cgroups
	cgroupMeasuredMemStats = []string{"RSS", "Cache", "Swap", "Max Usage", "Kernel Usage", "Kernel Max Usage"}
	cgroupMeasuredCpuStats = []string{"System Mode", "User Mode", "Throttled Periods", "Throttled Time", "Percent"}

	// finishedErr is the error message received when trying to kill and already
	// exited process.
	finishedErr = "os: process already finished"
)

type ExecerDriver struct {
	DriverContext
	fingerprint.StaticFingerprinter

	// A tri-state boolean to know if the fingerprinting has happened and
	// whether it has been successful
	fingerprintSuccess *bool
}

type execerHandle struct {
	totalCpuStats  *stats.CpuStats
	userCpuStats   *stats.CpuStats
	systemCpuStats *stats.CpuStats

	process   *libcontainer.Process
	container libcontainer.Container

	taskDir *allocdir.TaskDir

	killTimeout    time.Duration
	maxKillTimeout time.Duration

	logger *log.Logger

	waitCh chan *dstructs.WaitResult
	doneCh chan struct{}

	version string
}

type ExecerDriverConfig struct {
	Command string   `mapstructure:"command"`
	Args    []string `mapstructure:"args"`
}

func NewExecerDriver(ctx *DriverContext) Driver {
	return &ExecerDriver{DriverContext: *ctx}
}

func (d *ExecerDriver) Fingerprint(cfg *config.Config, node *structs.Node) (bool, error) {
	// Only enable if cgroups are available and we are root
	if !cgroupsMounted(node) {
		if d.fingerprintSuccess == nil || *d.fingerprintSuccess {
			d.logger.Printf("[DEBUG] driver.execer: cgroups unavailable, disabling")
		}
		d.fingerprintSuccess = helper.BoolToPtr(false)
		delete(node.Attributes, execerDriverAttr)
		return false, nil
	} else if unix.Geteuid() != 0 {
		if d.fingerprintSuccess == nil || *d.fingerprintSuccess {
			d.logger.Printf("[DEBUG] driver.execer: must run as root user, disabling")
		}
		delete(node.Attributes, execerDriverAttr)
		d.fingerprintSuccess = helper.BoolToPtr(false)
		return false, nil
	}

	if d.fingerprintSuccess == nil || !*d.fingerprintSuccess {
		d.logger.Printf("[DEBUG] driver.execer: exec driver is enabled")
	}
	node.Attributes[execerDriverAttr] = "1"
	d.fingerprintSuccess = helper.BoolToPtr(true)
	return true, nil
}

// Validate is used to validate the driver configuration
func (d *ExecerDriver) Validate(config map[string]interface{}) error {
	fd := &fields.FieldData{
		Raw: config,
		Schema: map[string]*fields.FieldSchema{
			"command": &fields.FieldSchema{
				Type:     fields.TypeString,
				Required: true,
			},
			"args": &fields.FieldSchema{
				Type: fields.TypeArray,
			},
		},
	}

	if err := fd.Validate(); err != nil {
		return err
	}

	return nil
}

func (d *ExecerDriver) Abilities() DriverAbilities {
	return DriverAbilities{
		SendSignals: true,
		Exec:        false,
	}
}

func (d *ExecerDriver) FSIsolation() cstructs.FSIsolation { return cstructs.FSIsolationChroot }
func (d *ExecerDriver) Prestart(*ExecContext, *structs.Task) (*PrestartResponse, error) {
	return nil, nil
}
func (d *ExecerDriver) Cleanup(*ExecContext, *CreatedResources) error { return nil }

func (d *ExecerDriver) Start(ctx *ExecContext, task *structs.Task) (*StartResponse, error) {
	var driverConfig ExecerDriverConfig
	if err := mapstructure.WeakDecode(task.Config, &driverConfig); err != nil {
		return nil, err
	}

	bin, err := discover.NomadExecutable()
	if err != nil {
		return nil, fmt.Errorf("unable to find the nomad binary: %v", err)
	}

	factory, err := libcontainer.New(ctx.TaskDir.Dir, libcontainer.Cgroupfs, libcontainer.InitArgs(bin, "libcontainer-shim"))
	if err != nil {
		wrapped := fmt.Errorf("failed to create factory: %v", err)
		d.logger.Printf("[ERR] driver.execv2: %v", wrapped)
		return nil, wrapped
	}

	container, err := factory.Create(d.DriverContext.allocID, newLibcontainerConfig(ctx.TaskDir.Dir))
	if err != nil {
		wrapped := fmt.Errorf("failed to create container: %v", err)
		d.logger.Printf("[ERR] driver.execv2: %v", wrapped)
		return nil, wrapped
	}

	combined := make([]string, 0, len(driverConfig.Args)+1)
	combined = append(combined, driverConfig.Command)
	combined = append(combined, driverConfig.Args...)

	process := &libcontainer.Process{
		Args:   combined,
		Env:    ctx.TaskEnv.List(),
		Stdin:  nil,
		Stdout: os.Stdout, // TODO
		Stderr: os.Stderr,
	}

	if err := container.Run(process); err != nil {
		container.Destroy()
		return nil, err
	}

	// Return a driver handle
	maxKill := d.DriverContext.config.MaxKillTimeout
	h := &execerHandle{
		totalCpuStats:  stats.NewCpuStats(),
		userCpuStats:   stats.NewCpuStats(),
		systemCpuStats: stats.NewCpuStats(),
		process:        process,
		container:      container,
		killTimeout:    GetKillTimeout(task.KillTimeout, maxKill),
		maxKillTimeout: maxKill,
		logger:         d.logger,
		version:        d.config.Version.VersionNumber(),
		doneCh:         make(chan struct{}),
		waitCh:         make(chan *dstructs.WaitResult, 1),
		taskDir:        ctx.TaskDir,
	}
	go h.run()
	return &StartResponse{Handle: h}, nil
}

func (d *ExecerDriver) Open(ctx *ExecContext, handleID string) (DriverHandle, error) {
	return nil, fmt.Errorf("unsupported")
}

func (h *execerHandle) ID() string {
	return ""
}

func (h *execerHandle) WaitCh() chan *dstructs.WaitResult {
	return h.waitCh
}

func (h *execerHandle) Update(task *structs.Task) error {
	// Update is not possible
	h.killTimeout = GetKillTimeout(task.KillTimeout, h.maxKillTimeout)
	return nil
}

func (h *execerHandle) Exec(ctx context.Context, cmd string, args []string) ([]byte, int, error) {
	// TODO
	return nil, 0, nil
}

func (h *execerHandle) Signal(s os.Signal) error {
	return h.process.Signal(s)
}

func (h *execerHandle) Kill() error {
	if err := h.Signal(os.Interrupt); err != nil && !strings.Contains(err.Error(), "no such process") {
		return fmt.Errorf("executor Shutdown failed: %v", err)
	}

	select {
	case <-h.doneCh:
	case <-time.After(h.killTimeout):
		if err := h.container.Signal(os.Kill, true); err != nil {
			return fmt.Errorf("executor Exit failed: %v", err)
		}
	}
	return nil
}

func (h *execerHandle) Stats() (*cstructs.TaskResourceUsage, error) {
	lstats, err := h.container.Stats()
	if err != nil {
		return nil, err
	}

	ts := time.Now()
	stats := lstats.CgroupStats

	// Memory Related Stats
	swap := stats.MemoryStats.SwapUsage
	maxUsage := stats.MemoryStats.Usage.MaxUsage
	rss := stats.MemoryStats.Stats["rss"]
	cache := stats.MemoryStats.Stats["cache"]
	ms := &cstructs.MemoryStats{
		RSS:            rss,
		Cache:          cache,
		Swap:           swap.Usage,
		MaxUsage:       maxUsage,
		KernelUsage:    stats.MemoryStats.KernelUsage.Usage,
		KernelMaxUsage: stats.MemoryStats.KernelUsage.MaxUsage,
		Measured:       cgroupMeasuredMemStats,
	}

	// CPU Related Stats
	totalProcessCPUUsage := float64(stats.CpuStats.CpuUsage.TotalUsage)
	userModeTime := float64(stats.CpuStats.CpuUsage.UsageInUsermode)
	kernelModeTime := float64(stats.CpuStats.CpuUsage.UsageInKernelmode)

	totalPercent := h.totalCpuStats.Percent(totalProcessCPUUsage)
	cs := &cstructs.CpuStats{
		SystemMode:       h.systemCpuStats.Percent(kernelModeTime),
		UserMode:         h.userCpuStats.Percent(userModeTime),
		Percent:          totalPercent,
		ThrottledPeriods: stats.CpuStats.ThrottlingData.ThrottledPeriods,
		ThrottledTime:    stats.CpuStats.ThrottlingData.ThrottledTime,
		TotalTicks:       h.systemCpuStats.TicksConsumed(totalPercent),
		Measured:         cgroupMeasuredCpuStats,
	}
	taskResUsage := cstructs.TaskResourceUsage{
		ResourceUsage: &cstructs.ResourceUsage{
			MemoryStats: ms,
			CpuStats:    cs,
		},
		Timestamp: ts.UTC().UnixNano(),
		// TODO Pids
	}
	return &taskResUsage, nil
}

func (h *execerHandle) run() {
	// Wait for the process to finish.
	ps, werr := h.process.Wait()
	close(h.doneCh)

	// Destroy the container.
	if err := h.container.Destroy(); err != nil {
		h.logger.Printf("[ERR] driver.execv2: destroy errored: %v", err)
	}

	exitCode := 1
	var signal int
	if ps != nil {
		if status, ok := ps.Sys().(syscall.WaitStatus); ok {
			exitCode = status.ExitStatus()
			if status.Signaled() {
				// bash(1) uses the lower 7 bits of a uint8
				// to indicate normal program failure (see
				// <sysexits.h>). If a process terminates due
				// to a signal, encode the signal number to
				// indicate which signal caused the process
				// to terminate.  Mirror this exit code
				// encoding scheme.
				const exitSignalBase = 128
				signal = int(status.Signal())
				exitCode = exitSignalBase + signal
			}
		} else {
			h.logger.Printf("[DEBUG] driver.execv2: unexpected Wait() error type: %T", ps.Sys())
		}
	}

	// Send the results
	h.waitCh <- dstructs.NewWaitResult(exitCode, signal, werr)
	close(h.waitCh)
}

func newLibcontainerConfig(rootfs string) *lconfigs.Config {
	defaultMountFlags := syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV
	return &lconfigs.Config{
		Rootfs: rootfs,
		Capabilities: &lconfigs.Capabilities{
			Bounding: []string{
				"CAP_CHOWN",
				"CAP_DAC_OVERRIDE",
				"CAP_FSETID",
				"CAP_FOWNER",
				"CAP_MKNOD",
				"CAP_NET_RAW",
				"CAP_SETGID",
				"CAP_SETUID",
				"CAP_SETFCAP",
				"CAP_SETPCAP",
				"CAP_NET_BIND_SERVICE",
				"CAP_SYS_CHROOT",
				"CAP_KILL",
				"CAP_AUDIT_WRITE",
			},
			Permitted: []string{
				"CAP_CHOWN",
				"CAP_DAC_OVERRIDE",
				"CAP_FSETID",
				"CAP_FOWNER",
				"CAP_MKNOD",
				"CAP_NET_RAW",
				"CAP_SETGID",
				"CAP_SETUID",
				"CAP_SETFCAP",
				"CAP_SETPCAP",
				"CAP_NET_BIND_SERVICE",
				"CAP_SYS_CHROOT",
				"CAP_KILL",
				"CAP_AUDIT_WRITE",
			},
			Inheritable: []string{
				"CAP_CHOWN",
				"CAP_DAC_OVERRIDE",
				"CAP_FSETID",
				"CAP_FOWNER",
				"CAP_MKNOD",
				"CAP_NET_RAW",
				"CAP_SETGID",
				"CAP_SETUID",
				"CAP_SETFCAP",
				"CAP_SETPCAP",
				"CAP_NET_BIND_SERVICE",
				"CAP_SYS_CHROOT",
				"CAP_KILL",
				"CAP_AUDIT_WRITE",
			},
			Ambient: []string{
				"CAP_CHOWN",
				"CAP_DAC_OVERRIDE",
				"CAP_FSETID",
				"CAP_FOWNER",
				"CAP_MKNOD",
				"CAP_NET_RAW",
				"CAP_SETGID",
				"CAP_SETUID",
				"CAP_SETFCAP",
				"CAP_SETPCAP",
				"CAP_NET_BIND_SERVICE",
				"CAP_SYS_CHROOT",
				"CAP_KILL",
				"CAP_AUDIT_WRITE",
			},
			Effective: []string{
				"CAP_CHOWN",
				"CAP_DAC_OVERRIDE",
				"CAP_FSETID",
				"CAP_FOWNER",
				"CAP_MKNOD",
				"CAP_NET_RAW",
				"CAP_SETGID",
				"CAP_SETUID",
				"CAP_SETFCAP",
				"CAP_SETPCAP",
				"CAP_NET_BIND_SERVICE",
				"CAP_SYS_CHROOT",
				"CAP_KILL",
				"CAP_AUDIT_WRITE",
			},
		},
		Namespaces: lconfigs.Namespaces([]lconfigs.Namespace{
			{Type: lconfigs.NEWNS},
			{Type: lconfigs.NEWUTS},
			{Type: lconfigs.NEWIPC},
			{Type: lconfigs.NEWPID},
			//{Type: lconfigs.NEWUSER},
			{Type: lconfigs.NEWNET},
		}),
		Cgroups: &lconfigs.Cgroup{
			Name:   "test-container",
			Parent: "system",
			Resources: &lconfigs.Resources{
				MemorySwappiness: nil,
				AllowAllDevices:  helper.BoolToPtr(false),
				AllowedDevices:   lconfigs.DefaultAllowedDevices,
			},
		},
		MaskPaths: []string{
			"/proc/kcore",
			"/sys/firmware",
		},
		ReadonlyPaths: []string{
			"/proc/sys", "/proc/sysrq-trigger", "/proc/irq", "/proc/bus",
		},
		Devices:  lconfigs.DefaultAutoCreatedDevices,
		Hostname: "testing",
		Mounts: []*lconfigs.Mount{
			{
				Source:      "proc",
				Destination: "/proc",
				Device:      "proc",
				Flags:       defaultMountFlags,
			},
			{
				Source:      "tmpfs",
				Destination: "/dev",
				Device:      "tmpfs",
				Flags:       syscall.MS_NOSUID | syscall.MS_STRICTATIME,
				Data:        "mode=755",
			},
			{
				Source:      "devpts",
				Destination: "/dev/pts",
				Device:      "devpts",
				Flags:       syscall.MS_NOSUID | syscall.MS_NOEXEC,
				Data:        "newinstance,ptmxmode=0666,mode=0620,gid=5",
			},
			{
				Device:      "tmpfs",
				Source:      "shm",
				Destination: "/dev/shm",
				Data:        "mode=1777,size=65536k",
				Flags:       defaultMountFlags,
			},
			{
				Source:      "mqueue",
				Destination: "/dev/mqueue",
				Device:      "mqueue",
				Flags:       defaultMountFlags,
			},
			{
				Source:      "sysfs",
				Destination: "/sys",
				Device:      "sysfs",
				Flags:       defaultMountFlags | syscall.MS_RDONLY,
			},
		},
		//UidMappings: []lconfigs.IDMap{
		//{
		//ContainerID: 0,
		//HostID:      1000,
		//Size:        65536,
		//},
		//},
		//GidMappings: []lconfigs.IDMap{
		//{
		//ContainerID: 0,
		//HostID:      1000,
		//Size:        65536,
		//},
		//},
		Networks: []*lconfigs.Network{
			{
				Type:    "loopback",
				Address: "127.0.0.1/0",
				Gateway: "localhost",
			},
		},
		Rlimits: []lconfigs.Rlimit{
			{
				Type: syscall.RLIMIT_NOFILE,
				Hard: uint64(1025),
				Soft: uint64(1025),
			},
		},
	}
}
