package conmon

import (
	"context"
	"os"
	"os/exec"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/client/conmon/proto"
	"github.com/hashicorp/nomad/plugins/base"
	"google.golang.org/grpc"
)

const (
	PluginName = "conmon"
)

func LaunchConMon(root hclog.Logger, reattachConfig *plugin.ReattachConfig) (ConMon, *plugin.Client, error) {
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

func newClient(c *plugin.ClientConfig) (ConMon, *plugin.Client, error) {
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

	return raw.(ConMon), client, nil
}

type Plugin struct {
	plugin.NetRPCUnsupportedPlugin
	cm ConMon
}

func NewPlugin(cm ConMon) plugin.Plugin {
	return &Plugin{cm: cm}
}

func (p *Plugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterConMonServer(s, &conmonServer{
		cm:     p.cm,
		broker: broker,
	})
	return nil
}

func (p *Plugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, cc *grpc.ClientConn) (interface{}, error) {
	return &conmonClient{
		doneCtx: ctx,
		client:  proto.NewConMonClient(cc),
	}, nil
}
