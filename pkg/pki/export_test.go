package pki

import "time"

// SetIdentityHostForTest lets tests simulate a compromised or buggy issuer
// that tries to mint identities outside its tenant namespace.
func (c *TenantCA) SetIdentityHostForTest(h string) { c.identityHost = h }

func (c TenantCA) WithClockForTest(t time.Time) *TenantCA {
	c.now = func() time.Time { return t }
	return &c
}
