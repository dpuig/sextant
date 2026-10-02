package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/server"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/storage/storagetest"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
)

const base = "/apis/sextant.andean.io/v1alpha1/organizations/"

// tokenAuth maps bearer tokens to a single tenant each.
type tokenAuth map[string]string

func (a tokenAuth) Authenticate(r *http.Request) (server.Principal, error) {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if _, ok := a[tok]; !ok {
		return server.Principal{}, errors.New("no")
	}
	return server.Principal{Subject: tok}, nil
}

func (a tokenAuth) Authorize(p server.Principal, t tenancy.ID, _, _ string) bool {
	return a[p.Subject] == t.String()
}

var auth = tokenAuth{"tok-acme": "acme", "tok-globex": "globex"}

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- Rejections that must happen before storage is touched (nil registry:
// reaching it would panic). ---

func TestRejectedBeforeStorage(t *testing.T) {
	h := server.New(nil, auth, auth, nil)
	tests := []struct {
		name, method, path, token string
		want                      int
	}{
		{"no credentials", "GET", base + "acme/clusters", "", 401},
		{"bad credentials", "GET", base + "acme/clusters", "nope", 401},
		{"other tenant's path", "GET", base + "globex/clusters", "tok-acme", 403},
		{"other tenant's object", "GET", base + "globex/clusters/c1", "tok-acme", 403},
		{"other tenant create", "POST", base + "globex/clusters", "tok-acme", 403},
		{"other tenant delete", "DELETE", base + "globex/clusters/c1", "tok-acme", 403},
		{"other tenant status", "PUT", base + "globex/clusters/c1/status", "tok-acme", 403},
		{"nonexistent tenant is also 403", "GET", base + "nobody/clusters", "tok-acme", 403},
		{"invalid tenant id", "GET", base + "AC.ME/clusters", "tok-acme", 400},
		{"unknown resource", "GET", base + "acme/widgets", "tok-acme", 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := do(t, h, tt.method, tt.path, tt.token, "").Code; got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDenyAllByDefault(t *testing.T) {
	h := server.New(nil, auth, server.DenyAll{}, nil)
	if got := do(t, h, "GET", base+"acme/clusters", "tok-acme", "").Code; got != 403 {
		t.Fatalf("status = %d, want 403", got)
	}
}

func TestStaticAuth(t *testing.T) {
	acme, _ := tenancy.ParseID("acme")
	if _, err := server.NewStaticAuth("short", acme); err == nil {
		t.Fatal("short token accepted")
	}
	sa, err := server.NewStaticAuth("0123456789abcdef", acme)
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(nil, sa, sa, nil)
	if got := do(t, h, "GET", base+"acme/clusters", "wrong-token-value", "").Code; got != 401 {
		t.Fatalf("wrong token status = %d", got)
	}
	if got := do(t, h, "GET", base+"globex/clusters", "0123456789abcdef", "").Code; got != 403 {
		t.Fatalf("foreign tenant status = %d", got)
	}
}

// --- Full request flow against Postgres. ---

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	return server.New(registry.New(storage.New(storagetest.NewPool(t))), auth, auth, nil)
}

const clusterBody = `{"metadata":{"name":"c1","labels":{"team":"payments"}},"spec":{"environment":"prod"}}`

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return m
}

func TestLifecycle(t *testing.T) {
	h := newHandler(t)
	p := base + "acme/clusters"

	rec := do(t, h, "POST", p, "tok-acme", clusterBody)
	if rec.Code != 201 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	created := decode(t, rec)
	if created["kind"] != "Cluster" || created["apiVersion"] != "sextant.andean.io/v1alpha1" {
		t.Fatalf("typemeta missing: %v", created)
	}
	if got := do(t, h, "POST", p, "tok-acme", clusterBody).Code; got != 409 {
		t.Fatalf("duplicate create = %d", got)
	}

	rv := created["metadata"].(map[string]any)["resourceVersion"].(string)
	upd := `{"metadata":{"name":"c1","resourceVersion":"` + rv + `"},"spec":{"environment":"prod","provider":"eks"}}`
	if got := do(t, h, "PUT", p+"/c1", "tok-acme", upd).Code; got != 200 {
		t.Fatalf("update = %d", got)
	}
	if got := do(t, h, "PUT", p+"/c1", "tok-acme", upd).Code; got != 409 { // now stale
		t.Fatalf("stale update = %d, want 409", got)
	}

	got := decode(t, do(t, h, "GET", p+"/c1", "tok-acme", ""))
	rv = got["metadata"].(map[string]any)["resourceVersion"].(string)
	st := `{"metadata":{"name":"c1","resourceVersion":"` + rv + `"},"status":{"connected":true}}`
	if rec := do(t, h, "PUT", p+"/c1/status", "tok-acme", st); rec.Code != 200 {
		t.Fatalf("status update = %d %s", rec.Code, rec.Body)
	}
	got = decode(t, do(t, h, "GET", p+"/c1", "tok-acme", ""))
	if got["status"].(map[string]any)["connected"] != true || got["spec"].(map[string]any)["provider"] != "eks" {
		t.Fatalf("after status update: %v", got)
	}

	list := decode(t, do(t, h, "GET", p, "tok-acme", ""))
	if list["kind"] != "ClusterList" || len(list["items"].([]any)) != 1 {
		t.Fatalf("list = %v", list)
	}
	if got := do(t, h, "DELETE", p+"/c1", "tok-acme", "").Code; got != 204 {
		t.Fatalf("delete = %d", got)
	}
	if got := do(t, h, "GET", p+"/c1", "tok-acme", "").Code; got != 404 {
		t.Fatalf("get after delete = %d", got)
	}
}

func TestBadBodies(t *testing.T) {
	h := newHandler(t)
	p := base + "acme/clusters"
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"unknown field", "POST", p, `{"metadata":{"name":"c1"},"spec":{"environment":"prod","evil":1}}`, 422},
		{"not json", "POST", p, `{`, 422},
		{"trailing data", "POST", p, clusterBody + `{}`, 422},
		{"invalid spec", "POST", p, `{"metadata":{"name":"c1"}}`, 422},
		{"kind mismatch", "POST", p, `{"kind":"Workspace","metadata":{"name":"c1"},"spec":{"environment":"p"}}`, 422},
		{"name mismatch with path", "PUT", p + "/c2", clusterBody, 422},
		{"oversize", "POST", p, `{"metadata":{"name":"c1","annotations":{"a":"` + strings.Repeat("x", 1<<20) + `"}}}`, 422},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := do(t, h, tt.method, tt.path, "tok-acme", tt.body).Code; got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOrganizationRoute_NameMustEqualTenant(t *testing.T) {
	h := newHandler(t)
	if got := do(t, h, "POST", base+"acme/organizations", "tok-acme", `{"metadata":{"name":"globex"}}`).Code; got != 422 {
		t.Fatalf("foreign-named org = %d, want 422", got)
	}
	if got := do(t, h, "POST", base+"acme/organizations", "tok-acme", `{"metadata":{"name":"acme"}}`).Code; got != 201 {
		t.Fatalf("own org = %d, want 201", got)
	}
}

func TestHTTPTenantIsolation(t *testing.T) {
	h := newHandler(t)
	do(t, h, "POST", base+"acme/clusters", "tok-acme", clusterBody)
	if got := do(t, h, "GET", base+"globex/clusters/c1", "tok-globex", "").Code; got != 404 {
		t.Fatalf("globex reading its own namespace for acme's name = %d, want 404", got)
	}
	list := decode(t, do(t, h, "GET", base+"globex/clusters", "tok-globex", ""))
	if n := len(list["items"].([]any)); n != 0 {
		t.Fatalf("globex sees %d acme clusters", n)
	}
}

// --- cluster routes: registration tokens and the kube-apiserver proxy ---

type fakeTokens struct {
	mu      sync.Mutex
	created []string
}

func (f *fakeTokens) Create(ctx context.Context, agent string, ttl time.Duration) (string, error) {
	tid, err := tenancy.FromContext(ctx)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.created = append(f.created, tid.String()+"/"+agent+"/"+ttl.String())
	f.mu.Unlock()
	return "sxt1." + tid.String() + ".secret", nil
}

// fakeAgents "tunnels" by dialing a local TCP address, whatever address is asked for.
type fakeAgents struct {
	connected map[string]bool // "tenant/agent"
	target    string
}

func (f *fakeAgents) HasAgent(t tenancy.ID, a string) bool { return f.connected[t.String()+"/"+a] }
func (f *fakeAgents) Dialer(tenancy.ID, string) tunnel.Dialer {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.target)
	}
}

