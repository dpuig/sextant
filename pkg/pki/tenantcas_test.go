package pki_test

import (
	"context"
	"crypto"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/pki/localfile"
)

func TestTenantCAs_CachesPerTenant(t *testing.T) {
	now := time.Now()
	cas := pki.NewTenantCAs(newAuthority(t, now), 7*24*time.Hour)
	ctx := context.Background()
	a1, err := cas.For(ctx, tenant(t, "acme"))
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := cas.For(ctx, tenant(t, "acme"))
	b, _ := cas.For(ctx, tenant(t, "globex"))
	if a1 != a2 {
		t.Fatal("same tenant should reuse its CA")
	}
	if a1 == b || a1.Cert.Equal(b.Cert) {
		t.Fatal("tenants must not share a CA")
	}
}

func TestTenantCAs_ReissuesAfterHalfLife(t *testing.T) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	root, err := localfile.Generate("test-root")
	if err != nil {
		t.Fatal(err)
	}
	a := pki.NewAuthority(root, pki.WithClock(clock))
	cas := pki.NewTenantCAs(a, 10*time.Hour)
	first, _ := cas.For(context.Background(), tenant(t, "acme"))

	mu.Lock()
	now = now.Add(4 * time.Hour) // before half-life
	mu.Unlock()
	same, _ := cas.For(context.Background(), tenant(t, "acme"))
	if same != first {
		t.Fatal("reissued before half-life")
	}

	mu.Lock()
	now = now.Add(2 * time.Hour) // 6h > 5h half-life
	mu.Unlock()
	next, _ := cas.For(context.Background(), tenant(t, "acme"))
	if next == first {
		t.Fatal("did not reissue after half-life")
	}
	// The renewed CA must still be able to issue (valid now) even though the old one is nearing expiry.
	if _, err := next.IssueAgentCert(csr(t), "c1", time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestTenantCAs_ConcurrentForIsSafe(t *testing.T) {
	cas := pki.NewTenantCAs(newAuthority(t, time.Now()), time.Hour*24)
	var wg sync.WaitGroup
	got := make(chan *pki.TenantCA, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := cas.For(context.Background(), tenant(t, "acme"))
			if err != nil {
				t.Error(err)
				return
			}
			got <- c
		}()
	}
	wg.Wait()
	close(got)
	var first *pki.TenantCA
	for c := range got {
		if first == nil {
			first = c
		} else if c != first {
			t.Fatal("concurrent callers got different CAs for one tenant")
		}
	}
}

// blockingRoot blocks the first Sign call until released; later calls pass through.
type blockingRoot struct {
	*localfile.Root
	first   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (r *blockingRoot) Sign(rnd io.Reader, d []byte, o crypto.SignerOpts) ([]byte, error) {
	if r.first.CompareAndSwap(false, true) { // only the first caller blocks (sync.Once would block all)
		close(r.entered)
		<-r.release
	}
	return r.Root.Sign(rnd, d, o)
}

func TestTenantCAs_SlowTenantDoesNotBlockOthers(t *testing.T) {
	inner, _ := localfile.Generate("test-root")
	root := &blockingRoot{Root: inner, entered: make(chan struct{}), release: make(chan struct{})}
	cas := pki.NewTenantCAs(pki.NewAuthority(root), time.Hour*24)

	slow := make(chan error, 1)
	go func() { _, err := cas.For(context.Background(), tenant(t, "acme")); slow <- err }()
	<-root.entered // acme's issuance is now stuck inside the root signer

	other := make(chan error, 1)
	go func() { _, err := cas.For(context.Background(), tenant(t, "globex")); other <- err }()
	select {
	case err := <-other:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("globex was blocked behind acme's slow issuance")
	}
	close(root.release)
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
}
