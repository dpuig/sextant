package agent

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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the management plane's enrollment endpoints.
type Client struct {
	// BaseURL is the management plane, e.g. https://mp.example.com.
	BaseURL string
	// Roots verifies the management plane's server certificate; nil means the
	// system roots.
	Roots *x509.CertPool
}

func (c *Client) http(cert *tls.Certificate) *http.Client {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: c.Roots}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
}

// newKeyAndCSR makes a fresh P-256 key and a CSR for it. The CSR subject is
// irrelevant: the server decides the identity.
func newKeyAndCSR() (*ecdsa.PrivateKey, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "sextant-agent"}}, key)
	if err != nil {
		return nil, "", err
	}
	return key, base64.StdEncoding.EncodeToString(der), nil
}

// Enroll redeems token for the first credentials.
func (c *Client) Enroll(ctx context.Context, token string) (*Credentials, error) {
	key, csr, err := newKeyAndCSR()
	if err != nil {
		return nil, err
	}
	chain, err := c.post(ctx, nil, "/v1/enroll", map[string]string{"token": token, "csr": csr})
	if err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}
	return &Credentials{Key: key, Chain: chain}, nil
}

// Renew trades the current credentials (over mTLS) for new ones with a new key.
func (c *Client) Renew(ctx context.Context, cur *Credentials) (*Credentials, error) {
	key, csr, err := newKeyAndCSR()
	if err != nil {
		return nil, err
	}
	chain, err := c.post(ctx, cur.TLS(), "/v1/renew", map[string]string{"csr": csr})
	if err != nil {
		return nil, fmt.Errorf("renew: %w", err)
	}
	return &Credentials{Key: key, Chain: chain}, nil
}

func (c *Client) post(ctx context.Context, cert *tls.Certificate, path string, body any) ([][]byte, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.http(cert)
	defer hc.CloseIdleConnections() // one transport per call; do not leak its connections
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		// The server's message is generic by design; never echo the request body (it holds the token).
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return nil, fmt.Errorf("management plane returned %d: %s", resp.StatusCode, e.Message)
	}
	var out struct {
		Certificate []string `json:"certificate"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Certificate) < 1 {
		return nil, fmt.Errorf("unexpected response from management plane")
	}
	chain := make([][]byte, 0, len(out.Certificate))
	for _, s := range out.Certificate {
		der, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("unexpected response from management plane")
		}
		chain = append(chain, der)
	}
	return chain, nil
}
