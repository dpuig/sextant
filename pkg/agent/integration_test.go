package agent_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dpuig/sextant/pkg/agent"
	"github.com/dpuig/sextant/pkg/enroll"
	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/pki/localfile"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
)

type oneShotTokens struct {
	mu sync.Mutex
	ok map[string]bool
}

func (o *oneShotTokens) RedeemWith(_ context.Context, tok string, fn func(tenancy.ID, string) error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.ok[tok] {
		return storage.ErrInvalidToken
	}
	id, _ := tenancy.ParseID("acme")
	if err := fn(id, "cluster-1"); err != nil {
		return err
	}
	delete(o.ok, tok)
	return nil
}

// mgmtPlane serves the real enroll handler behind real mTLS (verify-if-given).
func mgmtPlane(t *testing.T) (*agent.Client, *x509.Certificate) {
	t.Helper()
	root, err := localfile.Generate("test-root")
	if err != nil {
		t.Fatal(err)
	}
	cas := pki.NewTenantCAs(pki.NewAuthority(root), 7*24*time.Hour)
	h := enroll.New(&oneShotTokens{ok: map[string]bool{"good-token": true}}, cas, nil, nil)
	ts := httptest.NewUnstartedServer(h)
	ts.StartTLS()
	// Reconfigure with mTLS keeping httptest's server certificate.
	cfg := tunnel.ServerTLSConfig(ts.TLS.Certificates[0], root.Certificate())
	ts.Close()
	ts = httptest.NewUnstartedServer(h)
	ts.TLS = cfg
	ts.StartTLS()
	t.Cleanup(ts.Close)

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return &agent.Client{BaseURL: ts.URL, Roots: pool}, root.Certificate()
}

func TestClient_EnrollThenRenewOverMTLS(t *testing.T) {
	c, rootCert := mgmtPlane(t)
	ctx := context.Background()
	first, err := c.Enroll(ctx, "good-token")
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := first.Leaf()
	tid, name, err := pki.ParseAgentIdentity(leaf)
	if err != nil || tid.String() != "acme" || name != "cluster-1" {
		t.Fatalf("identity = %v %q %v", tid, name, err)
	}
	roots, inter := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(rootCert)
	ic, _ := x509.ParseCertificate(first.Chain[1])
	inter.AddCert(ic)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("enrolled chain invalid: %v", err)
	}

	second, err := c.Renew(ctx, first)
	if err != nil {
		t.Fatalf("renew over real mTLS: %v", err)
	}
	if second.Key.Equal(first.Key) {
		t.Fatal("renewal reused the private key")
	}
	l2, _ := second.Leaf()
	if l2.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
		t.Fatal("renewal reused the serial")
	}
}

func TestClient_TokenCannotBeReusedAndErrorsDoNotEchoIt(t *testing.T) {
	c, _ := mgmtPlane(t)
	if _, err := c.Enroll(context.Background(), "good-token"); err != nil {
		t.Fatal(err)
	}
	_, err := c.Enroll(context.Background(), "good-token")
	if err == nil {
		t.Fatal("token reuse accepted")
	}
	if strings.Contains(err.Error(), "good-token") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestClient_RenewWithoutValidCertificateFails(t *testing.T) {
	c, _ := mgmtPlane(t)
	// Credentials from a different PKI: the handshake must be refused.
	other, _ := mgmtPlane(t)
	foreign, _ := other.Enroll(context.Background(), "good-token")
	if _, err := c.Renew(context.Background(), foreign); err == nil {
		t.Fatal("renewal with a foreign certificate succeeded")
	}
}

func TestClient_RefusesUntrustedServer(t *testing.T) {
	c, _ := mgmtPlane(t)
	c.Roots = x509.NewCertPool() // trusts nothing
	if _, err := c.Enroll(context.Background(), "good-token"); err == nil {
		t.Fatal("enrolled against an untrusted management plane (token would have been sent)")
	}
}

// --- kube proxy ---

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func pemOf(ts *httptest.Server) string {
	return string(pemEncode(ts.Certificate().Raw))
}

func newProxy(t *testing.T, upstream http.Handler) (http.Handler, string) {
	t.Helper()
	up := httptest.NewTLSServer(upstream)
	t.Cleanup(up.Close)
	dir := t.TempDir()
	tokenFile := writeFile(t, dir, "token", "sa-token-1\n")
	ca := writeFile(t, dir, "ca.crt", pemOf(up))
	u, _ := url.Parse(up.URL)
	h, err := agent.NewKubeProxy(u, ca, tokenFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, tokenFile
}

func TestKubeProxy_UsesServiceAccountTokenAndDiscardsCallerAuthorization(t *testing.T) {
	var gotAuth string
	h, tokenFile := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok " + r.URL.Path))
	}))

	req := httptest.NewRequest("GET", "/api/v1/pods", nil)
	req.Header.Set("Authorization", "Bearer evil-admin-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "ok /api/v1/pods" || gotAuth != "Bearer sa-token-1" {
		t.Fatalf("body=%q auth=%q", rec.Body, gotAuth)
	}

	// Token rotation is picked up without a restart.
	_ = os.WriteFile(tokenFile, []byte("sa-token-2"), 0o600)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if gotAuth != "Bearer sa-token-2" {
		t.Fatalf("rotated token not used: %q", gotAuth)
	}
}

