// Package server exposes the registry over HTTP with Kubernetes-style paths:
//
//	/apis/sextant.andean.io/v1alpha1/organizations/{org}/{plural}[/{name}[/status]]
//
// The tenant is in every path. A request is authenticated, then authorized for
// (tenant, verb, kind), and only then does it reach the registry; any failure
// before that point never touches storage.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dpuig/sextant/pkg/apis/v1alpha1"
	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
)

const (
	maxBody  = 1 << 20 // 1 MiB
	basePath = "/apis/" + v1alpha1.GroupName + "/" + v1alpha1.Version + "/organizations/{org}/"
)

// Principal is an authenticated caller.
type Principal struct{ Subject string }

// Authenticator identifies the caller or returns an error (-> 401).
type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
}

// Authorizer decides whether p may perform verb (get, list, create, update,
// update-status, delete) on kind within tenant. Implementations must default
// to deny.
type Authorizer interface {
	Authorize(p Principal, tenant tenancy.ID, verb, kind string) bool
}

// TokenIssuer mints single-use agent registration tokens (*storage.Tokens).
type TokenIssuer interface {
	Create(ctx context.Context, agent string, ttl time.Duration) (string, error)
}

// Agents reaches clusters through their agents' tunnels (*tunnel.Server).
// The agent for a Cluster is the one whose name equals the Cluster's name.
type Agents interface {
	HasAgent(t tenancy.ID, agent string) bool
	Dialer(t tenancy.ID, agent string) tunnel.Dialer
}

// Option enables optional routes.
type Option func(*Handler)

// WithTokens enables POST .../clusters/{name}/registration-tokens.
func WithTokens(t TokenIssuer) Option { return func(h *Handler) { h.tokens = t } }

// WithAgents enables ANY .../clusters/{name}/proxy/{path...}.
func WithAgents(a Agents) Option { return func(h *Handler) { h.agents = a } }

// Handler serves the API.
type Handler struct {
	reg    *registry.Registry
	authn  Authenticator
	authz  Authorizer
	log    *slog.Logger
	kinds  map[string]v1alpha1.KindInfo // by plural
	tokens TokenIssuer
	agents Agents

	proxies sync.Map // "tenant/agent" -> *httputil.ReverseProxy (cluster_routes.go)
}

// New returns the API handler. log may be nil.
func New(reg *registry.Registry, authn Authenticator, authz Authorizer, log *slog.Logger, opts ...Option) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{reg: reg, authn: authn, authz: authz, log: log, kinds: map[string]v1alpha1.KindInfo{}}
	for _, o := range opts {
		o(h)
	}
	for _, k := range v1alpha1.Kinds() {
		h.kinds[k.Plural] = k
	}
	mux := http.NewServeMux()
	clusters := h.kinds["clusters"]
	if h.tokens != nil {
		mux.HandleFunc("POST "+basePath+"clusters/{name}/registration-tokens", h.serveFixed("create-registration-token", clusters, h.createToken))
	}
	if h.agents != nil {
		mux.HandleFunc(basePath+"clusters/{name}/proxy/{rest...}", h.serveFixed("proxy", clusters, h.proxy))
	}
	mux.HandleFunc("GET "+basePath+"{plural}", h.serve("list", h.list))
	mux.HandleFunc("POST "+basePath+"{plural}", h.serve("create", h.create))
	mux.HandleFunc("GET "+basePath+"{plural}/{name}", h.serve("get", h.get))
	mux.HandleFunc("PUT "+basePath+"{plural}/{name}", h.serve("update", h.update))
	mux.HandleFunc("PUT "+basePath+"{plural}/{name}/status", h.serve("update-status", h.updateStatus))
	mux.HandleFunc("DELETE "+basePath+"{plural}/{name}", h.serve("delete", h.delete))
	return mux
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, kind v1alpha1.KindInfo) error

