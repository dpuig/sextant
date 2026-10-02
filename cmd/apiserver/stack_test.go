package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
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

	"github.com/dpuig/sextant/pkg/agent"
	"github.com/dpuig/sextant/pkg/pki/localfile"
	"github.com/dpuig/sextant/pkg/server"
	"github.com/dpuig/sextant/pkg/storage/storagetest"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
)

const devToken = "0123456789abcdef-dev"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func serverCert(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mp.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, leaf
}

type fullStack struct {
	t      *testing.T
	mp     *httptest.Server
	roots  *x509.CertPool
	kube   *httptest.Server
	kubeCA string
	kubeTk string
	seen   chan http.Header
}

func newFullStack(t *testing.T) *fullStack {
	t.Helper()
	tunnel.SetLiveness(100*time.Millisecond, 600*time.Millisecond)
	t.Cleanup(func() { tunnel.SetLiveness(5*time.Second, 15*time.Second) })

	pool := storagetest.NewPool(t)
	root, err := localfile.Generate("test-root")
	if err != nil {
		t.Fatal(err)
	}
	acme, _ := tenancy.ParseID("acme")
	sa, _ := server.NewStaticAuth(devToken, acme)
	handler, stop := buildHandler(context.Background(), pool, sa, sa, root, quiet)
	t.Cleanup(stop)

	cert, leaf := serverCert(t)
	mp := httptest.NewUnstartedServer(handler)
	mp.TLS = tunnel.ServerTLSConfig(cert, root.Certificate())
	mp.StartTLS()
	t.Cleanup(mp.Close)
	roots := x509.NewCertPool()
	roots.AddCert(leaf)

	s := &fullStack{t: t, mp: mp, roots: roots, seen: make(chan http.Header, 8)}
	s.kube = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.seen <- r.Header.Clone()
		_, _ = io.WriteString(w, `{"kind":"PodList","path":"`+r.URL.Path+`"}`)
	}))
	t.Cleanup(s.kube.Close)
	dir := t.TempDir()
	s.kubeCA = filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(s.kubeCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.kube.Certificate().Raw}), 0o600)
	s.kubeTk = filepath.Join(dir, "token")
	_ = os.WriteFile(s.kubeTk, []byte("agent-sa-token"), 0o600)
	return s
}

// api calls the management plane's REST API as the dev tenant.
func (s *fullStack) api(method, path, body string) (int, map[string]any) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.mp.URL+"/apis/sextant.andean.io/v1alpha1/organizations/acme/"+path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+devToken)
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: s.roots, MinVersion: tls.VersionTLS13}}, Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func (s *fullStack) startAgent(stateDir, token string) (*tunnel.Agent, func()) {
	s.t.Helper()
	mgr := agent.NewManager(&agent.Client{BaseURL: s.mp.URL, Roots: s.roots}, agent.FileStore{Dir: stateDir}, token, quiet)
	if err := mgr.Init(context.Background()); err != nil {
		s.t.Fatalf("agent init: %v", err)
	}
	u, _ := url.Parse(s.kube.URL)
	kube, err := agent.NewKubeProxy(u, s.kubeCA, s.kubeTk, quiet)
	if err != nil {
		s.t.Fatal(err)
	}
	a, err := tunnel.NewAgent(tunnel.AgentConfig{
		URL: "wss" + strings.TrimPrefix(s.mp.URL, "https") + "/connect", Roots: s.roots,
		Certificate: mgr.Certificate, AllowedAddr: tunnel.KubeAPIAddr, Handler: kube, Log: quiet,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = a.Run(ctx) }()
	stop := func() { cancel(); wg.Wait() }
	s.t.Cleanup(stop)
	return a, stop
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *fullStack) connected() bool {
	_, m := s.api("GET", "clusters/c1", "")
	st, _ := m["status"].(map[string]any)
	return st["connected"] == true
}

// The Phase 0 flow, in process: create a Cluster, mint a token, enroll an
// agent with it, see the Cluster go connected, reach the cluster's
// kube-apiserver through the proxy route, and watch it go disconnected.
func TestFullStack_ClusterLifecycle(t *testing.T) {
	s := newFullStack(t)
	if code, _ := s.api("POST", "clusters", `{"metadata":{"name":"c1"},"spec":{"environment":"prod"}}`); code != 201 {
		t.Fatalf("create cluster = %d", code)
	}
	if got := s.connected(); got {
		t.Fatal("new cluster should not be connected")
	}
	code, tok := s.api("POST", "clusters/c1/registration-tokens", "")
	if code != 201 {
		t.Fatalf("mint token = %d", code)
	}
	token := tok["token"].(string)

	// Before any agent: the proxy answers 503, not a hang or a 500.
	if code, _ := s.api("GET", "clusters/c1/proxy/api/v1/pods", ""); code != 503 {
		t.Fatalf("proxy with no agent = %d, want 503", code)
	}

	state := filepath.Join(t.TempDir(), "state")
	_, stop := s.startAgent(state, token)
	eventually(t, "Cluster.status.connected", s.connected)

	code, body := s.api("GET", "clusters/c1/proxy/api/v1/namespaces/default/pods", "")
	if code != 200 || body["kind"] != "PodList" || body["path"] != "/api/v1/namespaces/default/pods" {
		t.Fatalf("proxy = %d %v", code, body)
	}
	hdr := <-s.seen
	if hdr.Get("Authorization") != "Bearer agent-sa-token" {
		t.Fatalf("cluster saw Authorization %q; want the agent's service-account token only", hdr.Get("Authorization"))
	}

	_, m := s.api("GET", "clusters/c1", "")
	if m["status"].(map[string]any)["lastSeen"] == nil {
		t.Fatal("status.lastSeen not set")
	}

	stop()
	eventually(t, "Cluster.status.connected=false", func() bool { return !s.connected() })
	if code, _ := s.api("GET", "clusters/c1/proxy/api/v1/pods", ""); code != 503 {
		t.Fatalf("proxy after disconnect = %d, want 503", code)
	}

	// The token is spent: a second agent cannot enroll with it.
	mgr := agent.NewManager(&agent.Client{BaseURL: s.mp.URL, Roots: s.roots}, agent.FileStore{Dir: filepath.Join(t.TempDir(), "other")}, token, quiet)
	if err := mgr.Init(context.Background()); err == nil {
		t.Fatal("registration token was accepted twice")
	}
}

func TestBuildHandler_WithoutPKIHasNoTunnelOrEnrollment(t *testing.T) {
	pool := storagetest.NewPool(t)
	acme, _ := tenancy.ParseID("acme")
	sa, _ := server.NewStaticAuth(devToken, acme)
	h, stop := buildHandler(context.Background(), pool, sa, sa, nil, quiet)
	defer stop()
	for _, path := range []string{"/connect", "/v1/enroll", "/v1/renew"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
		if rec.Code != 404 {
			t.Fatalf("%s = %d without a PKI root, want 404", path, rec.Code)
		}
	}
}

func TestPKIInit_WritesRootAndRefusesToOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pki")
	if err := pkiCmd([]string{"init", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := localfile.Load(filepath.Join(dir, "root.crt"), filepath.Join(dir, "root.key")); err != nil {
		t.Fatalf("generated root does not load: %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "root.key"))
	if err := pkiCmd([]string{"init", "--dir", dir}); err == nil {
		t.Fatal("second init overwrote an existing root")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "root.key"))
	if !bytes.Equal(before, after) {
		t.Fatal("root key changed")
	}
}
