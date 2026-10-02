package pki

import (
	"context"
	"sync"
	"time"

	"github.com/dpuig/sextant/pkg/tenancy"
)

// TenantCAs hands out each tenant's intermediate CA, issuing it lazily and
// renewing it once half its lifetime has passed.
//
// Intermediates are deliberately not persisted: they live in process memory,
// every replica issues its own, and verifiers trust only the root, so any
// root-signed, name-constrained intermediate chains correctly. That keeps
// CA private keys out of the database and the management plane stateless.
// The cost is that a leaf's chain depends on the intermediate that signed it,
// which agents always present alongside their leaf.
type TenantCAs struct {
	auth *Authority
	ttl  time.Duration

	mu      sync.Mutex // guards the map only; never held while signing
	tenants map[tenancy.ID]*entry
}

// entry has its own lock so issuing one tenant's CA (which calls the root
// signer, possibly a slow KMS) never blocks another tenant.
type entry struct {
	mu sync.Mutex
	ca *TenantCA
}

// NewTenantCAs returns a provider whose intermediates live for ttl.
func NewTenantCAs(a *Authority, ttl time.Duration) *TenantCAs {
	return &TenantCAs{auth: a, ttl: ttl, tenants: map[tenancy.ID]*entry{}}
}

// For returns tenant's current CA, issuing or renewing it if needed.
func (c *TenantCAs) For(ctx context.Context, tenant tenancy.ID) (*TenantCA, error) {
	c.mu.Lock()
	e, ok := c.tenants[tenant]
	if !ok {
		e = &entry{}
		c.tenants[tenant] = e
	}
	c.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ca != nil {
		half := e.ca.Cert.NotBefore.Add(e.ca.Cert.NotAfter.Sub(e.ca.Cert.NotBefore) / 2)
		if c.auth.now().Before(half) {
			return e.ca, nil
		}
	}
	ca, err := c.auth.IssueTenantCA(ctx, tenant, c.ttl)
	if err != nil {
		return nil, err
	}
	e.ca = ca
	return ca, nil
}
