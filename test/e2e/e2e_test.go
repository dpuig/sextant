//go:build e2e

// Package e2e runs the Phase 0 exit gates against two real kind clusters: one
// hosting the management plane (with Postgres), one a workload cluster running
// the agent. It needs kind, helm, kubectl and a container runtime (podman, or docker via
// SEXTANT_E2E_CONTAINER), and the images from
// `make images IMAGE_TAG=e2e`. Run: make e2e
package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dpuig/sextant/pkg/pki/localfile"
)

const (
	mpCluster = "sextant-e2e-mp"
	wlCluster = "sextant-e2e-wl"
	ns        = "sextant-system"
	nodePort  = 30443
	imageTag  = "e2e"
	registry  = "ghcr.io/dpuig/sextant"
	devToken  = "e2e-dev-token-0123456789"
	tenant    = "acme"
	apiPath   = "/apis/sextant.andean.io/v1alpha1/organizations/" + tenant
)

// chart returns the absolute path of a chart: `go test` runs in test/e2e, and
// helm would read a relative "deploy/..." as a repository name.
func chart(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "deploy", "charts", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p, "Chart.yaml")); err != nil {
		t.Fatalf("chart %s not found at %s", name, p)
	}
	return p
}

// container is the runtime that built the images: podman locally, docker in CI.
func container() string {
	if c := os.Getenv("SEXTANT_E2E_CONTAINER"); c != "" {
		return c
	}
	return "podman"
}

func mpCtx() string { return "kind-" + mpCluster }
func wlCtx() string { return "kind-" + wlCluster }

type rig struct {
	t        *testing.T
	dir      string
	hostPort int
	nodeIP   string
	serverCA []byte // PEM of the management plane's (self-signed) server certificate
	http     *http.Client
}

