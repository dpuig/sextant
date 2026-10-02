// Package charts_test renders the Helm charts with `helm template` and checks
// the properties that must not regress: hardened pod specs, secrets only by
// reference, least-privilege RBAC, and the dev-auth gates.
package charts_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

type obj map[string]any

func (o obj) kind() string { s, _ := o["kind"].(string); return s }
func (o obj) name() string { return dig(o, "metadata", "name").(string) }

func dig(v any, path ...string) any {
	for _, p := range path {
		if o, isObj := v.(obj); isObj {
			v = map[string]any(o)
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func render(t *testing.T, chart string, sets ...string) ([]obj, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not installed")
	}
	args := []string{"template", "rel", chart, "--namespace", "sextant-system"}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("helm", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, &renderError{stderr.String()}
	}
	var objs []obj
	dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		var o obj
		if err := dec.Decode(&o); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode rendered YAML: %v", err)
		}
		if len(o) > 0 {
			objs = append(objs, o)
		}
	}
	return objs, nil
}

type renderError struct{ msg string }

func (e *renderError) Error() string { return e.msg }

func must(t *testing.T, chart string, sets ...string) []obj {
	t.Helper()
	objs, err := render(t, chart, sets...)
	if err != nil {
		t.Fatalf("helm template failed: %v", err)
	}
	return objs
}

func find(objs []obj, kind, nameContains string) obj {
	for _, o := range objs {
		if o.kind() == kind && strings.Contains(o.name(), nameContains) {
			return o
		}
	}
	return nil
}

func ofKind(objs []obj, kind string) []obj {
	var out []obj
	for _, o := range objs {
		if o.kind() == kind {
			out = append(out, o)
		}
	}
	return out
}

const (
	mp    = "sextant"
	agent = "sextant-agent"
)

var mpRequired = []string{"database.existingSecret=db", "tls.existingSecret=tls", "pki.existingSecret=pki"}
var agentRequired = []string{"managementURL=https://mp.example.com"}

func podSpecs(objs []obj) []obj {
	var specs []obj
	for _, o := range objs {
		switch o.kind() {
		case "Deployment":
			specs = append(specs, obj(dig(o, "spec", "template", "spec").(map[string]any)))
		case "Job":
			specs = append(specs, obj(dig(o, "spec", "template", "spec").(map[string]any)))
		}
	}
	return specs
}

func TestPodsAreHardened(t *testing.T) {
	for chart, sets := range map[string][]string{mp: mpRequired, agent: agentRequired} {
		objs := must(t, "./"+chart, sets...)
		specs := podSpecs(objs)
		if len(specs) == 0 {
			t.Fatalf("%s: no workloads rendered", chart)
		}
		for _, spec := range specs {
			if dig(spec, "securityContext", "runAsNonRoot") != true {
				t.Errorf("%s: pod must set runAsNonRoot", chart)
			}
			if dig(spec, "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
				t.Errorf("%s: pod must use the RuntimeDefault seccomp profile", chart)
			}
			for _, c := range spec["containers"].([]any) {
				sc := dig(c, "securityContext")
				if dig(sc, "allowPrivilegeEscalation") != false || dig(sc, "readOnlyRootFilesystem") != true {
					t.Errorf("%s: container %v is not locked down", chart, dig(c, "name"))
				}
				caps, _ := dig(sc, "capabilities", "drop").([]any)
				if len(caps) != 1 || caps[0] != "ALL" {
					t.Errorf("%s: container must drop ALL capabilities", chart)
				}
				if dig(sc, "privileged") == true {
					t.Errorf("%s: privileged container", chart)
				}
			}
		}
	}
}

func TestManagementPlane_SecretsOnlyByReference(t *testing.T) {
	objs := must(t, "./"+mp, append(mpRequired, "dev.enabled=true", "dev.iUnderstandThisIsInsecure=true", "dev.tenant=acme", "dev.tokenSecret=devtok")...)
	var rendered strings.Builder
	for _, o := range objs {
		b, _ := json.Marshal(o)
		rendered.Write(b)
	}
	if strings.Contains(rendered.String(), "postgres://") {
		t.Fatal("a database URL appears literally in the rendered manifests")
	}
	// The dev token and the DSN must come from secretKeyRef, never an arg or literal value.
	dep := find(objs, "Deployment", "sextant")
	c := dig(dep, "spec", "template", "spec", "containers").([]any)[0]
	for _, e := range c.(map[string]any)["env"].([]any) {
		if dig(e, "valueFrom", "secretKeyRef") == nil {
			t.Errorf("env %v is not a secretKeyRef", dig(e, "name"))
		}
	}
	for _, a := range c.(map[string]any)["args"].([]any) {
		if strings.Contains(strings.ToLower(a.(string)), "token") && !strings.HasPrefix(a.(string), "--dev-") {
			t.Errorf("suspicious arg %q", a)
		}
	}
	if dig(dep, "spec", "template", "spec", "automountServiceAccountToken") != false {
		t.Error("the management plane never talks to the Kubernetes API; do not mount a token")
	}
}

