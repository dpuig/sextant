package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/dpuig/sextant/pkg/tenancy"
)

// StaticAuth authenticates one bearer token and authorizes its holder for
// every verb on one tenant. It exists for local development and e2e tests
// until the identity broker lands (E2); it must never serve production.
type StaticAuth struct {
	token  string
	tenant tenancy.ID
}

// NewStaticAuth returns a StaticAuth; token must be at least 16 bytes.
func NewStaticAuth(token string, tenant tenancy.ID) (*StaticAuth, error) {
	if len(token) < 16 {
		return nil, errors.New("server: static token must be at least 16 bytes")
	}
	if tenant == (tenancy.ID{}) {
		return nil, tenancy.ErrNoTenant
	}
	return &StaticAuth{token: token, tenant: tenant}, nil
}

func (s *StaticAuth) Authenticate(r *http.Request) (Principal, error) {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		return Principal{}, errors.New("bad credentials")
	}
	return Principal{Subject: "dev:" + s.tenant.String()}, nil
}

func (s *StaticAuth) Authorize(_ Principal, tenant tenancy.ID, _, _ string) bool {
	return tenant == s.tenant
}

// DenyAll is the default Authorizer.
type DenyAll struct{}

func (DenyAll) Authorize(Principal, tenancy.ID, string, string) bool { return false }
