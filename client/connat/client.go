package connat

import (
	"context"
	"time"

	"github.com/hashicorp/nomad/client/connat/proto"
)

type connatClient struct {
	client proto.ConNatClient

	// doneCtx is closed when the plugin exits
	doneCtx context.Context
}

const connnatRPCTimeout = 1 * time.Minute
