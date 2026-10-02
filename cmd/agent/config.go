package main

import (
	"errors"
	"flag"
	"fmt"
	fs2 "io/fs"
	"net/url"
	"strings"
)

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

type config struct {
	managementURL *url.URL
	serverCAFile  string // optional; system roots when empty
	stateDir      string
	token         string // registration token; empty once enrolled
	kubeAPI       *url.URL
	kubeCAFile    string
	kubeTokenFile string
	// credentialsSecret, when set, stores credentials in this Secret (in
	// namespace) instead of stateDir, so they survive pod restarts.
	credentialsSecret string
	namespace         string
}

// tunnelURL is where the agent dials the tunnel (wss://host/connect).
func (c *config) tunnelURL() string {
	u := *c.managementURL
	u.Scheme = "wss"
	u.Path = strings.TrimRight(u.Path, "/") + "/connect"
	return u.String()
}

// parseConfig reads flags and environment. Secrets (the registration token)
// come from the environment or a file, never a flag: flags show up in ps.
func parseConfig(args []string, getenv func(string) string, readFile func(string) ([]byte, error)) (*config, error) {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	mgmt := fs.String("management-url", getenv("SEXTANT_MANAGEMENT_URL"), "management plane base URL (https://...)")
	serverCA := fs.String("server-ca-file", getenv("SEXTANT_SERVER_CA_FILE"), "CA bundle to verify the management plane (default: system roots)")
	state := fs.String("state-dir", orDefault(getenv("SEXTANT_STATE_DIR"), "/var/lib/sextant-agent"), "directory for agent credentials")
	tokenFile := fs.String("token-file", getenv("SEXTANT_REGISTRATION_TOKEN_FILE"), "file containing the registration token")
	kubeAPI := fs.String("kube-api", getenv("SEXTANT_KUBE_API"), "kube-apiserver URL (default: in-cluster)")
	kubeCA := fs.String("kube-ca-file", orDefault(getenv("SEXTANT_KUBE_CA_FILE"), saDir+"/ca.crt"), "kube-apiserver CA file")
	kubeToken := fs.String("kube-token-file", orDefault(getenv("SEXTANT_KUBE_TOKEN_FILE"), saDir+"/token"), "service account token file")
	secret := fs.String("credentials-secret", getenv("SEXTANT_CREDENTIALS_SECRET"), "store credentials in this Secret instead of --state-dir")
	ns := fs.String("namespace", getenv("SEXTANT_NAMESPACE"), "namespace of the credentials Secret (default: the pod's namespace)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	c := &config{credentialsSecret: *secret, namespace: *ns, serverCAFile: *serverCA, stateDir: *state, kubeCAFile: *kubeCA, kubeTokenFile: *kubeToken}

	if *mgmt == "" {
		return nil, errors.New("--management-url (or SEXTANT_MANAGEMENT_URL) is required")
	}
	mu, err := url.Parse(*mgmt)
	if err != nil || mu.Scheme != "https" || mu.Host == "" {
		return nil, fmt.Errorf("management URL must be https://host[:port], got %q", *mgmt)
	}
	c.managementURL = mu

	switch {
	case *kubeAPI != "":
		c.kubeAPI, err = url.Parse(*kubeAPI)
		if err != nil || c.kubeAPI.Scheme != "https" {
			return nil, fmt.Errorf("--kube-api must be an https URL, got %q", *kubeAPI)
		}
	case getenv("KUBERNETES_SERVICE_HOST") != "":
		host, port := getenv("KUBERNETES_SERVICE_HOST"), orDefault(getenv("KUBERNETES_SERVICE_PORT"), "443")
		c.kubeAPI = &url.URL{Scheme: "https", Host: joinHostPort(host, port)}
	default:
		return nil, errors.New("not running in a cluster: set --kube-api")
	}

	if c.credentialsSecret != "" && c.namespace == "" {
		b, err := readFile(saDir + "/namespace")
		if err != nil || strings.TrimSpace(string(b)) == "" {
			return nil, errors.New("--credentials-secret needs a namespace: set --namespace (the pod namespace file is unreadable)")
		}
		c.namespace = strings.TrimSpace(string(b))
	}

	// The token file wins over the environment variable; either may be absent
	// once the agent has stored credentials.
	if *tokenFile != "" {
		b, err := readFile(*tokenFile)
		switch {
		case errors.Is(err, fs2.ErrNotExist):
			// Normal once enrolled: the Secret/file may be gone. If credentials are
			// stored the agent runs; if not, the manager reports the missing token.
		case err != nil:
			return nil, fmt.Errorf("read token file: %w", err)
		default:
			c.token = strings.TrimSpace(string(b))
		}
	} else {
		c.token = strings.TrimSpace(getenv("SEXTANT_REGISTRATION_TOKEN"))
	}
	return c, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func joinHostPort(host, port string) string {
	if strings.Contains(host, ":") { // IPv6 literal
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}
