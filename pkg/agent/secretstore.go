package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const secretKey = "agent-credentials.pem"

// SecretStore keeps credentials in a Kubernetes Secret, so they survive pod
// restarts (the registration token is single-use and cannot be replayed). It
// speaks plain REST to the kube-apiserver as the agent's service account,
// which needs get/create/update on that one Secret and nothing else.
type SecretStore struct {
	api       *url.URL
	http      *http.Client
	tokenFile string
	namespace string
	name      string
}

// NewSecretStore returns a store for secret namespace/name. caFile verifies
// the apiserver; tokenFile is the projected service-account token.
func NewSecretStore(api *url.URL, caFile, tokenFile, namespace, name string) (*SecretStore, error) {
	if api == nil || api.Scheme != "https" {
		return nil, errors.New("agent: kube-apiserver URL must be https")
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read apiserver CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("agent: apiserver CA file has no certificates")
	}
	return &SecretStore{
		api:       api,
		tokenFile: tokenFile, namespace: namespace, name: name,
		http: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		}},
	}, nil
}

func (s *SecretStore) path(name string) string {
	p := "/api/v1/namespaces/" + url.PathEscape(s.namespace) + "/secrets"
	if name != "" {
		p += "/" + url.PathEscape(name)
	}
	return p
}

type secretObj struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   secretMeta        `json:"metadata"`
	Type       string            `json:"type"`
	Data       map[string]string `json:"data"`
}

type secretMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

func (s *SecretStore) do(method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	u := *s.api
	u.Path = path
	req, err := http.NewRequestWithContext(context.Background(), method, u.String(), rdr)
	if err != nil {
		return 0, nil, err
	}
	tok, err := os.ReadFile(s.tokenFile)
	if err != nil {
		return 0, nil, fmt.Errorf("read service account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data, nil
}

// get returns the stored secret as generic JSON (so an update can preserve
// everything it does not own: labels, annotations such as Helm's
// resource-policy, other data keys), or nil if it does not exist.
func (s *SecretStore) get() (map[string]any, error) {
	code, data, err := s.do(http.MethodGet, s.path(s.name), nil)
	if err != nil {
		return nil, err
	}
	switch code {
	case http.StatusOK:
		var o map[string]any
		if err := json.Unmarshal(data, &o); err != nil {
			return nil, fmt.Errorf("decode secret: %w", err)
		}
		return o, nil
	case http.StatusNotFound:
		return nil, nil
	}
	return nil, fmt.Errorf("get secret: kube-apiserver returned %d", code)
}

func (s *SecretStore) Load() (*Credentials, error) {
	o, err := s.get()
	if err != nil || o == nil {
		return nil, err
	}
	data, _ := o["data"].(map[string]any)
	enc, ok := data[secretKey].(string)
	if !ok {
		return nil, nil
	}
	pemData, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("secret data is not base64: %w", err)
	}
	return decodeCredentials(pemData)
}

// Save replaces the Secret's contents, creating it if it does not exist. It
// looks before it writes so the agent's Role can be limited to get/update on
// this one named Secret (Kubernetes cannot restrict "create" by name).
func (s *SecretStore) Save(c *Credentials) error {
	pemData, err := encodeCredentials(c)
	if err != nil {
		return err
	}
	obj := secretObj{
		APIVersion: "v1", Kind: "Secret", Type: "Opaque",
		Metadata: secretMeta{Name: s.name, Namespace: s.namespace},
		Data:     map[string]string{secretKey: base64.StdEncoding.EncodeToString(pemData)},
	}
	for attempt := 0; attempt < 3; attempt++ {
		cur, err := s.get()
		if err != nil {
			return err
		}
		var code int
		if cur == nil {
			code, _, err = s.do(http.MethodPost, s.path(""), obj)
		} else {
			// Replace only our key; everything else on the object is preserved.
			data, _ := cur["data"].(map[string]any)
			if data == nil {
				data = map[string]any{}
			}
			data[secretKey] = obj.Data[secretKey]
			cur["data"] = data
			code, _, err = s.do(http.MethodPut, s.path(s.name), cur)
		}
		if err != nil {
			return err
		}
		switch code {
		case http.StatusOK, http.StatusCreated:
			return nil
		case http.StatusConflict: // lost a race with another writer: look again
			continue
		}
		return fmt.Errorf("save secret: kube-apiserver returned %d", code)
	}
	return errors.New("save secret: too many conflicts")
}
