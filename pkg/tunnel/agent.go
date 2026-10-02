package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dpuig/sextant/third_party/remotedialer"
)

// Backoff bounds. The cap keeps worst-case reconnect well inside the 30 s gate
// once the management plane is reachable again.
const (
	minBackoff = 100 * time.Millisecond
	maxBackoff = 5 * time.Second
	// A session must stay up this long before the backoff resets. Otherwise a
	// server that accepts the connection and drops it immediately would make
	// every agent reconnect at the minimum interval forever.
	healthyAfter = 10 * time.Second
)

// SetLiveness configures dead-peer detection for all tunnel sessions in this
// process (agent ping interval and how long to wait for traffic).
func SetLiveness(pingInterval, wait time.Duration) { remotedialer.SetLiveness(pingInterval, wait) }

// AgentConfig configures an Agent.
type AgentConfig struct {
	// URL is the management plane's tunnel endpoint, e.g. wss://mp.example.com/connect.
	URL string
	// Roots verifies the management plane's server certificate.
	Roots *x509.CertPool
	// Certificate supplies the client certificate on every (re)connect, so a
	// rotated certificate is picked up without restarting the agent.
	Certificate func() (*tls.Certificate, error)
	// AllowedAddr is the only address the management plane may ask this agent
	// to dial (the local kube-apiserver, e.g. "10.96.0.1:443"). Everything else
	// is refused: a compromised management plane must not become a scanner of
	// the customer network.
	AllowedAddr string
	Log         *slog.Logger
}

// Agent keeps one outbound tunnel alive.
type Agent struct {
	cfg       AgentConfig
	connected atomic.Bool
}

// NewAgent validates cfg and returns an Agent.
func NewAgent(cfg AgentConfig) (*Agent, error) {
	switch {
	case cfg.URL == "":
		return nil, errors.New("tunnel: URL is required")
	case cfg.Roots == nil:
		return nil, errors.New("tunnel: Roots is required")
	case cfg.Certificate == nil:
		return nil, errors.New("tunnel: Certificate is required")
	case cfg.AllowedAddr == "":
		return nil, errors.New("tunnel: AllowedAddr is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Agent{cfg: cfg}, nil
}

// Connected reports whether a tunnel session is currently established.
func (a *Agent) Connected() bool { return a.connected.Load() }

// Run connects and reconnects until ctx is cancelled, with jittered
// exponential backoff that resets once a session is established.
func (a *Agent) Run(ctx context.Context) error {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    a.cfg.Roots,
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return a.cfg.Certificate()
			},
		},
	}
	allow := func(proto, addr string) bool { return proto == "tcp" && addr == a.cfg.AllowedAddr }

	backoff := minBackoff
	for ctx.Err() == nil {
		// onConnect runs on its own goroutine and can be scheduled after a very
		// short session has already ended; the mutex stops it from flipping
		// connected back to true afterwards.
		var (
			mu          sync.Mutex
			ended       bool
			established bool
		)
		began := time.Now()
		err := remotedialer.ConnectToProxy(ctx, a.cfg.URL, http.Header{}, allow, dialer,
			func(context.Context, *remotedialer.Session) error {
				mu.Lock()
				defer mu.Unlock()
				if !ended {
					established = true
					a.connected.Store(true)
				}
				return nil
			})
		mu.Lock()
		ended = true
		a.connected.Store(false)
		wasEstablished := established
		mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if wasEstablished && time.Since(began) >= healthyAfter {
			backoff = minBackoff
		}
		a.cfg.Log.Warn("tunnel disconnected; retrying", "err", err, "in", backoff)
		sleep := backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)+1)) // jitter: [b/2, b]
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
		backoff = min(backoff*2, maxBackoff)
	}
	return ctx.Err()
}
