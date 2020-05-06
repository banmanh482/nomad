package conmon

import (
	"context"
	"time"

	"github.com/hashicorp/nomad/client/conmon/proto"
)

type conmonClient struct {
	client proto.ConMonClient

	// doneCtx is closed when the plugin exits
	doneCtx context.Context
}

const conmonRPCTimeout = 1 * time.Minute

//
