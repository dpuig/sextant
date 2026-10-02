package agent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKubeAPI implements just the secrets endpoints the store uses.
type fakeKubeAPI struct {
	mu      sync.Mutex
	secrets map[string]map[string]any // name -> object
	rv      int
	calls   []string
	authz   []string
	failPut bool
}

func (f *fakeKubeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.authz = append(f.authz, r.Header.Get("Authorization"))
	const prefix = "/api/v1/namespaces/sextant-system/secrets"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")
	body, _ := io.ReadAll(r.Body)
	switch r.Method {
	case "GET":
		obj, ok := f.secrets[name]
		if !ok {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"kind":"Status","code":404}`)
			return
		}
		_ = json.NewEncoder(w).Encode(obj)
	case "POST":
		var obj map[string]any
		_ = json.Unmarshal(body, &obj)
		n := obj["metadata"].(map[string]any)["name"].(string)
		if _, exists := f.secrets[n]; exists {
			w.WriteHeader(409)
			return
		}
		f.rv++
		obj["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(f.rv)
		f.secrets[n] = obj
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(obj)
	case "PUT":
		if f.failPut {
			w.WriteHeader(500)
			return
		}
		var obj map[string]any
		_ = json.Unmarshal(body, &obj)
		cur, ok := f.secrets[name]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if obj["metadata"].(map[string]any)["resourceVersion"] != cur["metadata"].(map[string]any)["resourceVersion"] {
			w.WriteHeader(409)
			return
		}
		f.rv++
		obj["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(f.rv)
		f.secrets[name] = obj
		_ = json.NewEncoder(w).Encode(obj)
	default:
		w.WriteHeader(405)
	}
}

func newSecretStore(t *testing.T) (*SecretStore, *fakeKubeAPI) {
	t.Helper()
	api := &fakeKubeAPI{secrets: map[string]map[string]any{}}
	ts := httptest.NewTLSServer(api)
	t.Cleanup(ts.Close)
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(caFile, pemEncodeCert(ts.Certificate()), 0o600)
	tokFile := filepath.Join(dir, "token")
	_ = os.WriteFile(tokFile, []byte("sa-token"), 0o600)
	u, _ := url.Parse(ts.URL)
	st, err := NewSecretStore(u, caFile, tokFile, "sextant-system", "sextant-agent-credentials")
	if err != nil {
		t.Fatal(err)
	}
	return st, api
}

func pemEncodeCert(c *x509.Certificate) []byte {
	return []byte("-----BEGIN CERTIFICATE-----\n" + b64Lines(c.Raw) + "-----END CERTIFICATE-----\n")
}

func TestSecretStore_EmptyThenCreateThenUpdateRoundTrip(t *testing.T) {
	st, api := newSecretStore(t)
	if got, err := st.Load(); got != nil || err != nil {
		t.Fatalf("Load on missing secret = %v, %v", got, err)
	}
	now := time.Now()
	first := selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour))
	if err := st.Save(first); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil || got == nil || !got.Key.Equal(first.Key) {
		t.Fatalf("Load after create = %v, %v", got, err)
	}

	second := selfSignedCreds(t, now.Add(-time.Hour), now.Add(2*time.Hour))
	if err := st.Save(second); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.Load()
	if !got.Key.Equal(second.Key) {
		t.Fatal("update did not replace the credentials")
	}
	// Every call authenticated as the agent's service account.
	for _, a := range api.authz {
		if a != "Bearer sa-token" {
			t.Fatalf("call authenticated with %q", a)
		}
	}
	obj := api.secrets["sextant-agent-credentials"]
	if obj["type"] != "Opaque" {
		t.Fatalf("type = %v", obj["type"])
	}
}

// With a pre-created Secret (how the Helm chart ships it) the agent must only
// ever GET and PUT: its Role grants no "create".
func TestSecretStore_NeverCreatesWhenSecretExists(t *testing.T) {
	st, api := newSecretStore(t)
	api.secrets["sextant-agent-credentials"] = map[string]any{
		"metadata": map[string]any{"name": "sextant-agent-credentials", "resourceVersion": "7"},
	}
	now := time.Now()
	if err := st.Save(selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	for _, c := range api.calls {
		if strings.HasPrefix(c, "POST") {
			t.Fatalf("agent issued %q although the Secret exists", c)
		}
	}
}

// The chart ships the Secret with Helm labels and a resource-policy: keep
// annotation; an update that replaced the whole object would silently drop
// them and a later `helm uninstall` would delete the agent's identity.
func TestSecretStore_UpdatePreservesMetadataAndOtherKeys(t *testing.T) {
	st, api := newSecretStore(t)
	api.secrets["sextant-agent-credentials"] = map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]any{
			"name": "sextant-agent-credentials", "resourceVersion": "3",
			"labels":      map[string]any{"app.kubernetes.io/managed-by": "Helm"},
			"annotations": map[string]any{"helm.sh/resource-policy": "keep"},
		},
		"data": map[string]any{"unrelated": "b3RoZXI="},
	}
	now := time.Now()
	if err := st.Save(selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	got := api.secrets["sextant-agent-credentials"]
	meta := got["metadata"].(map[string]any)
	if meta["annotations"].(map[string]any)["helm.sh/resource-policy"] != "keep" {
		t.Fatal("resource-policy annotation was dropped")
	}
	if meta["labels"].(map[string]any)["app.kubernetes.io/managed-by"] != "Helm" {
		t.Fatal("labels were dropped")
	}
	data := got["data"].(map[string]any)
	if data["unrelated"] != "b3RoZXI=" || data[secretKey] == nil {
		t.Fatalf("data = %v: must keep other keys and add ours", data)
	}
	if loaded, err := st.Load(); err != nil || loaded == nil {
		t.Fatalf("Load after update: %v %v", loaded, err)
	}
}

func TestSecretStore_SaveFallsBackToUpdateWhenSecretAppearsConcurrently(t *testing.T) {
	st, api := newSecretStore(t)
	now := time.Now()
	// Secret already exists (e.g. created by a previous pod) but Load was never called.
	_ = st.Save(selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour)))
	api.calls = nil
	next := selfSignedCreds(t, now.Add(-time.Hour), now.Add(3*time.Hour))
	if err := st.Save(next); err != nil {
		t.Fatalf("save over existing: %v", err)
	}
	got, _ := st.Load()
	if !got.Key.Equal(next.Key) {
		t.Fatal("stale credentials after save")
	}
}

func TestSecretStore_ErrorsPropagate(t *testing.T) {
	st, api := newSecretStore(t)
	now := time.Now()
	_ = st.Save(selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour)))
	api.mu.Lock()
	api.failPut = true
	api.mu.Unlock()
	if err := st.Save(selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour))); err == nil {
		t.Fatal("a failed write was reported as success; the agent would lose its credentials on restart")
	}
}

func TestSecretStore_CorruptDataIsAnError(t *testing.T) {
	st, api := newSecretStore(t)
	api.secrets["sextant-agent-credentials"] = map[string]any{
		"metadata": map[string]any{"name": "sextant-agent-credentials", "resourceVersion": "1"},
		"data":     map[string]any{"agent-credentials.pem": "Z2FyYmFnZQ=="},
	}
	if _, err := st.Load(); err == nil {
		t.Fatal("garbage credentials accepted")
	}
}

func TestSecretStore_RejectsUntrustedAPIServer(t *testing.T) {
	api := &fakeKubeAPI{secrets: map[string]map[string]any{}}
	ts := httptest.NewUnstartedServer(api)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{impostor(t)}}
	ts.StartTLS()
	defer ts.Close()
	dir := t.TempDir()
	good := httptest.NewTLSServer(http.NotFoundHandler()) // the CA we actually trust
	defer good.Close()
	caFile := filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(caFile, pemEncodeCert(good.Certificate()), 0o600)
	tokFile := filepath.Join(dir, "token")
	_ = os.WriteFile(tokFile, []byte("sa-token"), 0o600)
	u, _ := url.Parse(ts.URL)
	st, _ := NewSecretStore(u, caFile, tokFile, "sextant-system", "x")
	if _, err := st.Load(); err == nil {
		t.Fatal("talked to an API server the CA does not vouch for (would have sent the SA token)")
	}
	if len(api.calls) != 0 {
		t.Fatalf("request reached the impostor: %v", api.calls)
	}
}
