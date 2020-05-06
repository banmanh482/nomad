package conmon

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// Agent serves one or more roles in an effort to manufacture a seamless
// integration with Consul Connect Native applications. May run as any of
//   - HTTP <-> unix socket MITM Proxy
//   - HTTP <-> HTTPS MITM Proxy
//   - DNS recursor
type Agent struct {
	bind string
	port int

	lock       sync.Mutex
	httpServer *http.Server
}

func NewAgent() *Agent {
	fmt.Println("SH NewAgent @ 127.0.0.1:8500")
	return &Agent{
		bind: "127.0.0.1",
		port: 8500,
	}
}

func (a *Agent) Open() error {
	a.lock.Lock()
	defer a.lock.Unlock()

	fmt.Println("SH Agent.Open()")

	if a.httpServer != nil {
		return errors.New("conmon agent already started")
	}

	go func(address string) {
		a.httpServer = &http.Server{
			Addr:    address,
			Handler: a.http(),
		}
		fmt.Println("SH Agent.Open going to listen and serve")
		if err := a.httpServer.ListenAndServe(); err != nil {
			// probably something else pretending to be consul or DNS
			return
		}
	}(fmt.Sprintf("%s:%d", a.bind, a.port))

	return nil
}

func (a *Agent) Close() error {
	a.lock.Lock()
	defer a.lock.Unlock()

	fmt.Println("SH Agent.Close()")

	if err := a.httpServer.Close(); err != nil {
		return err
	}

	return nil
}

func (a *Agent) http() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("Agent HTTP endpoint hit from:", r.RemoteAddr)
	})
}
