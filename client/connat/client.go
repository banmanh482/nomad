package connat

import (
	"context"
	"time"

	"github.com/hashicorp/nomad/client/connat/proto"
	"github.com/hashicorp/nomad/helper/pluginutils/grpcutils"
)

type connatClient struct {
	client proto.ConNatClient

	// doneCtx is closed when the plugin exits
	doneCtx context.Context
}

const connnatRPCTimeout = 1 * time.Minute

func (cc *connatClient) Start(c *Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), connnatRPCTimeout)
	defer cancel()

	_, err := cc.client.Start(ctx, new(proto.StartRequest))
	return grpcutils.HandleGrpcErr(err, cc.doneCtx)
}

func (cc *connatClient) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), connnatRPCTimeout)
	defer cancel()

	_, err := cc.client.Stop(ctx, new(proto.StopRequest))
	return grpcutils.HandleGrpcErr(err, cc.doneCtx)
}