func newClusterHandler(t *testing.T, upstream http.Handler, connected ...string) (http.Handler, *fakeTokens) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	ag := &fakeAgents{connected: map[string]bool{}, target: strings.TrimPrefix(up.URL, "http://")}
	for _, c := range connected {
		ag.connected[c] = true
	}
	tk := &fakeTokens{}
	h := server.New(registry.New(storage.New(storagetest.NewPool(t))), auth, auth, nil, server.WithTokens(tk), server.WithAgents(ag))
	return h, tk
}

func mkCluster(t *testing.T, h http.Handler, tenant, token, name string) {
	t.Helper()
	body := `{"metadata":{"name":"` + name + `"},"spec":{"environment":"prod"}}`
	if rec := do(t, h, "POST", base+tenant+"/clusters", token, body); rec.Code != 201 {
		t.Fatalf("create cluster: %d %s", rec.Code, rec.Body)
	}
}

func TestRegistrationToken_IssuedOnceForExistingCluster(t *testing.T) {
	h, tk := newClusterHandler(t, http.NotFoundHandler())
	p := base + "acme/clusters/c1/registration-tokens"

	if got := do(t, h, "POST", p, "tok-acme", "").Code; got != 404 {
		t.Fatalf("token for missing cluster = %d, want 404", got)
	}
	mkCluster(t, h, "acme", "tok-acme", "c1")
	rec := do(t, h, "POST", p, "tok-acme", "")
	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if m := decode(t, rec); m["token"] != "sxt1.acme.secret" || m["expiresAt"] == nil {
		t.Fatalf("body = %v", m)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token response must not be cacheable")
	}
	if len(tk.created) != 1 || tk.created[0] != "acme/c1/1h0m0s" {
		t.Fatalf("created = %v (default ttl should be 1h)", tk.created)
	}
}

