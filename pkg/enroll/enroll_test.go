package enroll_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dpuig/sextant/pkg/enroll"
	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/pki/localfile"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
)

// fakeTokens is a single-use token store keyed by token string.
type fakeTokens struct {
	mu sync.Mutex
	m  map[string][2]string // token -> tenant, agent
}

func (f *fakeTokens) RedeemWith(_ context.Context, tok string, fn func(tenancy.ID, string) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[tok]
	if !ok {
		return storage.ErrInvalidToken
	}
	id, _ := tenancy.ParseID(v[0])
	if err := fn(id, v[1]); err != nil {
		return err // token stays: mirrors the transactional rollback
	}
	delete(f.m, tok)
	return nil
}

type revoked map[string]bool

func (r revoked) Revoked(_ tenancy.ID, agent, _ string) bool { return r[agent] }

type fixture struct {
	h      http.Handler
	root   *x509.Certificate
	tokens *fakeTokens
	rev    revoked
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := localfile.Generate("test-root")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{root: root.Certificate(), tokens: &fakeTokens{m: map[string][2]string{"tok-1": {"acme", "c1"}}}, rev: revoked{}}
	cas := pki.NewTenantCAs(pki.NewAuthority(root), 7*24*time.Hour)
	f.h = enroll.New(f.tokens, cas, f.rev, nil)
	return f
}

func csrB64(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der), key
}

func post(h http.Handler, path, body string, state *tls.ConnectionState) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
	req.TLS = state
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type issued struct {
	Certificate []string  `json:"certificate"`
	NotAfter    time.Time `json:"notAfter"`
}

func parseIssued(t *testing.T, rec *httptest.ResponseRecorder) (leaf, inter *x509.Certificate) {
	t.Helper()
	var out issued
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Certificate) != 2 {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	der0, _ := base64.StdEncoding.DecodeString(out.Certificate[0])
	der1, _ := base64.StdEncoding.DecodeString(out.Certificate[1])
	leaf, err := x509.ParseCertificate(der0)
	if err != nil {
		t.Fatal(err)
	}
	inter, err = x509.ParseCertificate(der1)
	if err != nil {
		t.Fatal(err)
	}
	return leaf, inter
}

func (f *fixture) verify(t *testing.T, leaf, inter *x509.Certificate) {
	t.Helper()
	roots, pool := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(f.root)
	pool.AddCert(inter)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("issued chain does not verify: %v", err)
	}
}

func enrollBody(token, csr string) string {
	b, _ := json.Marshal(map[string]string{"token": token, "csr": csr})
	return string(b)
}

func TestEnroll_IssuesVerifiableChainForTokenIdentity(t *testing.T) {
	f := newFixture(t)
	csr, _ := csrB64(t)
	rec := post(f.h, "/v1/enroll", enrollBody("tok-1", csr), nil) // no client cert needed
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	leaf, inter := parseIssued(t, rec)
	f.verify(t, leaf, inter)
	tid, agent, err := pki.ParseAgentIdentity(leaf)
	if err != nil || tid.String() != "acme" || agent != "c1" {
		t.Fatalf("identity = %v %q %v", tid, agent, err)
	}
	if ttl := time.Until(leaf.NotAfter); ttl > 24*time.Hour || ttl < 23*time.Hour {
		t.Fatalf("leaf ttl = %v, want ~24h", ttl)
	}
}

func TestEnroll_TokenIsSingleUse(t *testing.T) {
	f := newFixture(t)
	csr, _ := csrB64(t)
	if rec := post(f.h, "/v1/enroll", enrollBody("tok-1", csr), nil); rec.Code != 200 {
		t.Fatalf("first = %d", rec.Code)
	}
	if rec := post(f.h, "/v1/enroll", enrollBody("tok-1", csr), nil); rec.Code != 401 {
		t.Fatalf("second = %d, want 401", rec.Code)
	}
}

