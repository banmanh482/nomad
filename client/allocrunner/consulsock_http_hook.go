package allocrunner

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/go-cleanhttp"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/allocdir"
	"github.com/hashicorp/nomad/client/allocrunner/interfaces"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/nomad/structs/config"
	"github.com/pkg/errors"
)

const (
	httpProtocolName = "HTTP"

	httpConsulSocketHookName = "consul_http_socket"
)

type httpConsulSocketHook struct {
	logger hclog.Logger
	alloc  *structs.Allocation
	proxy  *httpProxy

	// lock synchronizes proxy for concurrent Prerun, Update, Postrun actions.
	lock sync.Mutex
}

func newHTTPConsulSocketHook(logger hclog.Logger, alloc *structs.Allocation, allocDir *allocdir.AllocDir, config *config.ConsulConfig) *httpConsulSocketHook {
	return &httpConsulSocketHook{
		alloc:  alloc,
		logger: logger.Named(httpConsulSocketHookName),
		proxy:  newHTTPProxy(logger, allocDir, config),
	}
}

func (h *httpConsulSocketHook) Name() string {
	return httpConsulSocketHookName
}

func (h *httpConsulSocketHook) shouldRun() bool {
	tg := h.alloc.Job.LookupTaskGroup(h.alloc.TaskGroup)
	for _, service := range tg.Services {
		if service.Connect.IsNative() {
			return true
		}
	}
	return false
}

func (h *httpConsulSocketHook) Prerun() error {
	h.lock.Lock()
	defer h.lock.Unlock()

	shouldRun := h.shouldRun()
	h.logger.Warn("Prerun", "shouldRun", shouldRun)
	if !shouldRun {
		return nil
	}

	return h.proxy.run(h.alloc)
}

func (h *httpConsulSocketHook) Update(upReq *interfaces.RunnerUpdateRequest) error {
	h.lock.Lock()
	defer h.lock.Unlock()

	h.alloc = upReq.Alloc
	shouldRun := h.shouldRun()
	h.logger.Warn("Update", "shouldRun", shouldRun)
	if !shouldRun {
		return nil
	}

	return h.proxy.run(h.alloc)
}

func (h *httpConsulSocketHook) Postrun() error {
	h.lock.Lock()
	defer h.lock.Unlock()

	if err := h.proxy.stop(); err != nil {
		// todo: actually stop the server
		h.logger.Error("failed to stop Consul HTTP socket proxy", "error", err)
	}
	return nil
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

func (p *httpProxy) run(alloc *structs.Allocation) error {
	p.logger.Warn("run", "alloc", alloc.Name)

	// Only run once.
	if p.runOnce {
		p.logger.Warn("run already running")
		return nil
	}

	// Only run once, never restart.
	select {
	case <-p.doneC:
		p.logger.Warn("socket proxy already shutdown; exiting")
		return nil
	case <-p.ctx.Done():
		p.logger.Warn("socket proxy already done; exiting")
		return nil
	default:
	}

	hostHTTPSockPath := filepath.Join(p.allocDir.AllocDir, allocdir.AllocHTTPSocket)
	p.logger.Warn("will open socker for http proxy", "path", hostHTTPSockPath)

	listener, err := openUnixSocket(hostHTTPSockPath, httpProtocolName)
	if err != nil {
		return errors.Wrap(err, "failed to start proxy")
	}

	s := p.server(listener)
	_ = s

	return nil
}

func (p *httpProxy) stop() error {
	// todo: stop the server
	return nil
}

func (p *httpProxy) server(listener net.Listener) *http.Server {
	s := &http.Server{
		Handler: http.HandlerFunc(p.handler),
	}
	go func() {
		_ = s.Serve(listener)
	}()
	return s
}

func (p *httpProxy) handler(w http.ResponseWriter, r *http.Request) {
	p.logger.Warn("http proxy handler", "method", r.Method, "remote", r.RemoteAddr)

	rdir, err := p.request(r)
	if err != nil {
		p.logger.Error("failed to create proxy request from socket to Consul", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// clean this up
	client := cleanhttp.DefaultClient()
	client.Timeout = 10 * time.Second
	response, err := client.Do(rdir)
	if err != nil {
		p.logger.Error("failed to proxy request from socket to Consul", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(w, response.Body); err != nil {
		p.logger.Error("failed to copy request response from socket to Consul", "error", err)
		return
	}

	p.logger.Warn("http proxy handler complete")
}

func (p *httpProxy) request(original *http.Request) (*http.Request, error) {
	body, err := ioutil.ReadAll(original.Body)
	if err != nil {
		return nil, errors.Wrap(err, "unable to read original request")
	}
	request := original.Clone(context.TODO())
	request.Body = ioutil.NopCloser(bytes.NewReader(body))
	request.Header.Set("X-Forwarded-For", original.Header.Get("X-Forwarded-For"))
	request.Header.Set("User-Agent", original.Header.Get("User-Agent"))
	request.RequestURI = ""
	request.URL = &url.URL{
		Scheme:   "http",
		Host:     p.consulConfig.Addr,
		Path:     original.URL.Path,
		RawQuery: original.URL.Query().Encode(),
	}
	p.logger.Warn("request url", "url", request.URL.String())

	return request, nil
}