func TestKubeProxy_StripsImpersonationHeaders(t *testing.T) {
	var seen http.Header
	h, _ := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() }))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Impersonate-User", "system:admin")
	req.Header.Set("Impersonate-Group", "system:masters")
	req.Header.Set("impersonate-extra-scopes", "all")
	req.Header.Set("X-Keep", "yes")
	h.ServeHTTP(httptest.NewRecorder(), req)
	for name := range seen {
		if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
			t.Fatalf("upstream received %s", name)
		}
	}
	if seen.Get("X-Keep") != "yes" {
		t.Fatal("ordinary headers must still be forwarded")
	}
}

func TestKubeProxy_DoesNotSendCallerTokenWhenOwnTokenUnreadable(t *testing.T) {
	var gotAuth = "unset"
	h, tokenFile := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotAuth = r.Header.Get("Authorization") }))
	_ = os.Remove(tokenFile)
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer evil")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotAuth != "" {
		t.Fatalf("upstream saw Authorization %q; caller's token must never be forwarded", gotAuth)
	}
}

func TestKubeProxy_StreamsResponsesImmediately(t *testing.T) {
	release := make(chan struct{})
	h, _ := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("event-1\n"))
		w.(http.Flusher).Flush()
		<-release // a watch stays open; the second event comes later
		_, _ = w.Write([]byte("event-2\n"))
	}))
	front := httptest.NewServer(h)
	defer front.Close()

	resp, err := http.Get(front.URL + "/watch")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	line := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(resp.Body).ReadString('\n'); line <- s }()
	select {
	case s := <-line:
		if s != "event-1\n" {
			t.Fatalf("first line = %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first event was buffered until the stream ended")
	}
	close(release)
}

func TestKubeProxy_PassesWebSocketUpgrades(t *testing.T) {
	up := websocket.Upgrader{}
	h, _ := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		mt, msg, err := c.ReadMessage()
		if err == nil {
			_ = c.WriteMessage(mt, append([]byte("echo:"), msg...))
		}
	}))
	front := httptest.NewServer(h)
	defer front.Close()

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/exec", nil)
	if err != nil {
		t.Fatalf("upgrade through proxy: %v", err)
	}
	defer func() { _ = c.Close() }()
	_ = c.WriteMessage(websocket.TextMessage, []byte("hi"))
	_, msg, err := c.ReadMessage()
	if err != nil || string(msg) != "echo:hi" {
		t.Fatalf("msg=%q err=%v", msg, err)
	}
}

func TestKubeProxy_RejectsBadConfigAndUntrustedUpstream(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "ca.crt", "-----BEGIN CERTIFICATE-----\n")
	httpURL, _ := url.Parse("http://x")
	if _, err := agent.NewKubeProxy(httpURL, good, "t", nil); err == nil {
		t.Fatal("plain-http apiserver URL accepted")
	}
	httpsURL, _ := url.Parse("https://x")
	if _, err := agent.NewKubeProxy(httpsURL, good, "t", nil); err == nil {
		t.Fatal("CA file without certificates accepted")
	}
	if _, err := agent.NewKubeProxy(httpsURL, filepath.Join(dir, "missing"), "t", nil); err == nil {
		t.Fatal("missing CA file accepted")
	}

	// An upstream whose certificate the CA does not vouch for gives 502, never
	// data. (httptest's built-in cert is shared by every NewTLSServer, so the
	// impostor gets its own generated certificate.)
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("secret")) }))
	up.TLS = &tls.Config{Certificates: []tls.Certificate{impostorCert(t)}}
	up.StartTLS()
	defer up.Close()
	trusted := httptest.NewTLSServer(http.NotFoundHandler()) // the CA the agent is configured with
	defer trusted.Close()
	caFile := writeFile(t, dir, "trusted-ca.crt", pemOf(trusted))
	u, _ := url.Parse(up.URL)
	h, err := agent.NewKubeProxy(u, caFile, writeFile(t, dir, "tok", "t"), nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body)
	}
}

func impostorCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "impostor"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
