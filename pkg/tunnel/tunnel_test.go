package tunnel_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/pki/localfile"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	t          *testing.T
	root       *localfile.Root
	auth       *pki.Authority
	server     *tunnel.Server
	serverCert tls.Certificate
	mp         *httptest.Server // management plane (TLS, requires client certs)
	tl         *trackListener
	target     *httptest.Server // stands in for the kube-apiserver on the agent's network
	pool       *x509.CertPool   // trusts the management plane's server cert
}

func tid(t *testing.T, s string) tenancy.ID {
	t.Helper()
	id, err := tenancy.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// selfSignedServerCert returns a TLS server certificate valid for 127.0.0.1.
func selfSignedServerCert(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mp.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, cert
}

// trackListener remembers accepted conns. httptest forgets hijacked
// (websocket) conns, so without this a "crash" would leave tunnels open.
type trackListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *trackListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.conns = append(l.conns, c)
		l.mu.Unlock()
	}
	return c, err
}

func (l *trackListener) dropAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		_ = c.Close()
	}
}

func (e *env) startManagementPlane(ln net.Listener) {
	e.t.Helper()
	mp := httptest.NewUnstartedServer(e.server)
	if ln == nil {
		ln = mp.Listener
	} else {
		_ = mp.Listener.Close()
	}
	e.tl = &trackListener{Listener: ln}
	mp.Listener = e.tl
	mp.TLS = tunnel.ServerTLSConfig(e.serverCert, e.root.Certificate())
	mp.StartTLS()
	e.t.Cleanup(mp.Close)
	e.mp = mp
}

func newEnv(t *testing.T, revoker tunnel.Revoker) *env {
	t.Helper()
	tunnel.SetLiveness(100*time.Millisecond, 600*time.Millisecond)
	t.Cleanup(func() { tunnel.SetLiveness(5*time.Second, 15*time.Second) })

	root, err := localfile.Generate("test-root")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, root: root, auth: pki.NewAuthority(root), server: tunnel.NewServer(revoker, quiet)}
	var serverLeaf *x509.Certificate
	e.serverCert, serverLeaf = selfSignedServerCert(t)
	e.pool = x509.NewCertPool()
	e.pool.AddCert(serverLeaf)
	e.startManagementPlane(nil)

	e.target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "kube-apiserver says hi") }))
	t.Cleanup(e.target.Close)
	return e
}

// rootSignedNonAgentCert chains to the trusted root and has ClientAuth, but
// carries no sextant agent identity.
func (e *env) rootSignedNonAgentCert() *tls.Certificate {
	e.t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "not-an-agent"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, e.root.Certificate(), &key.PublicKey, e.root)
	if err != nil {
		e.t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// agentCert returns a client certificate (leaf + intermediate) for tenant/name.
func (e *env) agentCert(tenant, name string, ttl time.Duration) *tls.Certificate {
	e.t.Helper()
	ca, err := e.auth.IssueTenantCA(context.Background(), tid(e.t, tenant), 24*time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.certFrom(ca, name, ttl)
}

func (e *env) certFrom(ca *pki.TenantCA, name string, ttl time.Duration) *tls.Certificate {
	e.t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, key)
	der, err := ca.IssueAgentCert(csr, name, ttl)
	if err != nil {
		e.t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, ca.Cert.Raw}, PrivateKey: key}
}

