// Package tunnel is the outbound-only agent connection. Agents dial the
// management plane over mutual TLS; the management plane then opens streams
// back through that connection to reach the cluster's kube-apiserver. It wraps
// the vendored remotedialer (the only importer of it, see ADR 0002).
package tunnel

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"

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

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.rd.ServeHTTP(w, r) }

// authorize turns the TLS-verified client certificate into the session key.
// The key embeds the tenant, so Dial can only ever reach a tenant's own agents.
func (s *Server) authorize(r *http.Request) (string, bool, error) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		return "", false, errors.New("no verified client certificate")
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

// Dialer returns a dialer that reaches addresses through the named agent of
// tenant t. It fails (at dial time) if that agent is not connected.
func (s *Server) Dialer(t tenancy.ID, agent string) Dialer {
	return s.rd.Dialer(sessionKey(t, agent))
}

// ServerTLSConfig requires and verifies client certificates against root.
func ServerTLSConfig(cert tls.Certificate, root *x509.Certificate) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
}
