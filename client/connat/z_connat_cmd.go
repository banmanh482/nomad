package connat

import (
	"os"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/plugins/base"
)

// Install a CLI plugin handler to ease working with tests and external plugins.
// This init() must be initialized last in package required by the child plugin
// process. It is recommended to avoid any other 'init' or inline any necessary
// calls here. See eeaa95d commit message for more details.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "connat" {
		logger := hclog.New(&hclog.LoggerOptions{
			Level:      hclog.Trace,
			JSONFormat: true,
			Name:       "connat",
		})
		plugin.Serve(&plugin.ServeConfig{
			HandshakeConfig: base.Handshake,
			Plugins: map[string]plugin.Plugin{
				"connat": NewPlugin(New(logger)),
			},
			GRPCServer: plugin.DefaultGRPCServer,
			Logger:     logger,
		})
		os.Exit(0)
	}
}
