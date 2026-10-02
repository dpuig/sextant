package tenancy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple", "acme", false},
		{"with dash and digits", "acme-corp-2", false},
		{"empty", "", true},
		{"uppercase", "Acme", true},
		{"leading dash", "-acme", true},
		{"trailing dash", "acme-", true},
		{"slash would escape a path", "a/b", true},
		{"dot-dot", "..", true},
		{"nats wildcard", "a*", true},
		{"nats separator", "a.b", true},
		{"space", "a b", true},
		{"too long", strings.Repeat("a", 64), true},
		{"max length", strings.Repeat("a", 63), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ParseID(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseID(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err == nil && id.String() != tt.in {
				t.Fatalf("round trip: got %q want %q", id, tt.in)
			}
		})
	}
}

func TestFromContext_MissingTenant(t *testing.T) {
	_, err := FromContext(context.Background())
	if !errors.Is(err, ErrNoTenant) {
		t.Fatalf("err = %v, want ErrNoTenant", err)
	}
}

func TestWithTenant_RoundTrip(t *testing.T) {
	id, _ := ParseID("acme")
	got, err := FromContext(WithTenant(context.Background(), id))
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("got %q want %q", got, id)
	}
}

func TestZeroIDIsRejectedByFromContext(t *testing.T) {
	ctx := WithTenant(context.Background(), ID{})
	if _, err := FromContext(ctx); !errors.Is(err, ErrNoTenant) {
		t.Fatalf("zero ID must not count as a tenant, err = %v", err)
	}
}

func TestSubject(t *testing.T) {
	id, _ := ParseID("acme")
	if got, want := id.Subject("cluster", "connected"), "sextant.acme.cluster.connected"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
