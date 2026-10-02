// Package tenancy carries the tenant identity through every layer. A tenant ID
// is never optional: storage and API code take it from the context and fail
// closed when it is absent.
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrNoTenant is returned when a context carries no (or an empty) tenant.
var ErrNoTenant = errors.New("tenancy: no tenant in context")

// The charset is deliberately narrow: an ID ends up in URL paths, SQL keys,
// NATS subjects and certificate subjects, so it must be inert in all four
// ('.', '*', '>' and '/' are excluded).
var idPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ID identifies a tenant (an Organization). The zero value is invalid.
type ID struct{ v string }

// ParseID validates s and returns it as an ID.
func ParseID(s string) (ID, error) {
	if !idPattern.MatchString(s) {
		return ID{}, fmt.Errorf("tenancy: invalid tenant id %q: must match %s", s, idPattern)
	}
	return ID{v: s}, nil
}

func (i ID) String() string { return i.v }

// Subject builds a tenant-namespaced NATS subject: sextant.<tenant>.<tokens...>.
func (i ID) Subject(tokens ...string) string {
	return strings.Join(append([]string{"sextant", i.v}, tokens...), ".")
}

type ctxKey struct{}

// WithTenant returns a context carrying id.
func WithTenant(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the tenant in ctx, or ErrNoTenant.
func FromContext(ctx context.Context) (ID, error) {
	id, ok := ctx.Value(ctxKey{}).(ID)
	if !ok || id.v == "" {
		return ID{}, ErrNoTenant
	}
	return id, nil
}
