// Package tunnel is the outbound-only agent connection. Agents dial the
// management plane over mutual TLS; the management plane then opens streams
// back through that connection to reach the cluster's kube-apiserver. It wraps
// the vendored remotedialer (the only importer of it, see ADR 0002).
package tunnel

import (
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/third_party/remotedialer"
)

// Dialer opens a connection to address on the agent's network.
type Dialer = remotedialer.Dialer

// Revoker lets the management plane refuse a still-valid certificate (e.g. a
// decommissioned or compromised agent). A nil Revoker revokes nothing.
type Revoker interface {
	Revoked(tenant tenancy.ID, agent string, serial string) bool
}

// Server accepts agent connections. Mount it as an http.Handler on a server
// whose TLS config comes from ServerTLSConfig.
type Server struct {
	rd      *remotedialer.Server
	revoker Revoker
	log     *slog.Logger
}

// NewServer returns a Server. revoker and log may be nil.
func NewServer(revoker Revoker, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{revoker: revoker, log: log}
	s.rd = remotedialer.New(s.authorize, func(w http.ResponseWriter, _ *http.Request, code int, _ error) {
		http.Error(w, http.StatusText(code), code)
	})
	return s
}

// OnChange registers fn to be told when an agent gains its first session or
// loses its last. fn runs on the connection's goroutine and must not block.
// Call it before serving.
func (s *Server) OnChange(fn func(tenant tenancy.ID, agent string, connected bool)) {
	s.rd.OnSessionChange = func(key string, connected bool) {
		tenant, agent, ok := strings.Cut(key, "/")
		tid, err := tenancy.ParseID(tenant)
		if !ok || err != nil {
			return // not a key we minted
		}
		fn(tid, agent, connected)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.rd.ServeHTTP(w, r) }

// authorize turns the TLS-verified client certificate into the session key.
// The key embeds the tenant, so Dial can only ever reach a tenant's own agents.
func (s *Server) authorize(r *http.Request) (string, bool, error) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		return "", false, nil // unauthenticated -> 401 (an error would map to 400)
	}
	leaf := r.TLS.VerifiedChains[0][0]
	tid, agent, err := pki.ParseAgentIdentity(leaf)
	if err != nil {
		s.log.Warn("agent rejected: bad identity", "err", err)
		return "", false, nil
	}
	if s.revoker != nil && s.revoker.Revoked(tid, agent, leaf.SerialNumber.String()) {
		s.log.Warn("agent rejected: revoked", "tenant", tid, "agent", agent)
		return "", false, nil
	}
	return sessionKey(tid, agent), true, nil
}

func sessionKey(t tenancy.ID, agent string) string { return t.String() + "/" + agent }

// HasAgent reports whether the tenant's agent currently has a live session.
func (s *Server) HasAgent(t tenancy.ID, agent string) bool {
	return s.rd.HasSession(sessionKey(t, agent))
}

// Disconnect drops the agent's live sessions and returns how many it closed.
// Call it when revoking a credential: Revoker is consulted only at connect
// time, so a revoked agent that is already connected stays up until dropped.
func (s *Server) Disconnect(t tenancy.ID, agent string) int {
	return s.rd.Disconnect(sessionKey(t, agent))
}

// DisconnectAll drops every live agent session and returns how many it closed.
// Call it when shutting down: agents then reconnect to a replacement
// immediately, instead of staying attached to a pod that is about to exit
// while the Service already routes new connections elsewhere.
func (s *Server) DisconnectAll() int {
	n := 0
	for _, key := range s.rd.ListClients() {
		n += s.rd.Disconnect(key)
	}
	return n
}

// Dialer returns a dialer that reaches addresses through the named agent of
// tenant t. It fails (at dial time) if that agent is not connected.
func (s *Server) Dialer(t tenancy.ID, agent string) Dialer {
	return s.rd.Dialer(sessionKey(t, agent))
}

// ServerTLSConfig verifies any client certificate presented against root, but
// does not demand one at the TLS layer: the same listener also serves the
// token-gated /v1/enroll, which agents call before they have a certificate.
// The tunnel route itself refuses connections without a verified certificate
// (see authorize), and /v1/renew does the same.
func ServerTLSConfig(cert tls.Certificate, root *x509.Certificate) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    pool,
	}
}

// KubeAPIAddr is the virtual address the management plane dials to reach an
// agent's kube-apiserver proxy. It is plain HTTP: the tunnel itself is
// already mutually authenticated and encrypted.
const KubeAPIAddr = "kube-apiserver.sextant.internal:80"
