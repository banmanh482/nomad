package connat

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/client/connat/proto"
)

type connatServer struct {
	broker *plugin.GRPCBroker
	cm     ConNat
}

func (s *connatServer) Start(ctx context.Context, req *proto.StartRequest) (*proto.StartResponse, error) {
	if err := s.cm.Start(&Config{
		// options
	}); err != nil {
		return nil, err
	}
	return new(proto.StartResponse), nil
}

func (s *connatServer) Stop(ctx context.Context, req *proto.StopRequest) (*proto.StopResponse, error) {
	return new(proto.StopResponse), s.cm.Stop()
}