// run executes a command and returns combined output; the test fails on error.
func (r *rig) run(name string, args ...string) string {
	r.t.Helper()
	out, err := r.try(name, args...)
	if err != nil {
		r.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func (r *rig) try(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

func (r *rig) kubectl(kctx string, args ...string) string {
	return r.run("kubectl", append([]string{"--context", kctx}, args...)...)
}

func (r *rig) apply(kctx string, manifest any) {
	r.t.Helper()
	b, _ := json.Marshal(manifest)
	cmd := exec.Command("kubectl", "--context", kctx, "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(b)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("kubectl apply: %v\n%s", err, out)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func secret(name string, data map[string]string) map[string]any {
	sd := map[string]string{}
	for k, v := range data {
		sd[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": name, "namespace": ns}, "data": sd}
}

func pemCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func (r *rig) createClusters() {
	t := r.t
	for _, name := range []string{mpCluster, wlCluster} {
		_, _ = r.try("kind", "delete", "cluster", "--name", name) // stale from a previous run
	}
	t.Cleanup(func() {
		if os.Getenv("SEXTANT_E2E_KEEP") != "" {
			t.Log("SEXTANT_E2E_KEEP set: leaving clusters in place")
			return
		}
		for _, name := range []string{mpCluster, wlCluster} {
			_, _ = r.try("kind", "delete", "cluster", "--name", name)
		}
	})

	cfg := fmt.Sprintf(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - {containerPort: %d, hostPort: %d, listenAddress: "127.0.0.1", protocol: TCP}
`, nodePort, r.hostPort)
	cfgFile := filepath.Join(r.dir, "mp-kind.yaml")
	_ = os.WriteFile(cfgFile, []byte(cfg), 0o600)
	t.Log("creating kind clusters")
	r.run("kind", "create", "cluster", "--name", mpCluster, "--config", cfgFile, "--wait", "180s")
	r.run("kind", "create", "cluster", "--name", wlCluster, "--wait", "180s")

	// Images are pre-built (make images); load them, plus Postgres, into the clusters.
	for _, img := range []string{registry + "/apiserver:" + imageTag, registry + "/agent:" + imageTag, "docker.io/library/postgres:17"} {
		if _, err := r.try(container(), "image", "inspect", img); err != nil {
			r.run(container(), "pull", img) // e.g. postgres on a fresh CI runner
		}
		tar := filepath.Join(r.dir, strings.NewReplacer("/", "_", ":", "_").Replace(img)+".tar")
		r.run(container(), "save", "-o", tar, img)
		for _, c := range []string{mpCluster, wlCluster} {
			if strings.Contains(img, "postgres") && c == wlCluster {
				continue
			}
			r.run("kind", "load", "image-archive", tar, "--name", c)
		}
	}
	r.nodeIP = r.kubectl(mpCtx(), "get", "nodes", "-o", `jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}`)
	if net.ParseIP(r.nodeIP) == nil {
		t.Fatalf("could not determine the management node IP: %q", r.nodeIP)
	}
	t.Logf("management plane node IP %s, host port %d", r.nodeIP, r.hostPort)
	for _, c := range []string{mpCtx(), wlCtx()} {
		r.kubectl(c, "create", "namespace", ns)
	}
}

// diagnose prints what is needed to understand a failure; it runs before the
// clusters are deleted.
func (r *rig) diagnose() {
	for _, c := range []string{mpCtx(), wlCtx()} {
		for _, args := range [][]string{
			{"-n", ns, "get", "pods,jobs,secrets,svc", "-o", "wide"},
			{"-n", ns, "get", "events", "--sort-by=.lastTimestamp"},
			{"-n", ns, "describe", "pods"},
		} {
			out, _ := r.try("kubectl", append([]string{"--context", c}, args...)...)
			r.t.Logf("--- [%s] kubectl %s\n%s", c, strings.Join(args, " "), out)
		}
		pods, _ := r.try("kubectl", "--context", c, "-n", ns, "get", "pods", "-o", "name")
		for _, p := range strings.Fields(pods) {
			out, _ := r.try("kubectl", "--context", c, "-n", ns, "logs", p, "--all-containers", "--tail=80")
			r.t.Logf("--- [%s] logs %s\n%s", c, p, out)
			if prev, err := r.try("kubectl", "--context", c, "-n", ns, "logs", p, "--all-containers", "--previous", "--tail=80"); err == nil {
				r.t.Logf("--- [%s] previous logs %s\n%s", c, p, prev)
			}
		}
	}
}

func (r *rig) installPostgres() {
	r.apply(mpCtx(), map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "sx-postgres", "namespace": ns, "labels": map[string]any{"app": "sx-postgres"}},
		"spec": map[string]any{"containers": []any{map[string]any{
			"name": "postgres", "image": "docker.io/library/postgres:17", "imagePullPolicy": "Never",
			"env": []any{
				map[string]any{"name": "POSTGRES_PASSWORD", "value": "pw"},
				map[string]any{"name": "POSTGRES_DB", "value": "sextant"},
			},
			"readinessProbe": map[string]any{"exec": map[string]any{"command": []any{"pg_isready", "-h", "127.0.0.1", "-U", "postgres"}}, "periodSeconds": 2},
		}}},
	})
	r.apply(mpCtx(), map[string]any{
		"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "sx-postgres", "namespace": ns},
		"spec": map[string]any{"selector": map[string]any{"app": "sx-postgres"}, "ports": []any{map[string]any{"port": 5432}}},
	})
	r.kubectl(mpCtx(), "-n", ns, "wait", "--for=condition=Ready", "pod/sx-postgres", "--timeout=180s")
	// The app role is created out of band (a deployment concern, like the DB
	// itself): the migration only creates it NOLOGIN if it does not exist.
	r.kubectl(mpCtx(), "-n", ns, "exec", "sx-postgres", "--", "psql", "-U", "postgres", "-d", "sextant", "-c",
		"CREATE ROLE sextant_app LOGIN PASSWORD 'app'")
}

func (r *rig) installManagementPlane() {
	t := r.t
	root, err := localfile.Generate("sextant e2e root")
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(r.dir, "root.crt"), filepath.Join(r.dir, "root.key")
	if err := root.Save(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	rootCrt, _ := os.ReadFile(certPath)
	rootKey, _ := os.ReadFile(keyPath)

	// Self-signed server certificate valid for every address the rig uses.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "sextant e2e"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.ParseIP(r.nodeIP)},
		DNSNames:    []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	r.serverCA = pemCert(der)
	tlsSecret := secret("sx-tls", map[string]string{
		"tls.crt": string(r.serverCA),
		"tls.key": string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})),
	})
	tlsSecret["type"] = "kubernetes.io/tls"

	r.apply(mpCtx(), tlsSecret)
	r.apply(mpCtx(), secret("sx-pki", map[string]string{"root.crt": string(rootCrt), "root.key": string(rootKey)}))
	r.apply(mpCtx(), secret("sx-db", map[string]string{
		"app-dsn":   "postgres://sextant_app:app@sx-postgres:5432/sextant?sslmode=disable",
		"owner-dsn": "postgres://postgres:pw@sx-postgres:5432/sextant?sslmode=disable",
	}))
	r.apply(mpCtx(), secret("sx-dev", map[string]string{"token": devToken}))

	r.run("helm", "install", "sextant", chart(t, "sextant"), "--kube-context", mpCtx(), "-n", ns,
		"--set", "image.tag="+imageTag, "--set", "image.pullPolicy=Never",
		"--set", "database.existingSecret=sx-db", "--set", "tls.existingSecret=sx-tls", "--set", "pki.existingSecret=sx-pki",
		"--set", "service.type=NodePort", "--set", fmt.Sprintf("service.nodePort=%d", nodePort),
		"--set", "dev.enabled=true", "--set", "dev.iUnderstandThisIsInsecure=true",
		"--set", "dev.tenant="+tenant, "--set", "dev.tokenSecret=sx-dev",
		"--wait", "--timeout", "90s")

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(r.serverCA)
	r.http = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}}
}

func (r *rig) base() string { return fmt.Sprintf("https://127.0.0.1:%d", r.hostPort) }

// api calls the management plane's REST API as the dev tenant.
func (r *rig) api(method, path, body string) (int, map[string]any, error) {
	req, _ := http.NewRequest(method, r.base()+apiPath+"/"+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := r.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m, nil
}

func (r *rig) clusterConnected() bool {
	code, m, err := r.api("GET", "clusters/wl", "")
	if err != nil || code != 200 {
		return false
	}
	st, _ := m["status"].(map[string]any)
	return st["connected"] == true
}

// sub runs fn as a subtest with the rig's helpers bound to it, so a helper's
// Fatalf ends only that subtest (r.t would otherwise be the parent).
func (r *rig) sub(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		prev := r.t
		r.t = t
		defer func() { r.t = prev }()
		fn(t)
	})
}

func (r *rig) waitFor(what string, within time.Duration, cond func() bool) time.Duration {
	r.t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if cond() {
			return time.Since(start)
		}
		time.Sleep(250 * time.Millisecond)
	}
	r.t.Fatalf("timed out after %v waiting for %s", within, what)
	return 0
}

func pct(d []time.Duration, p float64) time.Duration {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(float64(len(d)-1)*p)]
}

func (r *rig) proxyGet(path string) (int, error) {
	req, _ := http.NewRequest("GET", r.base()+apiPath+"/clusters/wl/proxy"+path, nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	resp, err := r.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func TestPhase0Gates(t *testing.T) {
	for _, tool := range []string{"kind", "helm", "kubectl", container()} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	r := &rig{t: t, dir: t.TempDir(), hostPort: freePort(t)}
	r.createClusters()
	t.Cleanup(func() { // registered after createClusters' cleanup, so it runs first
		if t.Failed() {
			r.diagnose()
		}
	})
	r.installPostgres()
	r.installManagementPlane()

	// ---- management plane is up; create the Cluster and a token.
	r.waitFor("management plane API", 60*time.Second, func() bool {
		code, _, err := r.api("GET", "clusters", "")
		return err == nil && code == 200
	})
	if code, _, err := r.api("POST", "clusters", `{"metadata":{"name":"wl"},"spec":{"environment":"e2e"}}`); err != nil || code != 201 {
		t.Fatalf("create Cluster: %d %v", code, err)
	}
	code, tok, err := r.api("POST", "clusters/wl/registration-tokens", "")
	if err != nil || code != 201 {
		t.Fatalf("mint token: %d %v", code, err)
	}
	token := tok["token"].(string)

	// ---- G1: one helm install connects the agent.
	agentURL := fmt.Sprintf("https://%s:%d", r.nodeIP, nodePort)
	installStart := time.Now()
	r.run("helm", "install", "sextant-agent", chart(t, "sextant-agent"), "--kube-context", wlCtx(), "-n", ns,
		"--set", "image.tag="+imageTag, "--set", "image.pullPolicy=Never",
		"--set", "managementURL="+agentURL, "--set", "registration.token="+token,
		"--set-file", "serverCA="+writeTemp(t, r.dir, "server-ca.pem", r.serverCA),
		"--wait", "--timeout", "3m")
	took := r.waitFor("Cluster.status.connected after helm install", 60*time.Second, r.clusterConnected)
	t.Logf("G1: agent connected %v after `helm install` returned; %v since install started (gate: < 60s)", took.Round(time.Millisecond), time.Since(installStart).Round(time.Millisecond))

	r.sub(t, "agent_enrolled_and_persisted_credentials_in_its_secret", func(t *testing.T) {
		logs := r.kubectl(wlCtx(), "-n", ns, "logs", "deploy/sextant-agent")
		if !strings.Contains(logs, "enrolled") {
			t.Fatalf("agent log does not show enrollment:\n%s", logs)
		}
		data := r.kubectl(wlCtx(), "-n", ns, "get", "secret", "sextant-agent-credentials", "-o", "jsonpath={.data.agent-credentials\\.pem}")
		if len(data) < 100 {
			t.Fatalf("credentials were not written to the Secret (len %d)", len(data))
		}
		// The agent's update must not strip what Helm put on the Secret.
		keep := r.kubectl(wlCtx(), "-n", ns, "get", "secret", "sextant-agent-credentials", "-o", `jsonpath={.metadata.annotations.helm\.sh/resource-policy}`)
		if keep != "keep" {
			t.Fatalf("resource-policy annotation lost after the agent wrote its credentials: %q", keep)
		}
	})

	r.sub(t, "G1_agent_exposes_nothing", func(t *testing.T) {
		out := r.kubectl(wlCtx(), "-n", ns, "get", "deploy,svc,ingress,pods", "-o", "wide")
		if strings.Contains(out, "service/") && strings.Contains(out, "sextant-agent") {
			t.Fatalf("the agent chart created a Service:\n%s", out)
		}
		ports := r.kubectl(wlCtx(), "-n", ns, "get", "pods", "-l", "app.kubernetes.io/name=sextant-agent", "-o", "jsonpath={.items[*].spec.containers[*].ports}")
		if ports != "" {
			t.Fatalf("agent pod declares ports: %s", ports)
		}
	})

	r.sub(t, "agent_rbac_is_least_privilege", func(t *testing.T) {
		sa := "--as=system:serviceaccount:" + ns + ":sextant-agent"
		can := func(verb, res string, extra ...string) string {
			out, _ := r.try("kubectl", append([]string{"--context", wlCtx(), "-n", ns, "auth", "can-i", verb, res, sa}, extra...)...)
			return out
		}
		if can("get", "secret/sextant-agent-credentials") != "yes" || can("update", "secret/sextant-agent-credentials") != "yes" {
			t.Error("agent cannot read/update its own credentials Secret")
		}
		for _, verb := range []string{"create", "delete", "list"} {
			if can(verb, "secrets") != "no" {
				t.Errorf("agent may %s secrets", verb)
			}
		}
		if can("get", "secret/some-other-secret") != "no" {
			t.Error("agent can read other Secrets")
		}
	})

	// ---- G3: kubectl through the tunnel.
	kubeconfig := filepath.Join(r.dir, "proxy-kubeconfig")
	_ = os.WriteFile(kubeconfig, []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: sx
  cluster:
    server: %s%s/clusters/wl/proxy
    certificate-authority-data: %s
users:
- name: dev
  user: {token: %s}
contexts:
- name: sx
  context: {cluster: sx, user: dev}
current-context: sx
`, r.base(), apiPath, base64.StdEncoding.EncodeToString(r.serverCA), devToken)), 0o600)

	r.sub(t, "G3_kubectl_get_pods_through_the_tunnel", func(t *testing.T) {
		out := r.run("kubectl", "--kubeconfig", kubeconfig, "get", "pods", "-A", "-o", "name")
		if !strings.Contains(out, "pod/kube-apiserver-"+wlCluster+"-control-plane") || !strings.Contains(out, "sextant-agent") {
			t.Fatalf("kubectl through the proxy did not list the workload cluster's pods:\n%s", out)
		}
		// The agent acts as its own service account, which is bound to `view`: reads work, writes do not.
		if out, err := r.try("kubectl", "--kubeconfig", kubeconfig, "create", "namespace", "should-fail"); err == nil {
			t.Fatalf("write succeeded through a view-only agent:\n%s", out)
		}
	})

	r.sub(t, "G3_latency", func(t *testing.T) {
		// Baseline: the same API reached directly (via `kubectl proxy`, which adds
		// a small hop of its own, so the added latency below is slightly understated).
		pport := freePort(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		proxy := exec.CommandContext(ctx, "kubectl", "--context", wlCtx(), "proxy", fmt.Sprintf("--port=%d", pport))
		if err := proxy.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cancel(); _ = proxy.Wait() }()
		direct := &http.Client{Timeout: 10 * time.Second}
		const path = "/api/v1/namespaces/kube-system/pods?limit=1"
		r.waitFor("kubectl proxy", 30*time.Second, func() bool {
			resp, err := direct.Get(fmt.Sprintf("http://127.0.0.1:%d%s", pport, path))
			if err != nil {
				return false
			}
			_ = resp.Body.Close()
			return resp.StatusCode == 200
		})
		measure := func(do func() error) []time.Duration {
			for i := 0; i < 50; i++ { // warm up
				_ = do()
			}
			out := make([]time.Duration, 0, 1000)
			for i := 0; i < 1000; i++ {
				start := time.Now()
				if err := do(); err != nil {
					t.Fatalf("request %d: %v", i, err)
				}
				out = append(out, time.Since(start))
			}
			return out
		}
		d := measure(func() error {
			resp, err := direct.Get(fmt.Sprintf("http://127.0.0.1:%d%s", pport, path))
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil
		})
		p := measure(func() error {
			code, err := r.proxyGet(path)
			if err == nil && code != 200 {
				err = fmt.Errorf("status %d", code)
			}
			return err
		})
		added := pct(p, .95) - pct(d, .95)
		t.Logf("G3 direct:  p50=%v p95=%v p99=%v", pct(d, .5).Round(time.Microsecond), pct(d, .95).Round(time.Microsecond), pct(d, .99).Round(time.Microsecond))
		t.Logf("G3 tunnel:  p50=%v p95=%v p99=%v", pct(p, .5).Round(time.Microsecond), pct(p, .95).Round(time.Microsecond), pct(p, .99).Round(time.Microsecond))
		t.Logf("G3 added p95 = %v (gate: < 50ms; same-host rig, so network RTT is ~0: re-measure in a real region)", added.Round(time.Microsecond))
		if added > 50*time.Millisecond {
			t.Fatalf("added p95 latency %v exceeds 50ms", added)
		}
	})

	// ---- agent restart: credentials persist in the Secret, token no longer needed.
	r.sub(t, "agent_restart_uses_stored_credentials", func(t *testing.T) {
		oldPod := r.kubectl(wlCtx(), "-n", ns, "get", "pods", "-l", "app.kubernetes.io/name=sextant-agent", "-o", "jsonpath={.items[0].metadata.name}")
		r.kubectl(wlCtx(), "-n", ns, "delete", "secret", "sextant-agent-registration") // the token is gone for good
		r.kubectl(wlCtx(), "-n", ns, "delete", "pod", oldPod)
		var newPod string
		r.waitFor("a replacement agent pod", 90*time.Second, func() bool {
			out, _ := r.try("kubectl", "--context", wlCtx(), "-n", ns, "get", "pods", "-l", "app.kubernetes.io/name=sextant-agent", "-o", "jsonpath={.items[0].metadata.name}")
			newPod = out
			return out != "" && out != oldPod
		})
		// "The proxy answers" is not enough: until the old pod is really gone the OLD
		// agent can still answer. Wait for the replacement's own log instead.
		var logs string
		r.waitFor("the replacement pod to log its credential source", 90*time.Second, func() bool {
			out, err := r.try("kubectl", "--context", wlCtx(), "-n", ns, "logs", newPod)
			logs = out
			return err == nil && (strings.Contains(out, "loaded stored agent credentials") || strings.Contains(out, "enrolled"))
		})
		if !strings.Contains(logs, "loaded stored agent credentials") || strings.Contains(logs, "enrolled\n") || strings.HasSuffix(strings.TrimSpace(logs), "enrolled") {
			t.Fatalf("replacement pod did not come up from stored credentials:\n%s", logs)
		}
		r.waitFor("the replacement agent to serve requests", 60*time.Second, func() bool {
			code, err := r.proxyGet("/api")
			return err == nil && code == 200
		})
	})

	// ---- G2: management-plane restarts.
	r.sub(t, "G2_management_plane_restart", func(t *testing.T) {
		podNames := func() []string {
			out, _ := r.try("kubectl", "--context", mpCtx(), "-n", ns, "get", "pods", "-l", "app.kubernetes.io/name=sextant", "-o", "name")
			return strings.Fields(out)
		}
		for i := 1; i <= 3; i++ {
			old := podNames()
			if len(old) != 1 {
				t.Fatalf("run %d: expected one management-plane pod, got %v", i, old)
			}
			r.kubectl(mpCtx(), "-n", ns, "rollout", "restart", "deployment/sextant")

			// Probe the data plane every 50ms until the old pod is gone and the proxy has
			// been healthy for 3s. The outage is the longest gap between successes, which
			// also covers the case where the restart is too fast for any probe to fail.
			start := time.Now()
			lastOK := start
			var maxGap time.Duration
			var stableSince time.Time
			lastPodCheck := time.Time{}
			oldGone := false
			deadline := start.Add(120 * time.Second)
			for time.Now().Before(deadline) {
				code, err := r.proxyGet("/api")
				now := time.Now()
				ok := err == nil && code == 200
				if ok {
					if gap := now.Sub(lastOK); gap > maxGap {
						maxGap = gap
					}
					lastOK = now
				}
				if now.Sub(lastPodCheck) > time.Second {
					lastPodCheck = now
					cur := podNames()
					oldGone = len(cur) == 1 && cur[0] != old[0]
				}
				if oldGone && ok {
					if stableSince.IsZero() {
						stableSince = now
					}
					if now.Sub(stableSince) > 3*time.Second {
						break
					}
				} else {
					stableSince = time.Time{}
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !oldGone || stableSince.IsZero() {
				t.Fatalf("run %d: management plane did not settle within 120s (old pod gone: %v)", i, oldGone)
			}
			t.Logf("G2 run %d: longest data-plane gap %v; old pod replaced after %v (gate: gap < 30s)", i, maxGap.Round(time.Millisecond), time.Since(start).Round(time.Second))
			if maxGap > 30*time.Second {
				t.Fatalf("run %d: gap %v exceeds 30s", i, maxGap)
			}
			r.waitFor("Cluster.status.connected after restart", 30*time.Second, r.clusterConnected)
		}
	})
}

func writeTemp(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
