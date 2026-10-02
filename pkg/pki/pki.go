// Package pki issues the certificates that identify agents. A Root (backed by
// a local file, Vault, or later a cloud KMS) signs one name-constrained
// intermediate per tenant; each tenant intermediate signs short-lived agent
// client certificates. The root key never leaves its Root implementation.
package pki

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/dpuig/sextant/pkg/tenancy"
)

// MaxAgentCertTTL caps agent certificate lifetime (spec: 24h, rotate at 50%).
const MaxAgentCertTTL = 24 * time.Hour

// tenantDomain is the URI-SAN host suffix; each tenant gets <tenant>.<suffix>
// and its intermediate is name-constrained to exactly that host.
const tenantDomain = "tenants.sextant.internal"

var agentNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Root is the trust anchor. Signing goes through crypto.Signer so a KMS or
// Vault implementation can sign without exposing key material.
type Root interface {
	crypto.Signer
	Certificate() *x509.Certificate
}

// Authority issues per-tenant CAs from a Root.
type Authority struct {
	root Root
	now  func() time.Time
}

// Option customises an Authority.
type Option func(*Authority)

// WithClock overrides the time source (tests).
func WithClock(now func() time.Time) Option { return func(a *Authority) { a.now = now } }

// NewAuthority returns an Authority signing with root.
func NewAuthority(root Root, opts ...Option) *Authority {
	a := &Authority{root: root, now: time.Now}
	for _, o := range opts {
		o(a)
	}
	return a
}

// TenantCA is a tenant's intermediate certificate and signing key.
type TenantCA struct {
	Cert *x509.Certificate
	Root *x509.Certificate
	key  *ecdsa.PrivateKey

	tenant       tenancy.ID
	identityHost string
	now          func() time.Time
}

// IssueTenantCA creates an intermediate for tenant, valid for ttl, constrained
// to that tenant's URI namespace and unable to issue further CAs.
func (a *Authority) IssueTenantCA(ctx context.Context, tenant tenancy.ID, ttl time.Duration) (*TenantCA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenant == (tenancy.ID{}) {
		return nil, tenancy.ErrNoTenant
	}
	rootCert := a.root.Certificate()
	if rootCert == nil || !rootCert.IsCA {
		return nil, errors.New("pki: root certificate is missing or not a CA")
	}
	if ttl <= 0 {
		return nil, errors.New("pki: tenant CA ttl must be positive")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pki: generate tenant key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	now := a.now()
	notAfter := now.Add(ttl)
	if notAfter.After(rootCert.NotAfter) {
		notAfter = rootCert.NotAfter
	}
	host := hostFor(tenant)
	tmpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: "sextant tenant CA " + tenant.String()},
		NotBefore:                   now.Add(-time.Minute),
		NotAfter:                    notAfter,
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedURIDomains:         []string{host},
		PermittedDNSDomainsCritical: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, rootCert, &key.PublicKey, a.root)
	if err != nil {
		return nil, fmt.Errorf("pki: sign tenant CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &TenantCA{Cert: cert, Root: rootCert, key: key, tenant: tenant, identityHost: host, now: a.now}, nil
}

// IssueAgentCert signs the public key in csrDER as client certificate for the
// named agent. Identity is taken from tenant and agentName, never from the CSR
// subject or SANs.
func (c *TenantCA) IssueAgentCert(csrDER []byte, agentName string, ttl time.Duration) ([]byte, error) {
	if ttl <= 0 || ttl > MaxAgentCertTTL {
		return nil, fmt.Errorf("pki: agent cert ttl %s outside (0, %s]", ttl, MaxAgentCertTTL)
	}
	if !agentNamePattern.MatchString(agentName) {
		return nil, fmt.Errorf("pki: invalid agent name %q", agentName)
	}
	req, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("pki: parse csr: %w", err)
	}
	if err := req.CheckSignature(); err != nil {
		return nil, fmt.Errorf("pki: csr signature: %w", err)
	}
	if err := CheckPublicKey(req.PublicKey); err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	now := c.now()
	if now.Before(c.Cert.NotBefore) || !now.Before(c.Cert.NotAfter) {
		return nil, errors.New("pki: tenant CA is not currently valid")
	}
	notAfter := now.Add(ttl)
	if notAfter.After(c.Cert.NotAfter) {
		notAfter = c.Cert.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: agentName},
		URIs:         []*url.URL{{Scheme: "sextant", Host: c.identityHost, Path: "/agent/" + agentName}},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, req.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("pki: sign agent cert: %w", err)
	}
	return der, nil
}

func hostFor(t tenancy.ID) string { return t.String() + "." + tenantDomain }

func newSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("pki: serial: %w", err)
	}
	return n, nil
}

// ParseAgentIdentity extracts the tenant and agent name from a certificate
// issued by IssueAgentCert. It accepts exactly one sextant:// URI SAN of the
// form sextant://<tenant>.tenants.sextant.internal/agent/<name> and rejects
// anything else, so a certificate that is merely trusted but not an agent
// identity can never authenticate as one. Chain verification is the caller's
// (TLS layer's) job; this only reads the identity.
func ParseAgentIdentity(cert *x509.Certificate) (tenancy.ID, string, error) {
	if len(cert.URIs) != 1 {
		return tenancy.ID{}, "", fmt.Errorf("pki: want exactly one URI SAN, got %d", len(cert.URIs))
	}
	u := cert.URIs[0]
	tenantStr, ok := strings.CutSuffix(u.Host, "."+tenantDomain)
	if u.Scheme != "sextant" || !ok || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return tenancy.ID{}, "", errors.New("pki: not a sextant agent identity")
	}
	tid, err := tenancy.ParseID(tenantStr)
	if err != nil {
		return tenancy.ID{}, "", fmt.Errorf("pki: identity tenant: %w", err)
	}
	name, ok := strings.CutPrefix(u.EscapedPath(), "/agent/")
	if !ok || !agentNamePattern.MatchString(name) {
		return tenancy.ID{}, "", errors.New("pki: identity path is not /agent/<name>")
	}
	return tid, name, nil
}

// CheckPublicKey accepts only ECDSA P-256/P-384 and Ed25519 public keys.
func CheckPublicKey(pub any) error {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if k.Curve == elliptic.P256() || k.Curve == elliptic.P384() {
			return nil
		}
	case ed25519.PublicKey:
		return nil
	}
	return errors.New("pki: unsupported public key (want ECDSA P-256/P-384 or Ed25519)")
}