func TestManagementPlane_DevAuthIsOffByDefaultAndGated(t *testing.T) {
	objs := must(t, "./"+mp, mpRequired...)
	b, _ := json.Marshal(find(objs, "Deployment", "sextant"))
	if strings.Contains(string(b), "dev-tenant") || strings.Contains(string(b), "SEXTANT_DEV_TOKEN") {
		t.Fatal("dev auth rendered by default")
	}
	for name, sets := range map[string][]string{
		"without the confirmation": append(append([]string{}, mpRequired...), "dev.enabled=true", "dev.tenant=acme", "dev.tokenSecret=t"),
		"without a tenant":         append(append([]string{}, mpRequired...), "dev.enabled=true", "dev.iUnderstandThisIsInsecure=true", "dev.tokenSecret=t"),
		"without a token secret":   append(append([]string{}, mpRequired...), "dev.enabled=true", "dev.iUnderstandThisIsInsecure=true", "dev.tenant=acme"),
	} {
		if _, err := render(t, "./"+mp, sets...); err == nil {
			t.Errorf("dev auth rendered %s", name)
		}
	}
}

func TestManagementPlane_RequiredValues(t *testing.T) {
	for _, missing := range []string{"database.existingSecret", "tls.existingSecret", "pki.existingSecret"} {
		var sets []string
		for _, s := range mpRequired {
			if !strings.HasPrefix(s, missing+"=") {
				sets = append(sets, s)
			}
		}
		if _, err := render(t, "./"+mp, sets...); err == nil {
			t.Errorf("chart rendered without %s", missing)
		}
	}
}

func TestManagementPlane_MigrationRunsAsHookWithOwnerCredentials(t *testing.T) {
	objs := must(t, "./"+mp, mpRequired...)
	job := find(objs, "Job", "migrate")
	if job == nil {
		t.Fatal("no migrate job")
	}
	if h := dig(job, "metadata", "annotations", "helm.sh/hook"); h != "pre-install,pre-upgrade" {
		t.Fatalf("hook = %v", h)
	}
	env := dig(job, "spec", "template", "spec", "containers").([]any)[0].(map[string]any)["env"].([]any)
	if dig(env[0], "valueFrom", "secretKeyRef", "key") != "owner-dsn" {
		t.Fatal("migrations must use the schema-owner DSN")
	}
	// And the server must use the NON-owner DSN so row-level security applies.
	dep := find(objs, "Deployment", "sextant")
	senv := dig(dep, "spec", "template", "spec", "containers").([]any)[0].(map[string]any)["env"].([]any)
	if dig(senv[0], "valueFrom", "secretKeyRef", "key") != "app-dsn" {
		t.Fatal("the server must connect as the app role, not the owner")
	}
	if off := must(t, "./"+mp, append(mpRequired, "migrate.enabled=false")...); find(off, "Job", "migrate") != nil {
		t.Fatal("migrate.enabled=false still renders the job")
	}
}

func TestManagementPlane_ServiceNodePort(t *testing.T) {
	objs := must(t, "./"+mp, append(mpRequired, "service.type=NodePort", "service.nodePort=30443")...)
	svc := find(objs, "Service", "sextant")
	port := dig(svc, "spec", "ports").([]any)[0]
	if dig(port, "nodePort") != float64(30443) {
		t.Fatalf("nodePort = %v", dig(port, "nodePort"))
	}
	if _, err := render(t, "./"+mp, append(mpRequired, "service.nodePort=30443")...); err == nil {
		t.Fatal("nodePort accepted on a ClusterIP service")
	}
}

func TestAgent_RequiresHTTPSManagementURL(t *testing.T) {
	if _, err := render(t, "./"+agent); err == nil {
		t.Fatal("rendered without managementURL")
	}
	if _, err := render(t, "./"+agent, "managementURL=http://mp.example.com"); err == nil {
		t.Fatal("rendered with a plaintext managementURL")
	}
}

