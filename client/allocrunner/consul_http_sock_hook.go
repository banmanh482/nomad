package allocrunner

import (
	"context"
	"os"
	"sync"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/nomad/structs/config"
	"github.com/pkg/errors"
)

const (
	consulHTTPSocketHookName = "consul_http_socket"
)

type consulHTTPSockHook struct {
	logger hclog.Logger
	alloc  *structs.Allocation
	proxy  *httpSocketProxy

	// lock synchronizes proxy which may be mutated and read concurrently via
	// Prerun, Update, and Postrun.
	lock sync.Mutex
}

func newConsulHTTPSocketHook(logger hclog.Logger, alloc *structs.Allocation, allocDir *allocdir.AllocDir, config *config.ConsulConfig) *consulHTTPSockHook {
	return &consulHTTPSockHook{
		alloc:  alloc,
		proxy:  newHTTPSocketProxy(logger, allocDir, config),
		logger: logger.Named(consulHTTPSocketHookName),
	}
}

func (*consulHTTPSockHook) Name() string {
	return consulHTTPSocketHookName
}

// shouldRun returns true if the alloc contains at least one connect native
// task and has a network configured in bridge mode
func (h *consulHTTPSockHook) shouldRun() bool {
	// todo(nuth)
	//  lookup the task group, ensure net[0] is bridge
	//  ensure at least one service is connect native

	return false
}

func (h *consulHTTPSockHook) Prerun() error {
	h.lock.Lock()
	defer h.lock.Unlock()

	// todo(nuth)
	//  exit early if this hook should not run
	//  otherwise start the proxy

	return nil
}

func (h *consulHTTPSockHook) Update(req *interfaces.RunnerUpdateRequest) error {
	h.lock.Lock()
	defer h.lock.Unlock()

	// todo(nuth)
	//  similar to Prerun, exit early if this hook should not run
	//  otherwise start the proxy
	//  but with update, we have a new alloc to set on the hook

	return nil
}

func (h *consulHTTPSockHook) Postrun() error {
	h.lock.Lock()
	defer h.lock.Unlock()

	// todo(nuth)
	//  stop the proxy

	return nil
}

type httpSocketProxy struct {
	logger   hclog.Logger
	allocDir *allocdir.AllocDir
	config   *config.ConsulConfig

	ctx     context.Context
	cancel  func()
	doneCh  chan struct{}
	runOnce bool
}

func newHTTPSocketProxy(logger hclog.Logger, allocDir *allocdir.AllocDir, config *config.ConsulConfig) *httpSocketProxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &httpSocketProxy{
		logger:   logger,
		allocDir: allocDir,
		config:   config,
		ctx:      ctx,
		cancel:   cancel,
		doneCh:   make(chan struct{}),
	}
}

// run the httpSocketProxy for the given allocation.
//
// Assumes locking done by the calling alloc runner.
func (p *httpSocketProxy) run(alloc *structs.Allocation) error {
	// todo(nuth)
	//  run the proxy

	return nil
}

func (p *httpSocketProxy) stop() error {
	// todo(nuth)
	//  stop theproxy

	return nil
}

func maybeRemoveOldSocket(socketPath string) error {
	_, err := os.Stat(socketPath)
	if err == nil {
		if err = os.Remove(socketPath); err != nil {
			return errors.Wrap(err, "unable to remove existing unix socket")
		}
	}
	return nil
}
