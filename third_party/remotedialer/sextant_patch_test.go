package remotedialer

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

func quietLogs(t *testing.T) {
	prev := logrus.GetLevel()
	logrus.SetLevel(logrus.FatalLevel)
	t.Cleanup(func() { logrus.SetLevel(prev) })
}

func newPatchTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	rd := New(func(*http.Request) (string, bool, error) { return "agent", true, nil }, DefaultErrorWriter)
	ts := httptest.NewServer(rd)
	t.Cleanup(ts.Close)
	return rd, ts
}

func wsURL(ts *httptest.Server) string { return "ws" + strings.TrimPrefix(ts.URL, "http") }

// Patch 2: Session.Close raced with startPings. Run with -race.
func TestSessionCloseDoesNotRaceWithStartPings(t *testing.T) {
	quietLogs(t)
	_, ts := newPatchTestServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%7)*time.Millisecond+time.Millisecond)
			defer cancel()
			_ = ConnectToProxy(ctx, wsURL(ts), nil, func(string, string) bool { return true }, &websocket.Dialer{}, nil)
		}(i)
	}
	wg.Wait()
}

// blackholeProxy forwards TCP until blackhole() is called, then silently
// swallows all traffic without closing anything (no FIN/RST).
type blackholeProxy struct {
	ln   net.Listener
	mu   sync.Mutex
	dead bool
}

func newBlackholeProxy(t *testing.T, target string) *blackholeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &blackholeProxy{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			t.Cleanup(func() { _ = c.Close(); _ = up.Close() })
			go p.pipe(c, up)
			go p.pipe(up, c)
		}
	}()
	return p
}

