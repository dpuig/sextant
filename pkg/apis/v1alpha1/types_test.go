package v1alpha1

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func meta(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name} }

func TestValidate(t *testing.T) {
	soon := metav1.NewTime(time.Now().Add(time.Hour))
	tests := []struct {
		name    string
		obj     Resource
		wantErr string
	}{
		{"org ok", &Organization{ObjectMeta: meta("acme")}, ""},
		{"org bad name", &Organization{ObjectMeta: meta("Acme")}, "name"},
		{"workspace ok", &Workspace{ObjectMeta: meta("platform")}, ""},
		{"environment ok", &Environment{ObjectMeta: meta("prod"), Spec: EnvironmentSpec{Workspace: "platform", Criticality: "high"}}, ""},
		{"environment needs workspace", &Environment{ObjectMeta: meta("prod")}, "workspace"},
		{"environment bad criticality", &Environment{ObjectMeta: meta("prod"), Spec: EnvironmentSpec{Workspace: "w", Criticality: "extreme"}}, "criticality"},
		{"cluster ok", &Cluster{ObjectMeta: meta("c1"), Spec: ClusterSpec{Environment: "prod"}}, ""},
		{"cluster needs environment", &Cluster{ObjectMeta: meta("c1")}, "environment"},
		{"grant ok", &AccessGrant{ObjectMeta: meta("g1"), Spec: AccessGrantSpec{Subject: "alice@example.com", Cluster: "c1", Role: "view", ExpiresAt: &soon}}, ""},
		{"grant needs subject", &AccessGrant{ObjectMeta: meta("g1"), Spec: AccessGrantSpec{Cluster: "c1", Role: "view"}}, "subject"},
		{"grant bad role", &AccessGrant{ObjectMeta: meta("g1"), Spec: AccessGrantSpec{Subject: "a", Cluster: "c1", Role: "root"}}, "role"},
		{"name too long", &Workspace{ObjectMeta: meta(strings.Repeat("a", 64))}, "name"},
		{"name with slash", &Workspace{ObjectMeta: meta("a/b")}, "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.obj.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestKinds_AreRegisteredWithUniquePlurals(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Kinds() {
		if seen[k.Plural] {
			t.Fatalf("duplicate plural %q", k.Plural)
		}
		seen[k.Plural] = true
		r := k.New()
		if r.KindName() != k.Kind {
			t.Fatalf("%s: New().KindName() = %q", k.Kind, r.KindName())
		}
	}
	if len(seen) != 5 {
		t.Fatalf("registered %d kinds, want 5", len(seen))
	}
}
