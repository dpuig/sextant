package agent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

// NewKubeProxy returns a handler that forwards requests to the kube-apiserver
// at apiURL as the agent's own service account. caFile verifies the apiserver;
// tokenFile holds the projected service-account token and is re-read on every
// request because Kubernetes rotates it.
//
// Whatever Authorization or Impersonate-* headers the caller sent are
// discarded, so the management plane cannot make the agent act as anyone but
// itself, whatever RBAC the agent's service account happens to hold. E2 will
// add deliberate impersonation here, set by the broker.
func NewKubeProxy(apiURL *url.URL, caFile, tokenFile string, log *slog.Logger) (http.Handler, error) {
	if apiURL == nil || apiURL.Scheme != "https" {
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
	if log == nil {
		log = slog.Default()
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(apiURL)
			r.Out.Header.Del("Authorization")
			for name := range r.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
					r.Out.Header.Del(name)
				}
			}
			if tok, err := os.ReadFile(tokenFile); err == nil {
				r.Out.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
			}
			// If the token cannot be read the request goes out unauthenticated
			// and the apiserver answers 401, which is the honest outcome.
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
			// No ResponseHeaderTimeout: watch and exec streams legitimately idle.
		},
		FlushInterval: -1, // stream watch/log responses immediately
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("kube-apiserver request failed", "path", r.URL.Path, "err", err)
			http.Error(w, "kube-apiserver unreachable", http.StatusBadGateway)
		},
	}, nil
}
