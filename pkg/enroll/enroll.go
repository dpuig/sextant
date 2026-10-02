// Package enroll is how an agent gets its client certificate. Enrollment
// trades a single-use registration token (plus a CSR) for a short-lived
// certificate; renewal trades the current certificate (over mTLS) for a fresh
// one. Identity always comes from the token or the verified current
// certificate, never from the CSR.
package enroll

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
)

const (
	maxBody = 64 << 10
	// CertTTL is the lifetime of issued agent certificates. Agents renew at 50%.
	CertTTL = pki.MaxAgentCertTTL
)

// Redeemer consumes a registration token and runs fn atomically with that
// consumption: if fn fails the token must stay usable. *storage.Tokens
// implements it.
type Redeemer interface {
	RedeemWith(ctx context.Context, token string, fn func(tenant tenancy.ID, agent string) error) error
}

// Revoker mirrors tunnel.Revoker so one implementation serves both.
type Revoker interface {
	Revoked(tenant tenancy.ID, agent string, serial string) bool
}

// Handler serves POST /v1/enroll and POST /v1/renew.
type Handler struct {
	tokens  Redeemer
	cas     *pki.TenantCAs
	revoker Revoker
	log     *slog.Logger
	mux     *http.ServeMux
}

// New returns the enrollment handler. revoker and log may be nil.
func New(tokens Redeemer, cas *pki.TenantCAs, revoker Revoker, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{tokens: tokens, cas: cas, revoker: revoker, log: log, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST /v1/enroll", h.enroll)
	h.mux.HandleFunc("POST /v1/renew", h.renew)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.mux.ServeHTTP(w, r)
}

type enrollRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"` // base64 (std) DER
}

type renewRequest struct {
	CSR string `json:"csr"`
}

type issued struct {
	// Certificate is [leaf, intermediate], each base64 (std) DER.
	Certificate []string  `json:"certificate"`
	NotAfter    time.Time `json:"notAfter"`
}

func (h *Handler) enroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if !decode(w, r, &req) {
		return
	}
	// Validate the CSR before redeeming: a malformed request must not burn the token.
	csr, ok := parseCSR(w, req.CSR)
	if !ok {
		return
	}
	if req.Token == "" {
		httpError(w, http.StatusBadRequest, "token is required")
		return
	}
	// Issue inside the redemption: if issuance fails the token is not spent.
	var out *issued
	ctx := r.Context()
	err := h.tokens.RedeemWith(ctx, req.Token, func(tid tenancy.ID, agent string) error {
		var err error
		out, err = h.issue(ctx, tid, agent, csr)
		return err
	})
	if errors.Is(err, storage.ErrInvalidToken) {
		httpError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	if err != nil {
		h.log.Error("enroll failed", "err", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) renew(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		httpError(w, http.StatusUnauthorized, "client certificate required")
		return
	}
	leaf := r.TLS.VerifiedChains[0][0]
	tid, agent, err := pki.ParseAgentIdentity(leaf)
	if err != nil {
		httpError(w, http.StatusForbidden, "not an agent certificate")
		return
	}
	if h.revoker != nil && h.revoker.Revoked(tid, agent, leaf.SerialNumber.String()) {
		h.log.Warn("renewal refused: revoked", "tenant", tid, "agent", agent)
		httpError(w, http.StatusForbidden, "revoked")
		return
	}
	var req renewRequest
	if !decode(w, r, &req) {
		return
	}
	csr, ok := parseCSR(w, req.CSR)
	if !ok {
		return
	}
	out, err := h.issue(r.Context(), tid, agent, csr)
	if err != nil {
		h.log.Error("renew failed", "tenant", tid, "agent", agent, "err", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) issue(ctx context.Context, tid tenancy.ID, agent string, csr []byte) (*issued, error) {
	ca, err := h.cas.For(ctx, tid)
	if err != nil {
		return nil, fmt.Errorf("tenant CA: %w", err)
	}
	der, err := ca.IssueAgentCert(csr, agent, CertTTL)
	if err != nil {
		return nil, fmt.Errorf("issue: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	h.log.Info("agent certificate issued", "tenant", tid, "agent", agent, "serial", leaf.SerialNumber.String())
	return &issued{
		Certificate: []string{base64.StdEncoding.EncodeToString(der), base64.StdEncoding.EncodeToString(ca.Cert.Raw)},
		NotAfter:    leaf.NotAfter,
	}, nil
}

func parseCSR(w http.ResponseWriter, b64 string) ([]byte, bool) {
	if b64 == "" {
		httpError(w, http.StatusBadRequest, "csr is required")
		return nil, false
	}
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "csr is not base64")
		return nil, false
	}
	req, err := x509.ParseCertificateRequest(der)
	if err != nil || req.CheckSignature() != nil {
		httpError(w, http.StatusBadRequest, "invalid csr")
		return nil, false
	}
	// Reject unusable keys now, before a token is redeemed: failing later would burn it.
	if err := pki.CheckPublicKey(req.PublicKey); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return der, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid body")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid body")
		return false
	}
	return true
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"code": code, "message": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
