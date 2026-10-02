package pki_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/url"
	"testing"
	"time"

	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/pki/localfile"
	"github.com/dpuig/sextant/pkg/tenancy"
)

func newAuthority(t *testing.T, now time.Time) *pki.Authority {
	t.Helper()
	root, err := localfile.Generate("sextant-test-root")
	if err != nil {
		t.Fatal(err)
	}
	return pki.NewAuthority(root, pki.WithClock(func() time.Time { return now }))
}

func tenant(t *testing.T, s string) tenancy.ID {
	t.Helper()
	id, err := tenancy.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func csr(t *testing.T) []byte {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "attacker-chosen-name"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func verify(leaf *x509.Certificate, ca *pki.TenantCA, at time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(ca.Root)
	inter := x509.NewCertPool()
	inter.AddCert(ca.Cert)
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err
}

func TestAgentCert_ChainsToRootThroughTenantIntermediate(t *testing.T) {
	now := time.Now()
	a := newAuthority(t, now)
	ca, err := a.IssueTenantCA(context.Background(), tenant(t, "acme"), 365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	der, err := ca.IssueAgentCert(csr(t), "cluster-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	if err := verify(leaf, ca, now.Add(time.Minute)); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got := leaf.URIs[0].String(); got != "sextant://acme.tenants.sextant.internal/agent/cluster-1" {
		t.Fatalf("identity URI = %q", got)
	}
}

func TestAgentCert_IdentityComesFromServerNotCSR(t *testing.T) {
	a := newAuthority(t, time.Now())
	ca, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	der, _ := ca.IssueAgentCert(csr(t), "cluster-1", time.Hour)
	leaf, _ := x509.ParseCertificate(der)
	if leaf.Subject.CommonName == "attacker-chosen-name" {
		t.Fatal("CSR subject leaked into issued certificate")
	}
}

func TestAgentCert_RejectsTTLOverMax(t *testing.T) {
	a := newAuthority(t, time.Now())
	ca, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	if _, err := ca.IssueAgentCert(csr(t), "c", pki.MaxAgentCertTTL+time.Second); err == nil {
		t.Fatal("expected TTL over max to be rejected")
	}
	if _, err := ca.IssueAgentCert(csr(t), "c", 0); err == nil {
		t.Fatal("expected zero TTL to be rejected")
	}
}

func TestAgentCert_RejectsBadCSRAndBadName(t *testing.T) {
	a := newAuthority(t, time.Now())
	ca, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	if _, err := ca.IssueAgentCert([]byte("garbage"), "c", time.Hour); err == nil {
		t.Fatal("expected garbage CSR rejected")
	}
	for _, name := range []string{"", "a/b", "..", "a b"} {
		if _, err := ca.IssueAgentCert(csr(t), name, time.Hour); err == nil {
			t.Fatalf("expected agent name %q rejected", name)
		}
	}
}

func TestAgentCert_ExpiresAtTTL(t *testing.T) {
	now := time.Now()
	a := newAuthority(t, now)
	ca, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	der, _ := ca.IssueAgentCert(csr(t), "c", time.Hour)
	leaf, _ := x509.ParseCertificate(der)
	if err := verify(leaf, ca, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expected expired cert to fail verification")
	}
}

// The core tenant-isolation property of the PKI: a cert issued by tenant A's
// intermediate must not validate under tenant B's intermediate.
func TestCrossTenant_CertDoesNotVerifyAgainstOtherTenantCA(t *testing.T) {
	now := time.Now()
	a := newAuthority(t, now)
	caA, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	caB, _ := a.IssueTenantCA(context.Background(), tenant(t, "globex"), time.Hour*24)
	der, _ := caA.IssueAgentCert(csr(t), "c", time.Hour)
	leaf, _ := x509.ParseCertificate(der)
	if err := verify(leaf, caB, now.Add(time.Minute)); err == nil {
		t.Fatal("tenant A leaf verified under tenant B intermediate")
	}
}

// Even if a tenant intermediate's key were abused, name constraints stop it
// from minting identities in another tenant's namespace.
func TestTenantCA_NameConstraintBlocksForeignTenantIdentity(t *testing.T) {
	now := time.Now()
	a := newAuthority(t, now)
	caA, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	caA.SetIdentityHostForTest("globex.tenants.sextant.internal")
	der, err := caA.IssueAgentCert(csr(t), "c", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	if err := verify(leaf, caA, now.Add(time.Minute)); err == nil {
		t.Fatal("name constraint did not block foreign-tenant URI SAN")
	}
}

func TestTenantCA_CannotIssueIntermediates(t *testing.T) {
	a := newAuthority(t, time.Now())
	ca, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	if ca.Cert.MaxPathLen != 0 || !ca.Cert.MaxPathLenZero {
		t.Fatalf("intermediate must have pathlen 0, got %d (zero=%v)", ca.Cert.MaxPathLen, ca.Cert.MaxPathLenZero)
	}
}

func TestAgentCert_RejectsExpiredTenantCA(t *testing.T) {
	now := time.Now()
	a := newAuthority(t, now)
	ca, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour)
	expired := ca.WithClockForTest(now.Add(2 * time.Hour))
	if _, err := expired.IssueAgentCert(csr(t), "c", time.Hour); err == nil {
		t.Fatal("expected issuing from an expired tenant CA to fail")
	}
}

func TestTenantCA_NotAfterCappedAtRoot(t *testing.T) {
	now := time.Now()
	a := newAuthority(t, now)
	ca, err := a.IssueTenantCA(context.Background(), tenant(t, "acme"), 100*365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ca.Cert.NotAfter.After(ca.Root.NotAfter) {
		t.Fatalf("tenant CA NotAfter %v exceeds root %v", ca.Cert.NotAfter, ca.Root.NotAfter)
	}
}

func TestParseAgentIdentity_RoundTrip(t *testing.T) {
	a := newAuthority(t, time.Now())
	ca, _ := a.IssueTenantCA(context.Background(), tenant(t, "acme"), time.Hour*24)
	der, _ := ca.IssueAgentCert(csr(t), "cluster-1", time.Hour)
	leaf, _ := x509.ParseCertificate(der)
	tid, name, err := pki.ParseAgentIdentity(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if tid.String() != "acme" || name != "cluster-1" {
		t.Fatalf("got %q %q", tid, name)
	}
}

func TestParseAgentIdentity_RejectsNonAgentCerts(t *testing.T) {
	mk := func(uris ...string) *x509.Certificate {
		c := &x509.Certificate{}
		for _, u := range uris {
			p, _ := url.Parse(u)
			c.URIs = append(c.URIs, p)
		}
		return c
	}
	for name, c := range map[string]*x509.Certificate{
		"no uri":           mk(),
		"wrong scheme":     mk("https://acme.tenants.sextant.internal/agent/c1"),
		"wrong domain":     mk("sextant://acme.example.com/agent/c1"),
		"bad tenant":       mk("sextant://Acme.tenants.sextant.internal/agent/c1"),
		"not an agent":     mk("sextant://acme.tenants.sextant.internal/user/c1"),
		"bad agent name":   mk("sextant://acme.tenants.sextant.internal/agent/a%2Fb"),
		"extra path":       mk("sextant://acme.tenants.sextant.internal/agent/c1/x"),
		"two identities":   mk("sextant://acme.tenants.sextant.internal/agent/c1", "sextant://globex.tenants.sextant.internal/agent/c1"),
		"nested subdomain": mk("sextant://x.acme.tenants.sextant.internal/agent/c1"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := pki.ParseAgentIdentity(c); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