func TestAgent_LeastPrivilegeOwnSecretAccess(t *testing.T) {
	objs := must(t, "./"+agent, agentRequired...)
	role := find(objs, "Role", "sextant-agent")
	rules := dig(role, "rules").([]any)
	if len(rules) != 1 {
		t.Fatalf("agent Role has %d rules, want exactly 1", len(rules))
	}
	rule := rules[0].(map[string]any)
	names := rule["resourceNames"].([]any)
	if len(names) != 1 || !strings.HasSuffix(names[0].(string), "-credentials") {
		t.Fatalf("resourceNames = %v: access must be limited to the one credentials Secret", names)
	}
	verbs := map[string]bool{}
	for _, v := range rule["verbs"].([]any) {
		verbs[v.(string)] = true
	}
	if !verbs["get"] || !verbs["update"] || len(verbs) != 2 {
		t.Fatalf("verbs = %v, want exactly get+update (no create/list/delete/watch)", verbs)
	}
	// Nothing cluster-scoped is granted besides the access role binding.
	if got := ofKind(objs, "ClusterRole"); len(got) != 0 {
		t.Fatalf("chart defines %d ClusterRoles; it should only bind an existing one", len(got))
	}
}

func TestAgent_CredentialsSecretShippedEmptyAndKept(t *testing.T) {
	objs := must(t, "./"+agent, agentRequired...)
	sec := find(objs, "Secret", "credentials")
	if sec == nil {
		t.Fatal("credentials Secret not rendered")
	}
	if sec["data"] != nil || sec["stringData"] != nil {
		t.Fatal("chart must never set credentials data: helm upgrade would then reset the agent's identity")
	}
	if dig(sec, "metadata", "annotations", "helm.sh/resource-policy") != "keep" {
		t.Fatal("credentials Secret must survive helm uninstall/upgrade")
	}
}

func TestAgent_RegistrationTokenHandling(t *testing.T) {
	// No token given: no token Secret, and the env reference is optional.
	objs := must(t, "./"+agent, agentRequired...)
	if find(objs, "Secret", "registration") != nil {
		t.Fatal("registration Secret rendered without a token")
	}
	dep := find(objs, "Deployment", "sextant-agent")
	c := dig(dep, "spec", "template", "spec", "containers").([]any)[0].(map[string]any)
	var tokenEnv map[string]any
	for _, e := range c["env"].([]any) {
		if dig(e, "name") == "SEXTANT_REGISTRATION_TOKEN" {
			tokenEnv = e.(map[string]any)
		}
	}
	if dig(tokenEnv, "valueFrom", "secretKeyRef", "optional") != true {
		t.Fatal("the token reference must be optional: it is only needed for the first start")
	}
	// Token given: stored in a Secret and referenced, never inlined into the pod spec.
	objs = must(t, "./"+agent, append(agentRequired, "registration.token=sxt1.acme.s3cr3t")...)
	if find(objs, "Secret", "registration") == nil {
		t.Fatal("registration Secret missing")
	}
	b, _ := json.Marshal(find(objs, "Deployment", "sextant-agent"))
	if strings.Contains(string(b), "s3cr3t") {
		t.Fatal("registration token leaked into the Deployment")
	}
	// An existing Secret wins and nothing is created.
	objs = must(t, "./"+agent, append(agentRequired, "registration.token=x", "registration.existingSecret=mine")...)
	if find(objs, "Secret", "registration") != nil {
		t.Fatal("chart created a token Secret although existingSecret is set")
	}
}

func TestAgent_AccessRoleBindingDefaultsToViewAndCanBeDisabled(t *testing.T) {
	objs := must(t, "./"+agent, agentRequired...)
	crb := find(objs, "ClusterRoleBinding", "access")
	if crb == nil || dig(crb, "roleRef", "name") != "view" {
		t.Fatalf("default access binding = %v, want the view ClusterRole", crb)
	}
	objs = must(t, "./"+agent, append(agentRequired, "rbac.accessClusterRole=")...)
	if find(objs, "ClusterRoleBinding", "access") != nil {
		t.Fatal("access binding rendered although disabled")
	}
}

func TestAgent_SingleReplicaRecreate_NoPortsOpened(t *testing.T) {
	objs := must(t, "./"+agent, agentRequired...)
	dep := find(objs, "Deployment", "sextant-agent")
	if dig(dep, "spec", "replicas") != float64(1) || dig(dep, "spec", "strategy", "type") != "Recreate" {
		t.Fatal("agent must run as exactly one pod, replaced not rolled")
	}
	c := dig(dep, "spec", "template", "spec", "containers").([]any)[0].(map[string]any)
	if c["ports"] != nil {
		t.Fatal("the agent is outbound-only and must not declare any ports")
	}
	if len(ofKind(objs, "Service"))+len(ofKind(objs, "Ingress")) != 0 {
		t.Fatal("the agent chart must not expose anything")
	}
}
