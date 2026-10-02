package agent_test

import (
	"context"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dpuig/sextant/pkg/agent"
	"github.com/dpuig/sextant/pkg/enroll"
	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/pki/localfile"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
)

// An in-process management plane (one mTLS listener serving /connect and
// /v1/*) plus a real agent stack, tunnelling to a fake kube-apiserver.
type stack struct {
	t         *testing.T
	mp        *httptest.Server
	srv       *tunnel.Server
	roots     *x509.CertPool
	tokens    *oneShotTokens
	kubeSeen  chan *http.Request
	kubeURL   *url.URL
	kubeCA    string
	kubeToken string
	stateDir  string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	tunnel.SetLiveness(100*time.Millisecond, 600*time.Millisecond)
	t.Cleanup(func() { tunnel.SetLiveness(5*time.Second, 15*time.Second) })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	root, _ := localfile.Generate("test-root")
	cas := pki.NewTenantCAs(pki.NewAuthority(root), 7*24*time.Hour)
	s := &stack{t: t, tokens: &oneShotTokens{ok: map[string]bool{"good-token": true}}, kubeSeen: make(chan *http.Request, 16), stateDir: filepath.Join(t.TempDir(), "state")}
	s.srv = tunnel.NewServer(nil, quiet)

	mux := http.NewServeMux()
	mux.Handle("/connect", s.srv)
	mux.Handle("/v1/", enroll.New(s.tokens, cas, nil, quiet))
	s.mp = httptest.NewUnstartedServer(mux)
	s.mp.StartTLS()
	cfg := tunnel.ServerTLSConfig(s.mp.TLS.Certificates[0], root.Certificate())
	s.mp.Close()
	s.mp = httptest.NewUnstartedServer(mux)
	s.mp.TLS = cfg
	s.mp.StartTLS()
	t.Cleanup(s.mp.Close)
	s.roots = x509.NewCertPool()
	s.roots.AddCert(s.mp.Certificate())

	kube := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.kubeSeen <- r.Clone(r.Context())
		_, _ = io.WriteString(w, `{"kind":"PodList","items":[]}`)
	}))
	t.Cleanup(kube.Close)
	s.kubeURL, _ = url.Parse(kube.URL)
	dir := t.TempDir()
	s.kubeCA = writeFile(t, dir, "ca.crt", pemOf(kube))
	s.kubeToken = writeFile(t, dir, "token", "agent-sa-token")
	return s
}

// startAgent runs the same wiring as cmd/agent and returns when it is stopped.
func (s *stack) startAgent(token string) (*tunnel.Agent, func()) {
	s.t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := agent.NewManager(&agent.Client{BaseURL: s.mp.URL, Roots: s.roots}, agent.FileStore{Dir: s.stateDir}, token, quiet)
	if err := mgr.Init(context.Background()); err != nil {
		s.t.Fatal(err)
	}
	kube, err := agent.NewKubeProxy(s.kubeURL, s.kubeCA, s.kubeToken, quiet)
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

func (s *stack) waitConnected(a *tunnel.Agent) {
	s.t.Helper()
	acme, _ := tenancy.ParseID("acme")
	deadline := time.Now().Add(10 * time.Second)
	for !a.Connected() || !s.srv.HasAgent(acme, "cluster-1") {
		if time.Now().After(deadline) {
			s.t.Fatal("agent did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *stack) kubectlGet(path string) (string, error) {
	acme, _ := tenancy.ParseID("acme")
	c := &http.Client{Transport: &http.Transport{DialContext: s.srv.Dialer(acme, "cluster-1")}, Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + tunnel.KubeAPIAddr + path)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestE2E_EnrollTunnelAndReachKubeAPIAsServiceAccount(t *testing.T) {
	s := newStack(t)
	a, _ := s.startAgent("good-token")
	s.waitConnected(a)

	body, err := s.kubectlGet("/api/v1/namespaces/default/pods")
	if err != nil || !strings.Contains(body, "PodList") {
		t.Fatalf("body=%q err=%v", body, err)
	}
	seen := <-s.kubeSeen
	if seen.URL.Path != "/api/v1/namespaces/default/pods" || seen.Header.Get("Authorization") != "Bearer agent-sa-token" {
		t.Fatalf("kube-apiserver saw %s with Authorization %q", seen.URL.Path, seen.Header.Get("Authorization"))
	}
}

func TestE2E_RestartReconnectsFromStoredCredentialsWithoutToken(t *testing.T) {
	s := newStack(t)
	a, stop := s.startAgent("good-token")
	s.waitConnected(a)
	stop()

	// The single-use token is spent; a restarted agent must come back on its own.
	a2, _ := s.startAgent("")
	s.waitConnected(a2)
	if _, err := s.kubectlGet("/api/v1/pods"); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_ManagementPlaneCannotReachAnythingButTheKubeAPI(t *testing.T) {
	s := newStack(t)
	a, _ := s.startAgent("good-token")
	s.waitConnected(a)
	acme, _ := tenancy.ParseID("acme")
	d := s.srv.Dialer(acme, "cluster-1")
	// remotedialer returns a conn before the agent has answered, so refusal is
	// observed as "no data comes back", not as a dial error.
	reachable := func(addr string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := d(ctx, "tcp", addr)
		if err != nil {
			return false
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte("GET / HTTP/1.0\r\n\r\n")); err != nil {
			return false
		}
		buf := make([]byte, 1)
		n, err := conn.Read(buf)
		return n > 0 && err == nil
	}
	if !reachable(tunnel.KubeAPIAddr) {
		t.Fatal("control: the allowed address should answer")
	}
	for _, addr := range []string{"169.254.169.254:80", "127.0.0.1:22", s.kubeURL.Host, "kube-apiserver.sextant.internal:443"} {
		if reachable(addr) {
			t.Fatalf("management plane reached %s through the agent", addr)
		}
	}
}
