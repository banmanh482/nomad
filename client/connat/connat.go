package connat

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/pkg/errors"
)

const defaultTimeoutHTTP = 10 * time.Second

type Config struct {
	SocketPath string
	BindTo     string
}

func (c *Config) String() string {
	return fmt.Sprintf("path: %s, bind: %s", c.SocketPath, c.BindTo)
}

type ConNat interface {
	Start(*Config) error
	Stop() error
}

func New(logger hclog.Logger) ConNat {
	logger.Info("connat.New")
	return &conNat{
		logger: logger,
	}
}

type conNat struct {
	logger hclog.Logger

	lock  sync.Mutex
	proxy *Proxy
}

func (cn *conNat) Start(c *Config) error {
	cn.logger.Info("cn.conNat.Start")

	cn.lock.Lock()
	defer cn.lock.Unlock()

	if cn.proxy != nil {
		return errors.New("failed to start connat because it is already started")
	}

	cn.proxy = NewProxy(c.SocketPath, c.BindTo, cn.logger)
	cn.proxy.Open()

	cn.logger.Info("cn.conNat.Start is complete")

	return nil
}

func (cn *conNat) Stop() error {
	cn.logger.Info("cn.conNat.Stop")

	if cn.proxy == nil {
		return errors.New("failed to stop connat because it is not running")
	}

	cn.proxy.Close()
	cn.proxy = nil

	cn.logger.Info("cn.ConNat.Stop is complete")

	return nil
}

type Proxy struct {
	logger     hclog.Logger
	socketPath string
	bindTo     string
	stopC      chan os.Signal
}

func NewProxy(socketPath, bindTo string, logger hclog.Logger) *Proxy {
	logger.Info("connat.NewProxy, bindTo:", bindTo, "socket:", socketPath)

	return &Proxy{
		logger:     logger,
		socketPath: socketPath,
		bindTo:     bindTo,
	}
}

func (p *Proxy) Open() {
	p.logger.Info("! open proxy")

	p.stopC = make(chan os.Signal, 1)
	signal.Notify(p.stopC, syscall.SIGTERM|syscall.SIGINT)

	server := &http.Server{
		Addr:              p.bindTo,
		Handler:           p.handler(client(p.socketPath)),
		ReadTimeout:       defaultTimeoutHTTP,
		ReadHeaderTimeout: defaultTimeoutHTTP,
		WriteTimeout:      defaultTimeoutHTTP,
	}

	go func(s *http.Server) {
		if err := s.ListenAndServe(); err != nil {
			os.Exit(3)
		}
	}(server)

	go func(s *http.Server) {
		select {
		case <-p.stopC:
			if err := server.Close(); err != nil {
				p.logger.Error("failed to close server", "error", err)
			}
			p.logger.Info("proxy got stop signal and closed")
		}
	}(server)

	p.logger.Info("! done with open proxy, running in background")
}

func (p *Proxy) Close() {
	p.stopC <- syscall.SIGINT
}

func (p *Proxy) handler(client *http.Client) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {
		p.logger.Warn("handle request", "method", r.Method, "path", r.URL.EscapedPath())

		socketRequest, err := relayRequest(r)
		if err != nil {
			p.logger.Error("failed to create relay request", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		response, err := client.Do(socketRequest)
		if err != nil {
			p.logger.Error("failed to execute request", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if response.StatusCode >= 400 {
			p.logger.Error("failed to perform request", "code", response.StatusCode, "status", response.Status)
			w.WriteHeader(response.StatusCode)
			return
		}

		bs, err := ioutil.ReadAll(response.Body)
		if err != nil {
			p.logger.Error("failed to read request", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		setHeaders(w, response.Header)
		if _, err = w.Write(bs); err != nil {
			p.logger.Error("failed to write response", "error", err)
		}
	}
}

func setHeaders(w http.ResponseWriter, original http.Header) {
	for key, values := range original {
		duplicate := make([]string, len(values))
		copy(duplicate, values)
		w.Header()[key] = duplicate
	}
}

func relayRequest(original *http.Request) (*http.Request, error) {
	body, err := ioutil.ReadAll(original.Body)
	if err != nil {
		return nil, errors.Wrap(err, "unable to read original request body")
	}

	request := original.Clone(context.TODO())
	request.Body = ioutil.NopCloser(bytes.NewReader(body))
	request.Header.Set("X-Forwarded-For", original.RemoteAddr)
	request.Header.Set("User-Agent", "Nomad connat plugin")
	request.RequestURI = ""
	request.URL = &url.URL{
		Scheme:     "http",
		Opaque:     "",
		User:       nil,
		Host:       "unix",
		Path:       original.URL.Path,
		RawPath:    "",
		ForceQuery: false,
		RawQuery:   original.URL.Query().Encode(),
		Fragment:   "",
	}

	return request, nil
}

func client(socket string) *http.Client {
	return &http.Client{
		Timeout: defaultTimeoutHTTP,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socket)
			},
		},
	}
}
