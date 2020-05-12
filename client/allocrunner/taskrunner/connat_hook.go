package taskrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	"github.com/hashicorp/nomad/client/connat"
	"github.com/hashicorp/nomad/nomad/structs"
	bstructs "github.com/hashicorp/nomad/plugins/base/structs"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// connatReattachKey is the HookData key where the connat reattach config
	// is stored.
	connatReattachKey = "reattach_connat_config"
)

type connatHookConfig struct {
	logger   hclog.Logger
	taskName string
}

func newConNatHookConfig(taskName string, logger hclog.Logger) *connatHookConfig {
	return &connatHookConfig{
		logger:   logger,
		taskName: taskName,
	}
}

// connatHook launches connat which is a go-plugin proxy for Connect Native
// applications running inside a network namespace.
type connatHook struct {
	logger   hclog.Logger
	taskName string

	// handle to the actual agent
	cm             connat.ConNat
	cmPluginClient *plugin.Client
}

func newConNatHook(c *connatHookConfig) *connatHook {
	fmt.Println("newConNatHook, task:", c.taskName)
	return &connatHook{
		logger:   c.logger,
		taskName: c.taskName,

		// todo: actual cm stuff
	}
}

func (h *connatHook) Name() string {
	return "connat"
}

func (h *connatHook) launchConNat(reattachConfig *plugin.ReattachConfig) error {
	fmt.Println("launchConNat")
	cm, c, err := connat.LaunchConNat(h.logger, reattachConfig)
	if err != nil {
		return err
	}
	h.cm = cm
	h.cmPluginClient = c
	return nil
}

func pluginUnavailable(err error) bool {
	return err == bstructs.ErrPluginShutdown || status.Code(err) == codes.Unavailable
}

func (h *connatHook) Prestart(
	ctx context.Context,
	req *interfaces.TaskPrestartRequest,
	resp *interfaces.TaskPrestartResponse) error {

	fmt.Println("connatHook.Prestart")

	for attempt := 1; ; attempt++ {
		if err := h.prestartAttempt(ctx, req); pluginUnavailable(err) {
			h.logger.Warn("connat unavailable while making request", "error", err)
			if attempt >= 3 {
				h.logger.Warn("connat unavailable while making request; giving up", "attempts", attempt, "error", err)
				return err
			}
			h.cmPluginClient.Kill()
			time.Sleep(1 * time.Second)
			continue
		} else if err != nil {
			return err
		}

		rCfg := pstructs.ReattachConfigFromGoPlugin(h.cmPluginClient.ReattachConfig())
		jsonCfg, err := json.Marshal(rCfg)
		if err != nil {
			return err
		}
		resp.State = map[string]string{"key": string(jsonCfg)}
		return nil
	}
}

func (h *connatHook) prestartAttempt(ctx context.Context, req *interfaces.TaskPrestartRequest) error {
	// attach to a running connat if state indicates one
	if h.cmPluginClient == nil {
		reattachConfig, err := reattachConfigFromHookData(req.PreviousState)
		if err != nil {
			h.logger.Error("failed to load reattach config", "error", err)
			return err
		}
		if reattachConfig != nil {
			if err := h.launchConNat(reattachConfig); err != nil {
				h.logger.Warn("failed to reattach to logmon process", "error", err)
				// if we failed to launch connat, try again below
			}
		}
	}

	// create a new client in initial start, failed reattachment, or if exit detected
	if h.cmPluginClient == nil || h.cmPluginClient.Exited() {
		if err := h.launchConNat(nil); err != nil {
			// Retry errors launching connat as connat may have crashed on start
			// and subsequent attempts will start a new one.
			h.logger.Error("failed to launch connat co-process", "error", err)
			return structs.NewRecoverableError(err, true)
		}
	}

	if err := h.cm.Start(&connat.Config{
		// config
	}); err != nil {
		h.logger.Error("failed to start connat", "error", err)
		return err
	}

	return nil
}

func (h *connatHook) Stop(
	_ context.Context,
	req *interfaces.TaskStopRequest,
	_ *interfaces.TaskStopResponse) error {

	fmt.Println("connatHook.Stop()")

	// It is possible Stop was called without calling Prestart on agent restarts.
	// Attempt to reattach to an existing connat.
	if h.cm == nil || h.cmPluginClient == nil {
		if err := h.reattachBeforeStop(req); err != nil {
			h.logger.Trace("error reattaching to connat when stopping", "error", err)
		}
	}

	if h.cm != nil {
		_ = h.cm.Stop()
	}

	if h.cmPluginClient != nil {
		h.cmPluginClient.Kill()
	}

	return nil
}

func (h *connatHook) reattachBeforeStop(req *interfaces.TaskStopRequest) error {
	reattachConfig, err := reattachConfigFromHookData(req.ExistingState)
	if err != nil {
		return err
	}

	// give up if there is no reattach config
	if reattachConfig == nil {
		return nil
	}

	return h.launchConNat(reattachConfig)
}