func TestRegistrationToken_TTLValidationAndAuthz(t *testing.T) {
	h, _ := newClusterHandler(t, http.NotFoundHandler())
	mkCluster(t, h, "acme", "tok-acme", "c1")
	p := base + "acme/clusters/c1/registration-tokens"
	for body, want := range map[string]int{
		`{"ttlSeconds":600}`:        201,
		`{"ttlSeconds":86400}`:      201,
		`{"ttlSeconds":86401}`:      422,
		`{"ttlSeconds":-5}`:         422,
		`{"ttlSeconds":9223372037}`: 422, // seconds*1e9 wraps int64
		`{"ttlSeconds":"x"}`:        422,
		`{"surprise":1}`:            422,
	} {
		if got := do(t, h, "POST", p, "tok-acme", body).Code; got != want {
			t.Errorf("body %s: status = %d, want %d", body, got, want)
		}
	}
	if got := do(t, h, "POST", base+"acme/clusters/c1/registration-tokens", "tok-globex", "").Code; got != 403 {
		t.Fatalf("other tenant = %d, want 403", got)
	}
	if got := do(t, h, "POST", p, "", "").Code; got != 401 {
		t.Fatalf("anonymous = %d, want 401", got)
	}
}

func TestRegistrationTokenRouteAbsentWithoutIssuer(t *testing.T) {
	h := newHandler(t) // no WithTokens
	if got := do(t, h, "POST", base+"acme/clusters/c1/registration-tokens", "tok-acme", "").Code; got == 201 || got == 200 {
		t.Fatalf("route should not exist, got %d", got)
	}
}