func (e *env) startAgent(cert func() (*tls.Certificate, error), allowed string) (*tunnel.Agent, context.CancelFunc) {
	e.t.Helper()
	a, err := tunnel.NewAgent(tunnel.AgentConfig{
		URL:         "wss" + strings.TrimPrefix(e.mp.URL, "https") + "/connect",
		Roots:       e.pool,
		Certificate: cert,
		AllowedAddr: allowed,
		Log:         quiet,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Run(ctx); close(done) }()
	e.t.Cleanup(func() { cancel(); <-done })
	return a, cancel
}

func static(c *tls.Certificate) func() (*tls.Certificate, error) {
	return func() (*tls.Certificate, error) { return c, nil }
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func never(t *testing.T, window time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if cond() {
			t.Fatalf("unexpectedly true: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *env) get(d tunnel.Dialer) (string, error) {
	c := &http.Client{Transport: &http.Transport{DialContext: d}, Timeout: 3 * time.Second}
	resp, err := c.Get(e.target.URL)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func targetAddr(e *env) string { return strings.TrimPrefix(e.target.URL, "http://") }

func TestAgentConnects_AndRequestsFlowThroughTunnel(t *testing.T) {
	e := newEnv(t, nil)
	a, _ := e.startAgent(static(e.agentCert("acme", "c1", time.Hour)), targetAddr(e))
	eventually(t, 5*time.Second, "agent session", func() bool { return e.server.HasAgent(tid(t, "acme"), "c1") && a.Connected() })

	body, err := e.get(e.server.Dialer(tid(t, "acme"), "c1"))
	if err != nil || body != "kube-apiserver says hi" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestTunnelsAreTenantScoped(t *testing.T) {
	e := newEnv(t, nil)
	e.startAgent(static(e.agentCert("acme", "c1", time.Hour)), targetAddr(e))
	eventually(t, 5*time.Second, "acme agent", func() bool { return e.server.HasAgent(tid(t, "acme"), "c1") })

	// Same agent name under another tenant is a different (absent) session.
	if e.server.HasAgent(tid(t, "globex"), "c1") {
		t.Fatal("globex sees acme's agent")
	}
	if _, err := e.get(e.server.Dialer(tid(t, "globex"), "c1")); err == nil {
		t.Fatal("dialing acme's agent through globex's key succeeded")
	}
}

func TestSameAgentNameInTwoTenantsAreIndependent(t *testing.T) {
	e := newEnv(t, nil)
	e.startAgent(static(e.agentCert("acme", "c1", time.Hour)), targetAddr(e))
	e.startAgent(static(e.agentCert("globex", "c1", time.Hour)), targetAddr(e))
	eventually(t, 5*time.Second, "both agents", func() bool {
		return e.server.HasAgent(tid(t, "acme"), "c1") && e.server.HasAgent(tid(t, "globex"), "c1")
	})
}

func TestUntrustedCertificateIsRejected(t *testing.T) {
	e := newEnv(t, nil)
	// A perfectly well-formed agent cert, but from a different PKI.
	other := newEnv(t, nil)
	a, _ := e.startAgent(static(other.agentCert("acme", "c1", time.Hour)), targetAddr(e))
	never(t, 1500*time.Millisecond, "agent connected with foreign-PKI cert", func() bool { return a.Connected() || e.server.HasAgent(tid(t, "acme"), "c1") })
}

func TestExpiredCertificateIsRejected(t *testing.T) {
	e := newEnv(t, nil)
	// Issue from a clock two hours in the past so the agent cert is already expired.
	past := pki.NewAuthority(e.root, pki.WithClock(func() time.Time { return time.Now().Add(-2 * time.Hour) }))
	pastCA, err := past.IssueTenantCA(context.Background(), tid(t, "acme"), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := e.startAgent(static(e.certFrom(pastCA, "c1", time.Hour)), targetAddr(e))
	never(t, 1500*time.Millisecond, "agent connected with expired cert", func() bool { return a.Connected() })
}

func TestNonAgentCertificateIsRejected(t *testing.T) {
	e := newEnv(t, nil)
	a, _ := e.startAgent(static(e.rootSignedNonAgentCert()), targetAddr(e))
	never(t, 1500*time.Millisecond, "agent connected without agent identity", func() bool { return a.Connected() })
}

type revokeSet struct {
	mu sync.Mutex
	m  map[string]bool
}

func (r *revokeSet) Revoked(_ tenancy.ID, agent, _ string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[agent]
}

func TestRevokedAgentCannotConnect(t *testing.T) {
	rv := &revokeSet{m: map[string]bool{"c1": true}}
	e := newEnv(t, rv)
	a, _ := e.startAgent(static(e.agentCert("acme", "c1", time.Hour)), targetAddr(e))
	never(t, 1500*time.Millisecond, "revoked agent connected", func() bool { return a.Connected() })

	rv.mu.Lock()
	rv.m["c1"] = false // un-revoke: the agent's retry loop should now succeed
	rv.mu.Unlock()
	eventually(t, 10*time.Second, "agent connects once not revoked", func() bool { return a.Connected() })
}

func TestAgentRefusesToDialAnythingButAllowedAddress(t *testing.T) {
	e := newEnv(t, nil)
	e.startAgent(static(e.agentCert("acme", "c1", time.Hour)), "10.255.255.1:443") // not the target
	eventually(t, 5*time.Second, "agent", func() bool { return e.server.HasAgent(tid(t, "acme"), "c1") })
	if body, err := e.get(e.server.Dialer(tid(t, "acme"), "c1")); err == nil {
		t.Fatalf("management plane reached a non-allowed address through the agent: %q", body)
	}
}

func TestAgentReconnectsAfterManagementPlaneRestart(t *testing.T) {
	e := newEnv(t, nil)
	a, _ := e.startAgent(static(e.agentCert("acme", "c1", time.Hour)), targetAddr(e))
	eventually(t, 5*time.Second, "agent", func() bool { return a.Connected() })

	// Kill the management plane (closing all conns, like a process exit) and
	// bring up a replacement on the same address with a fresh tunnel.Server.
	addr := e.mp.Listener.Addr().String()
	e.tl.dropAll()
	e.mp.Close()
	eventually(t, 5*time.Second, "agent notices", func() bool { return !a.Connected() })

	e.server = tunnel.NewServer(nil, quiet)
	e.startManagementPlane(listenWithRetry(t, addr))

	start := time.Now()
	eventually(t, 10*time.Second, "agent reconnects", func() bool { return e.server.HasAgent(tid(t, "acme"), "c1") })
	t.Logf("reconnected in %v", time.Since(start).Round(time.Millisecond))
	if body, err := e.get(e.server.Dialer(tid(t, "acme"), "c1")); err != nil || body == "" {
		t.Fatalf("request after reconnect: %q %v", body, err)
	}
}

func listenWithRetry(t *testing.T, addr string) net.Listener {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ln, err := net.Listen("tcp", addr); err == nil {
			return ln
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("could not re-listen")
	return nil
}

func TestCertificateProviderIsConsultedOnEveryReconnect(t *testing.T) {
	e := newEnv(t, nil)
	var mu sync.Mutex
	var calls int
	first := e.agentCert("acme", "c1", time.Hour)
	a, _ := e.startAgent(func() (*tls.Certificate, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return first, nil
	}, targetAddr(e))
	eventually(t, 5*time.Second, "connect", func() bool { return a.Connected() })

	e.tl.dropAll() // drop the session; agent must reconnect and ask again
	eventually(t, 10*time.Second, "reconnect", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls >= 2 && a.Connected()
	})
}

func TestNewAgentValidatesConfig(t *testing.T) {
	if _, err := tunnel.NewAgent(tunnel.AgentConfig{}); err == nil {
		t.Fatal("empty config accepted")
	}
	if _, err := tunnel.NewAgent(tunnel.AgentConfig{URL: "wss://x", Roots: x509.NewCertPool(), Certificate: func() (*tls.Certificate, error) { return nil, nil }}); err == nil {
		t.Fatal("missing AllowedAddr accepted")
	}
}

func TestRevokingDropsLiveSessionAndBlocksReconnect(t *testing.T) {
	rv := &revokeSet{m: map[string]bool{}}
	e := newEnv(t, rv)
	a, _ := e.startAgent(static(e.agentCert("acme", "c1", time.Hour)), targetAddr(e))
	eventually(t, 5*time.Second, "connected", func() bool { return a.Connected() })

	rv.mu.Lock()
	rv.m["c1"] = true
	rv.mu.Unlock()
	if n := e.server.Disconnect(tid(t, "acme"), "c1"); n != 1 {
		t.Fatalf("Disconnect = %d, want 1", n)
	}
	eventually(t, 5*time.Second, "agent dropped", func() bool { return !a.Connected() && !e.server.HasAgent(tid(t, "acme"), "c1") })
	never(t, 2*time.Second, "revoked agent reconnected", func() bool { return a.Connected() })
}

// A server that accepts the websocket and immediately closes it must not make
// agents reconnect at the minimum interval forever.
func TestAgentBacksOffAgainstFlappingServer(t *testing.T) {
	e := newEnv(t, nil)
	var mu sync.Mutex
	attempts := 0
	up := websocket.Upgrader{}
	flap := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		attempts++
		mu.Unlock()
		_ = c.Close()
	}))
	flap.TLS = tunnel.ServerTLSConfig(e.serverCert, e.root.Certificate())
	flap.StartTLS()
	t.Cleanup(flap.Close)

	a, err := tunnel.NewAgent(tunnel.AgentConfig{
		URL: "wss" + strings.TrimPrefix(flap.URL, "https") + "/connect", Roots: e.pool,
		Certificate: static(e.agentCert("acme", "c1", time.Hour)), AllowedAddr: targetAddr(e), Log: quiet,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	_ = a.Run(ctx)
	mu.Lock()
	defer mu.Unlock()
	if attempts < 2 || attempts > 12 {
		t.Fatalf("%d attempts in 2.5s; want backoff to keep this between 2 and 12", attempts)
	}
}
