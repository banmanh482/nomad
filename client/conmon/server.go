package conmon

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/client/conmon/proto"
)

type conmonServer struct {
	broker *plugin.GRPCBroker
	cm     ConMon
}

func (s *conmonServer) Start(ctx context.Context, req *proto.StartRequest) (*proto.StartResponse, error) {
	if err := s.cm.Start(&Config{
		// options
	}); err != nil {
		return nil, err
	}
	return new(proto.StartResponse), nil
}

func (s *conmonServer) Stop(ctx context.Context, req *proto.StopRequest) (*proto.StopResponse, error) {
	return new(proto.StopResponse), s.cm.Stop()
}