func (p *blackholeProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		dead := p.dead
		p.mu.Unlock()
		if dead {
			continue
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

func (p *blackholeProxy) blackhole() { p.mu.Lock(); p.dead = true; p.mu.Unlock() }

// Patch 1: liveness timing is configurable, and a silent partition is
// detected within roughly the configured wait.
func TestSilentPartitionDetectedWithinConfiguredWait(t *testing.T) {
	quietLogs(t)
	SetLiveness(100*time.Millisecond, 500*time.Millisecond)
	t.Cleanup(func() { SetLiveness(DefaultPingWriteInterval, DefaultPingWaitDuration) })

	rd, ts := newPatchTestServer(t)
	p := newBlackholeProxy(t, strings.TrimPrefix(ts.URL, "http://"))

	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- ConnectToProxy(ctx, "ws://"+p.ln.Addr().String(), nil, func(string, string) bool { return true }, &websocket.Dialer{}, nil)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !rd.HasSession("agent") {
		if time.Now().After(deadline) {
			t.Fatal("agent never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	p.blackhole()
	select {
	case <-done:
		if took := time.Since(start); took > 3*time.Second {
			t.Fatalf("detected after %v, want about 500ms", took)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("silent partition not detected within 10s")
	}
}

func TestDefaultLivenessMeetsReconnectGate(t *testing.T) {
	// Detection (wait) plus agent backoff cap (5s) must stay under the 30s gate.
	if DefaultPingWaitDuration+5*time.Second >= 30*time.Second {
		t.Fatalf("default wait %v leaves no room for reconnect backoff", DefaultPingWaitDuration)
	}
	if DefaultPingWriteInterval*2 >= DefaultPingWaitDuration {
		t.Fatal("ping interval must be well under the wait so one lost ping is tolerated")
	}
}

// Patch 3: connection.err was read and written by concurrent closers
// (tunnel disconnect vs. pipe teardown). Run with -race.
func TestConnectionTunnelCloseIsRaceFree(t *testing.T) {
	quietLogs(t)
	for i := 0; i < 50; i++ {
		c := &connection{session: &Session{}}
		c.backPressure = newBackPressure(c)
		c.buffer = newReadBuffer(1, c.backPressure)
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() { defer wg.Done(); c.doTunnelClose(nil) }()
		}
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.closedErr() }()
		wg.Wait()
	}
}

// Patch 4: Server.Disconnect drops a live session so revocation takes effect
// immediately instead of at the next natural disconnect.
func TestServerDisconnectDropsLiveSession(t *testing.T) {
	quietLogs(t)
	rd, ts := newPatchTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ConnectToProxy(ctx, wsURL(ts), nil, func(string, string) bool { return true }, &websocket.Dialer{}, nil)
	}()
	waitFor(t, "session", func() bool { return rd.HasSession("agent") })

	if n := rd.Disconnect("agent"); n != 1 {
		t.Fatalf("Disconnect returned %d, want 1", n)
	}
	waitFor(t, "server forgets session", func() bool { return !rd.HasSession("agent") })
	select {
	case <-done: // agent noticed the drop
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not notice the disconnect")
	}
	if n := rd.Disconnect("nobody"); n != 0 {
		t.Fatalf("Disconnect of unknown client = %d, want 0", n)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Patch 5: OnSessionChange reports connect and disconnect, in order.
func TestServerOnSessionChange(t *testing.T) {
	quietLogs(t)
	rd, ts := newPatchTestServer(t)
	var mu sync.Mutex
	var events []bool
	rd.OnSessionChange = func(key string, connected bool) {
		if key != "agent" {
			t.Errorf("key = %q", key)
		}
		mu.Lock()
		events = append(events, connected)
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ConnectToProxy(ctx, wsURL(ts), nil, func(string, string) bool { return true }, &websocket.Dialer{}, nil)
	}()
	waitFor(t, "connect event", func() bool { mu.Lock(); defer mu.Unlock(); return len(events) == 1 })
	cancel()
	<-done
	waitFor(t, "disconnect event", func() bool { mu.Lock(); defer mu.Unlock(); return len(events) == 2 })
	mu.Lock()
	defer mu.Unlock()
	if !events[0] || events[1] {
		t.Fatalf("events = %v, want [true false]", events)
	}
}

// A slow "disconnected" delivery must not land after the "connected" of a
// reconnect that happened while it was in flight. Without serialising the
// evaluate-and-deliver step, the stale false overwrites the newer true.
func TestServerOnSessionChange_StaleDisconnectCannotOverwriteReconnect(t *testing.T) {
	quietLogs(t)
	rd, ts := newPatchTestServer(t)
	var mu sync.Mutex
	last := false
	rd.OnSessionChange = func(_ string, connected bool) {
		if !connected {
			time.Sleep(150 * time.Millisecond) // a slow consumer (e.g. a database write)
		}
		mu.Lock()
		last = connected
		mu.Unlock()
	}

	connect := func() (cancel func(), done <-chan struct{}) {
		ctx, c := context.WithCancel(context.Background())
		d := make(chan struct{})
		go func() {
			defer close(d)
			_ = ConnectToProxy(ctx, wsURL(ts), nil, func(string, string) bool { return true }, &websocket.Dialer{}, nil)
		}()
		return c, d
	}

	cancelA, doneA := connect()
	waitFor(t, "A connected", func() bool { mu.Lock(); defer mu.Unlock(); return last })
	cancelA()
	<-doneA                     // A's session ends; its slow "false" is now in flight
	cancelB, doneB := connect() // B reconnects while that delivery is still sleeping
	defer func() { cancelB(); <-doneB }()
	waitFor(t, "B registered", func() bool { return rd.HasSession("agent") })

	time.Sleep(400 * time.Millisecond) // let every delivery finish
	mu.Lock()
	defer mu.Unlock()
	if !last {
		t.Fatal("stale 'disconnected' overwrote the newer 'connected': status would be wrong until the next event")
	}
}

// Patch 6: the ping/pong handlers ran on the read goroutine and called SetWriteDeadline without the write lock, while
// the same connection was being written under it. gorilla's SetWriteDeadline stores a plain field that WriteMessage
// reads, so each ping raced with in-flight traffic. Found by the race detector on a CI runner. Run with -race.
func TestPingHandlersDoNotRaceWithConcurrentWrites(t *testing.T) {
	quietLogs(t)
	SetLiveness(2*time.Millisecond, 5*time.Second) // pings arrive constantly
	t.Cleanup(func() { SetLiveness(DefaultPingWriteInterval, DefaultPingWaitDuration) })

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 4096))
	}))
	defer target.Close()

	rd, ts := newPatchTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ConnectToProxy(ctx, wsURL(ts), nil, func(string, string) bool { return true }, &websocket.Dialer{}, nil)
	}()
	waitFor(t, "session", func() bool { return rd.HasSession("agent") })

	client := &http.Client{Transport: &http.Transport{DialContext: rd.Dialer("agent")}, Timeout: 5 * time.Second}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				resp, err := client.Get(target.URL)
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	cancel()
	<-done
}
