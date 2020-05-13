package connat

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/connat/proto"
)

type connatServer struct {
	broker *plugin.GRPCBroker
	cm     ConNat
}

func (s *connatServer) Start(ctx context.Context, req *proto.StartRequest) (*proto.StartResponse, error) {
	fmt.Println("connatServer.Start")
	if err := s.cm.Start(&Config{
		SocketPath: "unix://" + allocdir.AllocHTTPSocket, // like envoy hook
		BindTo:     "127.0.0.1:8500",                     // todo plumb
	}); err != nil {
		fmt.Println("connatServer.Start failed err:", err)
		return nil, err
	}
	fmt.Println("connatServer.Start OK")
	return new(proto.StartResponse), nil
}

func (s *connatServer) Stop(ctx context.Context, req *proto.StopRequest) (*proto.StopResponse, error) {
	return new(proto.StopResponse), s.cm.Stop()
}
