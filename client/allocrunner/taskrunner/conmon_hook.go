package taskrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	"github.com/hashicorp/nomad/client/conmon"
	"github.com/hashicorp/nomad/nomad/structs"
	bstructs "github.com/hashicorp/nomad/plugins/base/structs"
	pstructs "github.com/hashicorp/nomad/plugins/shared/structs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// conmonReattachKey is the HookData key where the conmon reattach config
	// is stored.
	conmonReattachKey = "reattach_conmon_config"
)

// conmonHook launches conmon is a proxy for Connect Native applications
// running inside a network namespace
type conmonHook struct {
	config *conmonHookConfig
	logger hclog.Logger

	runner *TaskRunner // not needed ?

	// handle to the actual agent
	cm             conmon.ConMon
	cmPluginClient *plugin.Client
}

type conmonHookConfig struct {
	// options
}

func newConMonHook(tr *TaskRunner, logger hclog.Logger) *conmonHook {
	return &conmonHook{
		runner: tr,
		config: tr.conmonHookConfig,
	}
}

func newConMonHookConfig(taskName string) *conmonHookConfig {
	return &conmonHookConfig{
		// options
	}
}

func (h *conmonHook) Name() string {
	return "conmon"
}

func (h *conmonHook) launchConMon(reattachConfig *plugin.ReattachConfig) error {
	cm, c, err := conmon.LaunchConMon(h.logger, reattachConfig)
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

func (h *conmonHook) Prestart(
	ctx context.Context,
	req *interfaces.TaskPrestartRequest,
	resp *interfaces.TaskPrestartResponse) error {

	for attempt := 1; ; attempt++ {
		if err := h.prestartAttempt(ctx, req); pluginUnavailable(err) {
			h.logger.Warn("conmon unavailable while making request", "error", err)
			if attempt >= 3 {
				h.logger.Warn("conmon unavailable while making request; giving up", "attempts", attempt, "error", err)
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

func (h *conmonHook) prestartAttempt(ctx context.Context, req *interfaces.TaskPrestartRequest) error {
	// attach to a running conmon if state indicates one
	if h.cmPluginClient == nil {
		reattachConfig, err := reattachConfigFromHookData(req.PreviousState)
		if err != nil {
			h.logger.Error("failed to load reattach config", "error", err)
			return err
		}
		if reattachConfig != nil {
			if err := h.launchConMon(reattachConfig); err != nil {
				h.logger.Warn("failed to reattach to logmon process", "error", err)
				// if we failed to launch conmon, try again below
			}
		}
	}

	// create a new client in initial start, failed reattachment, or if exit detected
	if h.cmPluginClient == nil || h.cmPluginClient.Exited() {
		if err := h.launchConMon(nil); err != nil {
			// Retry errors launching conmon as conmon may have crashed on start
			// and subsequent attempts will start a new one.
			h.logger.Error("failed to launch conmon co-process", "error", err)
			return structs.NewRecoverableError(err, true)
		}
	}

	if err := h.cm.Start(&conmon.Config{
		// config
	}); err != nil {
		h.logger.Error("failed to start conmon", "error", err)
		return err
	}

	return nil
}

func (h *conmonHook) Stop(
	_ context.Context,
	req *interfaces.TaskStopRequest,
	_ *interfaces.TaskStopResponse) error {

	fmt.Println("SH comnmonHook.Stop()")

	// It is possible Stop was called without calling Prestart on agent restarts.
	// Attempt to reattach to an existing conmon.
	if h.cm == nil || h.cmPluginClient == nil {
		if err := h.reattachBeforeStop(req); err != nil {
			h.logger.Trace("error reattaching to conmon when stopping", "error", err)
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

func (h *conmonHook) reattachBeforeStop(req *interfaces.TaskStopRequest) error {
	reattachConfig, err := reattachConfigFromHookData(req.ExistingState)
	if err != nil {
		return err
	}

	// give up if there is no reattach config
	if reattachConfig == nil {
		return nil
	}

	return h.launchConMon(reattachConfig)
}
