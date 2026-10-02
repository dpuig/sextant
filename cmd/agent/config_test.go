package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func noFiles(string) ([]byte, error) { return nil, errors.New("no such file") }

func TestParseConfig_InClusterDefaults(t *testing.T) {
	c, err := parseConfig(nil, env(map[string]string{
		"SEXTANT_MANAGEMENT_URL":     "https://mp.example.com",
		"KUBERNETES_SERVICE_HOST":    "10.96.0.1",
		"KUBERNETES_SERVICE_PORT":    "443",
		"SEXTANT_REGISTRATION_TOKEN": " sxt1.acme.abc \n",
	}), noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if c.kubeAPI.String() != "https://10.96.0.1:443" {
		t.Fatalf("kubeAPI = %s", c.kubeAPI)
	}
	if c.token != "sxt1.acme.abc" {
		t.Fatalf("token = %q (should be trimmed)", c.token)
	}
	if c.kubeCAFile != saDir+"/ca.crt" || c.kubeTokenFile != saDir+"/token" || c.stateDir != "/var/lib/sextant-agent" {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestParseConfig_IPv6ServiceHost(t *testing.T) {
	c, err := parseConfig(nil, env(map[string]string{
		"SEXTANT_MANAGEMENT_URL": "https://mp.example.com", "KUBERNETES_SERVICE_HOST": "fd00::1", "KUBERNETES_SERVICE_PORT": "6443",
	}), noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if c.kubeAPI.Host != "[fd00::1]:6443" {
		t.Fatalf("host = %s", c.kubeAPI.Host)
	}
}

func TestParseConfig_Rejections(t *testing.T) {
	base := map[string]string{"SEXTANT_MANAGEMENT_URL": "https://mp.example.com", "KUBERNETES_SERVICE_HOST": "10.0.0.1"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	tests := map[string]map[string]string{
		"no management url":   {"KUBERNETES_SERVICE_HOST": "10.0.0.1"},
		"http management url": with("SEXTANT_MANAGEMENT_URL", "http://mp.example.com"),
		"no host":             with("SEXTANT_MANAGEMENT_URL", "https://"),
		"not in cluster":      {"SEXTANT_MANAGEMENT_URL": "https://mp.example.com"},
		"http kube api":       with("SEXTANT_KUBE_API", "http://10.0.0.1"),
	}
	for name, e := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfig(nil, env(e), noFiles); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseConfig_TokenFileBeatsEnv_AndTokenNeverFromFlag(t *testing.T) {
	e := env(map[string]string{
		"SEXTANT_MANAGEMENT_URL": "https://mp.example.com", "KUBERNETES_SERVICE_HOST": "10.0.0.1",
		"SEXTANT_REGISTRATION_TOKEN": "from-env",
	})
	read := func(p string) ([]byte, error) { return []byte("from-file\n"), nil }
	c, err := parseConfig([]string{"--token-file", "/tok"}, e, read)
	if err != nil || c.token != "from-file" {
		t.Fatalf("token = %q err = %v", c.token, err)
	}
	// There is deliberately no --token flag.
	if _, err := parseConfig([]string{"--token", "x"}, e, noFiles); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("a --token flag exists or failed oddly: %v", err)
	}
	if _, err := parseConfig([]string{"--token-file", "/denied"}, e, noFiles); err == nil {
		t.Fatal("unreadable (non-missing) token file should be an error")
	}
	// A missing file is normal once the agent has enrolled.
	missing := func(string) ([]byte, error) { return nil, &os.PathError{Op: "open", Path: "/tok", Err: os.ErrNotExist} }
	c, err = parseConfig([]string{"--token-file", "/tok"}, e, missing)
	if err != nil || c.token != "" {
		t.Fatalf("missing token file: token=%q err=%v", c.token, err)
	}
}

func TestTunnelURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://mp.example.com":      "wss://mp.example.com/connect",
		"https://mp.example.com/":     "wss://mp.example.com/connect",
		"https://mp.example.com:8443": "wss://mp.example.com:8443/connect",
		"https://mp.example.com/sx":   "wss://mp.example.com/sx/connect",
	} {
		c, err := parseConfig(nil, env(map[string]string{"SEXTANT_MANAGEMENT_URL": in, "KUBERNETES_SERVICE_HOST": "10.0.0.1"}), noFiles)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.tunnelURL(); got != want {
			t.Fatalf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestParseConfig_CredentialsSecretNamespaceResolution(t *testing.T) {
	base := map[string]string{"SEXTANT_MANAGEMENT_URL": "https://mp.example.com", "KUBERNETES_SERVICE_HOST": "10.0.0.1", "SEXTANT_CREDENTIALS_SECRET": "creds"}
	podNS := func(p string) ([]byte, error) {
		if p == saDir+"/namespace" {
			return []byte("sextant-system\n"), nil
		}
		return nil, errors.New("no")
	}
	c, err := parseConfig(nil, env(base), podNS)
	if err != nil || c.namespace != "sextant-system" || c.credentialsSecret != "creds" {
		t.Fatalf("namespace=%q secret=%q err=%v", c.namespace, c.credentialsSecret, err)
	}
	if _, err := parseConfig(nil, env(base), noFiles); err == nil {
		t.Fatal("secret without any resolvable namespace accepted")
	}
	withNS := map[string]string{"SEXTANT_NAMESPACE": "explicit"}
	for k, v := range base {
		withNS[k] = v
	}
	if c, err := parseConfig(nil, env(withNS), noFiles); err != nil || c.namespace != "explicit" {
		t.Fatalf("explicit namespace: %q %v", c.namespace, err)
	}
	// Without a secret, no namespace is needed.
	delete(base, "SEXTANT_CREDENTIALS_SECRET")
	if _, err := parseConfig(nil, env(base), noFiles); err != nil {
		t.Fatalf("file-backed config should not need a namespace: %v", err)
	}
}
