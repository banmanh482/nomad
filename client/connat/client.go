package connat

import (
	"context"
	"fmt"
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
	fmt.Println("connatClient.Start, config:", c)
	ctx, cancel := context.WithTimeout(context.Background(), connnatRPCTimeout)
	defer cancel()

	_, err := cc.client.Start(ctx, new(proto.StartRequest))
	fmt.Println("  -> err:", err)
	return grpcutils.HandleGrpcErr(err, cc.doneCtx)
}

func (cc *connatClient) Stop() error {
	fmt.Println("connatClient.Stop")
	ctx, cancel := context.WithTimeout(context.Background(), connnatRPCTimeout)
	defer cancel()

	_, err := cc.client.Stop(ctx, new(proto.StopRequest))
	fmt.Println("  -> err:", err)
	return grpcutils.HandleGrpcErr(err, cc.doneCtx)
}
