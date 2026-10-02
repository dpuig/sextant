// Spike: measure remotedialer against the Phase 0 exit gates.
//
//	G2: agent reconnects within 30 s after management-plane restart
//	G3: added p95 latency < 50 ms
//
// Opt-in (slow, and the silent-partition run trips a data race inside the
// vendored library, see docs/adr/0002):
//
//	SEXTANT_SPIKE=1 go test ./spikes/tunnel -v -count=1
package tunnel_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rancher/remotedialer"
	"github.com/sirupsen/logrus"
)

const clientKey = "agent-1"

type mgmt struct {
	addr string
	srv  *remotedialer.Server
	http *http.Server
	ln   *trackListener
}

// trackListener remembers accepted conns; http.Server.Close does not close
// hijacked (websocket) conns, which a real process exit would.
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

func (l *trackListener) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		_ = c.Close()
	}
}

func startMgmt(t *testing.T, addr string) *mgmt {
	t.Helper()
	rd := remotedialer.New(func(r *http.Request) (string, bool, error) { return clientKey, true, nil }, remotedialer.DefaultErrorWriter)
	mux := http.NewServeMux()
	mux.Handle("/connect", rd)
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ { // port may linger briefly after a restart
		if ln, err = net.Listen("tcp", addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	tl := &trackListener{Listener: ln}
	s := &http.Server{Handler: mux}
	go func() { _ = s.Serve(tl) }()
	return &mgmt{addr: ln.Addr().String(), srv: rd, http: s, ln: tl}
}

func (m *mgmt) kill() { _ = m.http.Close(); m.ln.closeAll() }

// agentLoop is what the real agent would do: reconnect with jittered backoff.
func agentLoop(ctx context.Context, url string) {
	var reset atomic.Bool
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		_ = remotedialer.ConnectToProxy(ctx, url, nil, func(string, string) bool { return true }, &websocket.Dialer{HandshakeTimeout: 5 * time.Second},
			func(context.Context, *remotedialer.Session) error {
				reset.Store(true) // an established session resets the backoff
				return nil
			})
		if reset.Swap(false) {
			backoff = 100 * time.Millisecond
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func percentile(d []time.Duration, p float64) time.Duration {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(float64(len(d)-1)*p)]
}

func measure(c *http.Client, url string, n int) []time.Duration {
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		resp, err := c.Get(url)
		if err != nil {
			panic(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		out = append(out, time.Since(t0))
	}
	return out
}

func waitSession(m *mgmt, within time.Duration) (time.Duration, bool) {
	t0 := time.Now()
	for time.Since(t0) < within {
		if m.srv.HasSession(clientKey) {
			return time.Since(t0), true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return within, false
}

func requireSpike(t *testing.T) {
	t.Helper()
	if os.Getenv("SEXTANT_SPIKE") == "" {
		t.Skip("set SEXTANT_SPIKE=1 to run tunnel spike")
	}
}

func TestSpike(t *testing.T) {
	requireSpike(t)
	logrus.SetLevel(logrus.FatalLevel)
	// "kube-apiserver": a small JSON response (~2 KB, like a pod list page).
	body := make([]byte, 2048)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer target.Close()

	m := startMgmt(t, "127.0.0.1:0")
	url := "ws://" + m.addr + "/connect"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agentLoop(ctx, url)
	if d, ok := waitSession(m, 10*time.Second); !ok {
		t.Fatal("agent never connected")
	} else {
		t.Logf("initial connect: %v", d.Round(time.Millisecond))
	}

	const n = 2000
	direct := &http.Client{}
	tunnelKeepAlive := &http.Client{Transport: &http.Transport{DialContext: remotedialerDialer(m)}}
	tunnelNewConn := &http.Client{Transport: &http.Transport{DialContext: remotedialerDialer(m), DisableKeepAlives: true}}
	directNewConn := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	report := func(name string, d []time.Duration) {
		t.Logf("%-28s p50=%-8v p95=%-8v p99=%-8v", name, percentile(d, .5).Round(time.Microsecond), percentile(d, .95).Round(time.Microsecond), percentile(d, .99).Round(time.Microsecond))
	}
	measure(direct, target.URL, 100) // warm up
	measure(tunnelKeepAlive, target.URL, 100)
	dd, tk := measure(direct, target.URL, n), measure(tunnelKeepAlive, target.URL, n)
	dn, tn := measure(directNewConn, target.URL, n), measure(tunnelNewConn, target.URL, n)
	report("direct keepalive", dd)
	report("tunnel keepalive", tk)
	report("direct new-conn", dn)
	report("tunnel new-conn", tn)
	t.Logf("added p95: keepalive=%v new-conn=%v (gate: <50ms)",
		(percentile(tk, .95) - percentile(dd, .95)).Round(time.Microsecond),
		(percentile(tn, .95) - percentile(dn, .95)).Round(time.Microsecond))

	// Concurrency: 64 parallel clients.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []time.Duration
	t0 := time.Now()
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := measure(tunnelKeepAlive, target.URL, 100)
			mu.Lock()
			all = append(all, d...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	report("tunnel x64 concurrent", all)
	t.Logf("throughput: %.0f req/s", float64(len(all))/time.Since(t0).Seconds())

	// Reconnect: kill management plane, restart on the same address.
	var results []time.Duration
	for i := 0; i < 10; i++ {
		addr := m.addr
		m.kill()
		time.Sleep(200 * time.Millisecond) // outage
		m = startMgmt(t, addr)
		d, ok := waitSession(m, 30*time.Second)
		if !ok {
			t.Fatalf("run %d: agent did not reconnect within 30s", i)
		}
		results = append(results, d)
	}
	sort.Slice(results, func(i, j int) bool { return results[i] < results[j] })
	t.Logf("reconnect after 200ms outage (10 runs): min=%v median=%v max=%v (gate: <30s)",
		results[0].Round(time.Millisecond), results[5].Round(time.Millisecond), results[9].Round(time.Millisecond))

	// After reconnect, traffic flows again.
	c := &http.Client{Transport: &http.Transport{DialContext: remotedialerDialer(m)}, Timeout: 5 * time.Second}
	if resp, err := c.Get(target.URL); err != nil {
		t.Fatalf("request after reconnect: %v", err)
	} else {
		_ = resp.Body.Close()
		t.Logf("request after reconnect: %s", resp.Status)
	}
	_ = fmt.Sprint()
}

func remotedialerDialer(m *mgmt) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return m.srv.Dialer(clientKey)(ctx, network, addr)
	}
}

// proxy is a TCP forwarder whose target can be switched, and which can
// blackhole traffic silently (no FIN/RST), simulating an LB failover or a NAT
// entry expiring.
type proxy struct {
	ln     net.Listener
	mu     sync.Mutex
	target string
	conns  []net.Conn
	black  map[net.Conn]bool
}

func startProxy(t *testing.T, target string) *proxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{ln: ln, target: target, black: map[net.Conn]bool{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			tgt := p.target
			p.mu.Unlock()
			up, err := net.Dial("tcp", tgt)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go p.pipe(c, up)
			go p.pipe(up, c)
		}
	}()
	return p
}

func (p *proxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		drop := p.black[src] || p.black[dst]
		p.mu.Unlock()
		if drop {
			continue // swallow silently
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

// failover silently blackholes every existing connection and points new ones at target.
func (p *proxy) failover(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		p.black[c] = true
	}
	p.target = target
}

func TestSpikeSilentPartition(t *testing.T) {
	requireSpike(t)
	logrus.SetLevel(logrus.FatalLevel)
	a := startMgmt(t, "127.0.0.1:0")
	b := startMgmt(t, "127.0.0.1:0")
	p := startProxy(t, a.addr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agentLoop(ctx, "ws://"+p.ln.Addr().String()+"/connect")
	if _, ok := waitSession(a, 10*time.Second); !ok {
		t.Fatal("agent never connected to A")
	}
	t0 := time.Now()
	p.failover(b.addr) // A goes silent; B is healthy
	d, ok := waitSession(b, 3*time.Minute)
	t.Logf("silent partition -> agent on new mgmt: %v (ok=%v, gate: <30s)", time.Since(t0).Round(time.Second), ok)
	_ = d
	if !ok {
		t.Fatal("agent never recovered")
	}
}
