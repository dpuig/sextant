package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/dpuig/sextant/pkg/apis/v1alpha1"
	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
)

const (
	defaultTokenTTL = time.Hour
	maxTokenTTL     = 24 * time.Hour
)

// createToken mints a single-use registration token for the Cluster's agent.
// The token is returned once and never logged.
func (h *Handler) createToken(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	name := r.PathValue("name")
	if _, err := h.reg.Get(r.Context(), k.Kind, name); err != nil {
		return err // 404 if the Cluster does not exist
	}
	ttl := defaultTokenTTL
	if r.ContentLength != 0 {
		var req struct {
			TTLSeconds int `json:"ttlSeconds"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			return &registry.ValidationError{Msg: "invalid body"}
		}
		if req.TTLSeconds != 0 {
			// Range-check the integer before converting: seconds * 1e9 overflows int64.
			if req.TTLSeconds < 1 || req.TTLSeconds > int(maxTokenTTL/time.Second) {
				return &registry.ValidationError{Msg: "ttlSeconds must be between 1 and 86400"}
			}
			ttl = time.Duration(req.TTLSeconds) * time.Second
		}
	}
	tok, err := h.tokens.Create(r.Context(), name, ttl)
	if err != nil {
		return err
	}
	h.log.Info("registration token issued", "cluster", name, "ttl", ttl) // never the token
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "expiresAt": time.Now().Add(ttl).UTC().Format(time.RFC3339)})
	return nil
}

// proxy forwards a request to the Cluster's kube-apiserver through its
// agent's tunnel. kubectl points its server URL at .../clusters/{name}/proxy.
func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	tid, err := tenancy.FromContext(r.Context())
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if _, err := h.reg.Get(r.Context(), k.Kind, name); err != nil {
		return err
	}
	if !h.agents.HasAgent(tid, name) {
		writeStatus(w, http.StatusServiceUnavailable, "cluster agent is not connected")
		return nil
	}
	// Watches and log/exec streams outlive the server-wide timeouts, which stay
	// in force for every other route.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	h.proxyFor(tid, name).ServeHTTP(w, r)
	return nil
}

// proxyFor returns the (cached) reverse proxy for one agent. The dialer
// resolves the live session at dial time, so a cached proxy survives agent
// reconnects.
func (h *Handler) proxyFor(t tenancy.ID, agent string) *httputil.ReverseProxy {
	key := t.String() + "/" + agent
	if p, ok := h.proxies.Load(key); ok {
		return p.(*httputil.ReverseProxy)
	}
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			rest := strings.TrimPrefix(pr.In.PathValue("rest"), "/")
			pr.Out.URL.Scheme = "http" // the tunnel is already mutually authenticated and encrypted
			pr.Out.URL.Host = tunnel.KubeAPIAddr
			pr.Out.URL.Path, pr.Out.URL.RawPath = "/"+rest, ""
			pr.Out.Host = tunnel.KubeAPIAddr
			// The caller's Sextant credential is for us, not for the cluster.
			pr.Out.Header.Del("Authorization")
		},
		Transport:     &http.Transport{DialContext: h.agents.Dialer(t, agent)},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.log.Warn("cluster proxy failed", "tenant", t, "cluster", agent, "err", err)
			writeStatus(w, http.StatusBadGateway, "cluster unreachable")
		},
	}
	actual, _ := h.proxies.LoadOrStore(key, p)
	return actual.(*httputil.ReverseProxy)
}
