package allocrunner

import (
	"context"
	"path/filepath"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/nomad/structs/config"
	"github.com/pkg/errors"
)

const (
	httpProtocolName = "HTTP"
)

type httpConsulSocketHook struct {
	alloc *structs.Allocation
}

type httpProxy struct {
	logger       hclog.Logger
	allocDir     *allocdir.AllocDir
	consulConfig *config.ConsulConfig // the real consul

	ctx     context.Context
	cancel  func()
	doneC   chan struct{}
	runOnce bool
}

func newHTTPProxy(logger hclog.Logger, allocDir *allocdir.AllocDir, config *config.ConsulConfig) *httpProxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &httpProxy{
		allocDir:     allocDir,
		consulConfig: config,
		ctx:          ctx,
		cancel:       cancel,
		doneC:        make(chan struct{}),
		logger:       logger,
	}
}

func (s *httpProxy) run(alloc *structs.Allocation) error {
	// Only run once.
	if s.runOnce {
		return nil
	}

	// Only run once, never restart.
	select {
	case <-s.doneC:
		s.logger.Trace("socket proxy already shutdown; exiting")
		return nil
	case <-s.ctx.Done():
		s.logger.Trace("socket proxy already done; exiting")
		return nil
	default:
	}

	// this pile needs to respect consul tls, etc.
	destAddr := s.consulConfig.Addr
	if destAddr == "" {
		return errors.New("failed to detect consul address")
	}

	hostHTTPSockPath := filepath.Join(s.allocDir.AllocDir, allocdir.AllocHTTPSocket)

	listener, err := openUnixSocket(hostHTTPSockPath, httpProtocolName)
	if err != nil {
		return errors.Wrap(err, "failed to start proxy")
	}

	// do stuff
	_ = listener

	return nil
}
