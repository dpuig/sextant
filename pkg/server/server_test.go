package server_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/server"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/storage/storagetest"
	"github.com/dpuig/sextant/pkg/tenancy"
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
