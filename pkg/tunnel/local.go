package tunnel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// localServer serves HTTP on in-memory connections: when the management
// plane dials the agent's virtual address, the other end of a net.Pipe is
// handed to an http.Server. No TCP port is ever opened on the agent.
type localServer struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
	srv   *http.Server
}

func newLocalServer(h http.Handler) *localServer {
	s := &localServer{
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
		srv:   &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second},
	}
	go func() { _ = s.srv.Serve(s) }()
	return s
}

// dial returns the client end of a new in-memory connection.
func (s *localServer) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case s.conns <- server:
		return client, nil
	case <-s.done:
	case <-ctx.Done():
	}
	_ = client.Close()
	_ = server.Close()
	return nil, errors.New("tunnel: local server unavailable")
}

// signalDone is idempotent. It must not call srv.Close: http.Server.Close
// calls Listener.Close (below), which calls signalDone, and re-entering a
// sync.Once from inside itself deadlocks.
func (s *localServer) signalDone() { s.once.Do(func() { close(s.done) }) }

func (s *localServer) close() {
	s.signalDone()
	_ = s.srv.Close()
}

// net.Listener for http.Server.Serve.
func (s *localServer) Accept() (net.Conn, error) {
	select {
	case c := <-s.conns:
		return c, nil
	case <-s.done:
		return nil, net.ErrClosed
	}
}
func (s *localServer) Close() error   { s.signalDone(); return nil }
func (s *localServer) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "sextant-agent-local" }