func TestEnroll_BadTokenDoesNotLeakWhy(t *testing.T) {
	f := newFixture(t)
	csr, _ := csrB64(t)
	rec := post(f.h, "/v1/enroll", enrollBody("nope", csr), nil)
	if rec.Code != 401 || strings.Contains(strings.ToLower(rec.Body.String()), "expired") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestEnroll_RejectsBadInput(t *testing.T) {
	f := newFixture(t)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsaCSR, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, rsaKey)
	goodCSR, _ := csrB64(t)
	tests := map[string]string{
		"not json":      `{`,
		"unknown field": `{"token":"tok-1","csr":"` + goodCSR + `","extra":1}`,
		"bad base64":    enrollBody("tok-1", "!!!"),
		"garbage csr":   enrollBody("tok-1", base64.StdEncoding.EncodeToString([]byte("garbage"))),
		"rsa csr":       enrollBody("tok-1", base64.StdEncoding.EncodeToString(rsaCSR)),
		"missing csr":   `{"token":"tok-1"}`,
		"oversize":      `{"token":"tok-1","csr":"` + strings.Repeat("A", 1<<17) + `"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rec := post(f.h, "/v1/enroll", body, nil)
			if rec.Code != 400 {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
			}
		})
	}
	// None of the failed attempts above may have burned the token unless the CSR
	// was already accepted: a rejected request must not consume it.
	if rec := post(f.h, "/v1/enroll", enrollBody("tok-1", goodCSR), nil); rec.Code != 200 {
		t.Fatalf("token consumed by malformed requests: %d %s", rec.Code, rec.Body)
	}
}

func TestEnroll_WrongMethod(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest("GET", "/v1/enroll", nil)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET = %d", rec.Code)
	}
}

// --- renew ---

func (f *fixture) enrolled(t *testing.T) (*x509.Certificate, *x509.Certificate) {
	t.Helper()
	csr, _ := csrB64(t)
	rec := post(f.h, "/v1/enroll", enrollBody("tok-1", csr), nil)
	return parseIssued(t, rec)
}

func connState(leaf, inter *x509.Certificate) *tls.ConnectionState {
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf, inter}}}
}

func renewBody(csr string) string {
	b, _ := json.Marshal(map[string]string{"csr": csr})
	return string(b)
}

func TestRenew_RequiresVerifiedClientCertificate(t *testing.T) {
	f := newFixture(t)
	csr, _ := csrB64(t)
	if rec := post(f.h, "/v1/renew", renewBody(csr), nil); rec.Code != 401 {
		t.Fatalf("no TLS = %d, want 401", rec.Code)
	}
	if rec := post(f.h, "/v1/renew", renewBody(csr), &tls.ConnectionState{}); rec.Code != 401 {
		t.Fatalf("no verified chain = %d, want 401", rec.Code)
	}
}

func TestRenew_IssuesNewCertForSameIdentity(t *testing.T) {
	f := newFixture(t)
	leaf, inter := f.enrolled(t)
	csr, _ := csrB64(t)
	rec := post(f.h, "/v1/renew", renewBody(csr), connState(leaf, inter))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	nl, ni := parseIssued(t, rec)
	f.verify(t, nl, ni)
	tid, agent, _ := pki.ParseAgentIdentity(nl)
	if tid.String() != "acme" || agent != "c1" {
		t.Fatalf("renewed identity changed: %v %q", tid, agent)
	}
	if nl.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
		t.Fatal("renewal reused the serial number")
	}
}

func TestRenew_RevokedAgentIsRefused(t *testing.T) {
	f := newFixture(t)
	leaf, inter := f.enrolled(t)
	f.rev["c1"] = true
	csr, _ := csrB64(t)
	if rec := post(f.h, "/v1/renew", renewBody(csr), connState(leaf, inter)); rec.Code != 403 {
		t.Fatalf("revoked renew = %d, want 403", rec.Code)
	}
}

func TestRenew_NonAgentCertificateIsRefused(t *testing.T) {
	f := newFixture(t)
	csr, _ := csrB64(t)
	if rec := post(f.h, "/v1/renew", renewBody(csr), connState(&x509.Certificate{}, &x509.Certificate{})); rec.Code != 403 {
		t.Fatalf("non-agent cert = %d, want 403", rec.Code)
	}
}

// failingRoot wraps a root and can be switched to fail every signature,
// simulating a KMS/Vault outage.
type failingRoot struct {
	inner *localfile.Root
	fail  atomic.Bool
}

func (r *failingRoot) Certificate() *x509.Certificate { return r.inner.Certificate() }
func (r *failingRoot) Public() crypto.PublicKey       { return r.inner.Public() }
func (r *failingRoot) Sign(rnd io.Reader, d []byte, o crypto.SignerOpts) ([]byte, error) {
	if r.fail.Load() {
		return nil, errors.New("kms unavailable")
	}
	return r.inner.Sign(rnd, d, o)
}

// An issuance failure after the token is validated must not spend the token.
func TestEnroll_IssuanceFailureDoesNotBurnToken(t *testing.T) {
	inner, _ := localfile.Generate("test-root")
	root := &failingRoot{inner: inner}
	tokens := &fakeTokens{m: map[string][2]string{"tok-1": {"acme", "c1"}}}
	h := enroll.New(tokens, pki.NewTenantCAs(pki.NewAuthority(root), time.Hour*24*7), nil, nil)
	csr, _ := csrB64(t)

	root.fail.Store(true)
	if rec := post(h, "/v1/enroll", enrollBody("tok-1", csr), nil); rec.Code != 500 {
		t.Fatalf("during outage = %d, want 500 (%s)", rec.Code, rec.Body)
	}
	root.fail.Store(false)
	if rec := post(h, "/v1/enroll", enrollBody("tok-1", csr), nil); rec.Code != 200 {
		t.Fatalf("retry after outage = %d, want 200 (%s)", rec.Code, rec.Body)
	}
}