// serve wraps a collection operation, resolving the kind from the {plural} path value.
func (h *Handler) serve(verb string, fn handlerFunc) http.HandlerFunc {
	return h.serveWith(verb, func(r *http.Request) (v1alpha1.KindInfo, bool) {
		k, ok := h.kinds[r.PathValue("plural")]
		return k, ok
	}, fn)
}

// serveFixed wraps an operation on a fixed kind (custom sub-routes).
func (h *Handler) serveFixed(verb string, kind v1alpha1.KindInfo, fn handlerFunc) http.HandlerFunc {
	return h.serveWith(verb, func(*http.Request) (v1alpha1.KindInfo, bool) { return kind, true }, fn)
}

// serveWith applies authn, tenant parsing, authz and error mapping.
func (h *Handler) serveWith(verb string, kindOf func(*http.Request) (v1alpha1.KindInfo, bool), fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, err := h.authn.Authenticate(r)
		if err != nil {
			writeStatus(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		tid, err := tenancy.ParseID(r.PathValue("org"))
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "invalid organization")
			return
		}
		kind, ok := kindOf(r)
		if !ok {
			writeStatus(w, http.StatusNotFound, "unknown resource")
			return
		}
		// Same response for "no such tenant" and "not yours": no existence oracle.
		if !h.authz.Authorize(p, tid, verb, kind.Kind) {
			writeStatus(w, http.StatusForbidden, "forbidden")
			return
		}
		r = r.WithContext(tenancy.WithTenant(r.Context(), tid))
		if err := fn(w, r, kind); err != nil {
			h.writeError(w, err)
		}
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	items, err := h.reg.List(r.Context(), k.Kind)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": v1alpha1.GroupName + "/" + v1alpha1.Version,
		"kind":       k.Kind + "List",
		"items":      items,
	})
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	obj, err := h.reg.Get(r.Context(), k.Kind, r.PathValue("name"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, obj)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	obj, err := decodeBody(w, r, k, "")
	if err != nil {
		return err
	}
	out, err := h.reg.Create(r.Context(), obj)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	obj, err := decodeBody(w, r, k, r.PathValue("name"))
	if err != nil {
		return err
	}
	out, err := h.reg.Update(r.Context(), obj)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	obj, err := decodeBody(w, r, k, r.PathValue("name"))
	if err != nil {
		return err
	}
	out, err := h.reg.UpdateStatus(r.Context(), obj)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo) error {
	if err := h.reg.Delete(r.Context(), k.Kind, r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// decodeBody reads a size-limited, strictly-typed body. pathName, when set,
// must match metadata.name.
func decodeBody(w http.ResponseWriter, r *http.Request, k v1alpha1.KindInfo, pathName string) (v1alpha1.Resource, error) {
	obj := k.New()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(obj); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, &registry.ValidationError{Msg: "request body too large"}
		}
		return nil, &registry.ValidationError{Msg: "invalid body: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &registry.ValidationError{Msg: "invalid body: trailing data"}
	}
	if gvk := obj.GetObjectKind().GroupVersionKind(); gvk.Kind != "" && gvk.Kind != k.Kind {
		return nil, &registry.ValidationError{Msg: "body kind " + gvk.Kind + " does not match " + k.Kind}
	}
	if pathName != "" && obj.GetName() != pathName {
		return nil, &registry.ValidationError{Msg: "metadata.name does not match path"}
	}
	return obj, nil
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	var ve *registry.ValidationError
	switch {
	case errors.As(err, &ve):
		writeStatus(w, http.StatusUnprocessableEntity, ve.Msg)
	case errors.Is(err, storage.ErrNotFound):
		writeStatus(w, http.StatusNotFound, "not found")
	case errors.Is(err, storage.ErrAlreadyExists):
		writeStatus(w, http.StatusConflict, "already exists")
	case errors.Is(err, storage.ErrConflict):
		writeStatus(w, http.StatusConflict, "resourceVersion conflict")
	default:
		h.log.Error("request failed", "err", err) // detail stays server-side
		writeStatus(w, http.StatusInternalServerError, "internal error")
	}
}

func writeStatus(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"kind": "Status", "code": code, "message": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