func TestProxy_ForwardsThroughTunnelWithoutCallerCredential(t *testing.T) {
	var got struct {
		sync.Mutex
		path, query, auth, host string
	}
	h, _ := newClusterHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Lock()
		got.path, got.query, got.auth, got.host = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Host
		got.Unlock()
		_, _ = io.WriteString(w, `{"kind":"PodList"}`)
	}), "acme/c1")
	mkCluster(t, h, "acme", "tok-acme", "c1")

	rec := do(t, h, "GET", base+"acme/clusters/c1/proxy/api/v1/namespaces/default/pods?limit=5", "tok-acme", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "PodList") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got.Lock()
	defer got.Unlock()
	if got.path != "/api/v1/namespaces/default/pods" || got.query != "limit=5" {
		t.Fatalf("cluster saw %q ? %q", got.path, got.query)
	}
	if got.auth != "" {
		t.Fatalf("the caller's Sextant credential reached the cluster side: %q", got.auth)
	}
	if got.host != "kube-apiserver.sextant.internal:80" {
		t.Fatalf("host = %q", got.host)
	}
}

func TestProxy_AgentOfflineIs503_UnknownClusterIs404(t *testing.T) {
	h, _ := newClusterHandler(t, http.NotFoundHandler()) // nobody connected
	mkCluster(t, h, "acme", "tok-acme", "c1")
	if got := do(t, h, "GET", base+"acme/clusters/c1/proxy/api", "tok-acme", "").Code; got != 503 {
		t.Fatalf("offline = %d, want 503", got)
	}
	if got := do(t, h, "GET", base+"acme/clusters/ghost/proxy/api", "tok-acme", "").Code; got != 404 {
		t.Fatalf("unknown cluster = %d, want 404", got)
	}
}

func TestProxy_TenantIsolation(t *testing.T) {
	// globex has an agent named c1 too; acme must never be routed to it, and
	// globex's credentials must not reach acme's cluster.
	h, _ := newClusterHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }), "acme/c1", "globex/c1")
	mkCluster(t, h, "acme", "tok-acme", "c1")
	if got := do(t, h, "GET", base+"acme/clusters/c1/proxy/api", "tok-globex", "").Code; got != 403 {
		t.Fatalf("globex token on acme cluster = %d, want 403", got)
	}
	// globex has the agent but no Cluster object of that name: 404, not a tunnel hit.
	if got := do(t, h, "GET", base+"globex/clusters/c1/proxy/api", "tok-globex", "").Code; got != 404 {
		t.Fatalf("agent without Cluster object = %d, want 404", got)
	}
}

func TestProxy_StreamsWatchResponses(t *testing.T) {
	release := make(chan struct{})
	h, _ := newClusterHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "event-1\n")
		w.(http.Flusher).Flush()
		<-release
	}), "acme/c1")
	mkCluster(t, h, "acme", "tok-acme", "c1")
	front := httptest.NewServer(h)
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+base+"acme/clusters/c1/proxy/api/v1/pods?watch=true", nil)
	req.Header.Set("Authorization", "Bearer tok-acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close(); close(release) }()
	line := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(resp.Body).ReadString('\n'); line <- s }()
	select {
	case s := <-line:
		if s != "event-1\n" {
			t.Fatalf("line = %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch event was buffered")
	}
}

// kubectl logs -f / get -w outlive any sane server WriteTimeout; the proxy
// route must lift the deadline for itself while the rest keep it.
func TestProxy_LongStreamsSurviveServerWriteTimeout(t *testing.T) {
	release := make(chan struct{})
	h, _ := newClusterHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "event-1\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "event-2\n")
	}), "acme/c1")
	mkCluster(t, h, "acme", "tok-acme", "c1")

	front := httptest.NewUnstartedServer(h)
	front.Config.WriteTimeout = 300 * time.Millisecond
	front.Config.ReadTimeout = 300 * time.Millisecond
	front.Start()
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+base+"acme/clusters/c1/proxy/api/v1/pods?watch=true", nil)
	req.Header.Set("Authorization", "Bearer tok-acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	r := bufio.NewReader(resp.Body)
	if s, _ := r.ReadString('\n'); s != "event-1\n" {
		t.Fatalf("first line = %q", s)
	}
	time.Sleep(900 * time.Millisecond) // three WriteTimeouts later
	close(release)
	s, err := r.ReadString('\n')
	if err != nil || s != "event-2\n" {
		t.Fatalf("stream was cut by the server write timeout: line=%q err=%v", s, err)
	}
}
