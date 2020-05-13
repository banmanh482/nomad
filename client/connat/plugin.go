package connat

import (
	"context"
	"os"
	"os/exec"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/client/connat/proto"
	"github.com/hashicorp/nomad/plugins/base"
	"google.golang.org/grpc"
)

const (
	PluginName = "connat"
)

func LaunchConNat(root hclog.Logger, reattachConfig *plugin.ReattachConfig) (ConNat, *plugin.Client, error) {
	logger := root.Named(PluginName)
	bin, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}

	pluginConf := &plugin.ClientConfig{
		HandshakeConfig:  base.Handshake,
		Plugins:          map[string]plugin.Plugin{PluginName: new(Plugin)},
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger:           logger,
	}

	// only set one of Cmd or Reattach
	if reattachConfig == nil {
		pluginConf.Cmd = exec.Command(bin, PluginName)
	} else {
		pluginConf.Reattach = reattachConfig
	}

	return newClient(pluginConf)
}

func newClient(c *plugin.ClientConfig) (ConNat, *plugin.Client, error) {
	// create gRPC client
	client := plugin.NewClient(c)
	rpcClient, err := client.Client()
	if err != nil {
		return nil, nil, err
	}

	raw, err := rpcClient.Dispense(PluginName)
	if err != nil {
		return nil, nil, err
	}

	return raw.(ConNat), client, nil
}

type Plugin struct {
	plugin.NetRPCUnsupportedPlugin
	cm ConNat
}

func NewPlugin(cm ConNat) plugin.Plugin {
	// todo: plumb bind config somehow
	return &Plugin{cm: cm}
}

func (p *Plugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterConNatServer(s, &connatServer{
		cm:     p.cm,
		broker: broker,
	})
	return nil
}

func (p *Plugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, cc *grpc.ClientConn) (interface{}, error) {
	return &connatClient{
		doneCtx: ctx,
		client:  proto.NewConNatClient(cc),
	}, nil
}
