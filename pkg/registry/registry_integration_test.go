package registry_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dpuig/sextant/pkg/apis/v1alpha1"
	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/storage/storagetest"
	"github.com/dpuig/sextant/pkg/tenancy"
)

func newRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	return registry.New(storage.New(storagetest.NewPool(t)))
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	id, err := tenancy.ParseID(tenant)
	if err != nil {
		t.Fatal(err)
	}
	return tenancy.WithTenant(context.Background(), id)
}

func cluster(name string) *v1alpha1.Cluster {
	return &v1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"team": "payments"}}, Spec: v1alpha1.ClusterSpec{Environment: "prod"}}
}

func TestCreateGet_RoundTripAndServerOwnedFields(t *testing.T) {
	r := newRegistry(t)
	ctx := ctxFor(t, "acme")
	in := cluster("c1")
	in.Status.Connected = true // client tries to set status on create
	in.ResourceVersion = "999"
	out, err := r.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	c := out.(*v1alpha1.Cluster)
	if c.Status.Connected {
		t.Fatal("client-supplied status survived create")
	}
	if c.ResourceVersion == "999" || c.ResourceVersion == "" {
		t.Fatalf("resourceVersion = %q, must be server-assigned", c.ResourceVersion)
	}
	if c.Kind != "Cluster" || c.APIVersion != "sextant.andean.io/v1alpha1" {
		t.Fatalf("typemeta = %q %q", c.APIVersion, c.Kind)
	}
	got, _ := r.Get(ctx, "Cluster", "c1")
	if got.(*v1alpha1.Cluster).Spec.Environment != "prod" || got.GetLabels()["team"] != "payments" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestCreate_ValidationErrorsAreTyped(t *testing.T) {
	r := newRegistry(t)
	_, err := r.Create(ctxFor(t, "acme"), &v1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "c1"}})
	var ve *registry.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestUpdate_PreservesStatus_UpdateStatus_PreservesSpec(t *testing.T) {
	r := newRegistry(t)
	ctx := ctxFor(t, "acme")
	created, _ := r.Create(ctx, cluster("c1"))

	st := created.(*v1alpha1.Cluster)
	st.Status.Connected = true
	afterStatus, err := r.UpdateStatus(ctx, st)
	if err != nil {
		t.Fatal(err)
	}

	sp := afterStatus.(*v1alpha1.Cluster)
	sp.Spec.Provider = "eks"
	sp.Status.Connected = false // must be ignored by a spec update
	afterSpec, err := r.Update(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	c := afterSpec.(*v1alpha1.Cluster)
	if !c.Status.Connected || c.Spec.Provider != "eks" {
		t.Fatalf("spec update clobbered status or lost spec: %+v", c)
	}

	// Status update must not change spec.
	c.Spec.Provider = "hacked"
	c.Status.Connected = false
	afterStatus2, err := r.UpdateStatus(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := afterStatus2.(*v1alpha1.Cluster); got.Spec.Provider != "eks" || got.Status.Connected {
		t.Fatalf("status update changed spec or ignored status: %+v", got)
	}
}

func TestUpdate_RequiresCurrentResourceVersion(t *testing.T) {
	r := newRegistry(t)
	ctx := ctxFor(t, "acme")
	created, _ := r.Create(ctx, cluster("c1"))
	stale := cluster("c1")
	stale.ResourceVersion = created.GetResourceVersion()
	if _, err := r.Update(ctx, created); err != nil { // bumps version
		t.Fatal(err)
	}
	if _, err := r.Update(ctx, stale); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale update err = %v, want ErrConflict", err)
	}
	noRV := cluster("c1")
	var ve *registry.ValidationError
	if _, err := r.Update(ctx, noRV); !errors.As(err, &ve) {
		t.Fatalf("missing rv err = %v, want ValidationError", err)
	}
}

func TestOrganization_NameMustEqualTenant(t *testing.T) {
	r := newRegistry(t)
	var ve *registry.ValidationError
	_, err := r.Create(ctxFor(t, "acme"), &v1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "globex"}})
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if _, err := r.Create(ctxFor(t, "acme"), &v1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistry_TenantsAreIsolated(t *testing.T) {
	r := newRegistry(t)
	a, b := ctxFor(t, "acme"), ctxFor(t, "globex")
	_, _ = r.Create(a, cluster("c1"))
	if _, err := r.Get(b, "Cluster", "c1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("cross-tenant get err = %v", err)
	}
	if l, _ := r.List(b, "Cluster"); len(l) != 0 {
		t.Fatalf("cross-tenant list = %d", len(l))
	}
}

func TestUpdateStatus_DoesNotRequireSpec(t *testing.T) {
	r := newRegistry(t)
	ctx := ctxFor(t, "acme")
	created, _ := r.Create(ctx, cluster("c1"))
	statusOnly := &v1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "c1", ResourceVersion: created.GetResourceVersion()}}
	statusOnly.Status.Connected = true
	out, err := r.UpdateStatus(ctx, statusOnly)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(*v1alpha1.Cluster); !got.Status.Connected || got.Spec.Environment != "prod" {
		t.Fatalf("got %+v", got)
	}
}

func TestUpdateStatus_RejectedForKindsWithoutStatus(t *testing.T) {
	r := newRegistry(t)
	ctx := ctxFor(t, "acme")
	created, _ := r.Create(ctx, &v1alpha1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: "w1"}})
	var ve *registry.ValidationError
	if _, err := r.UpdateStatus(ctx, created); !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	got, _ := r.Get(ctx, "Workspace", "w1")
	if got.GetResourceVersion() != created.GetResourceVersion() {
		t.Fatal("rejected status update still bumped resourceVersion")
	}
}

func TestCreate_ValidatesLabels(t *testing.T) {
	r := newRegistry(t)
	ctx := ctxFor(t, "acme")
	var ve *registry.ValidationError
	bad := cluster("c1")
	bad.Labels = map[string]string{"bad key!": "v"}
	if _, err := r.Create(ctx, bad); !errors.As(err, &ve) {
		t.Fatalf("bad label key err = %v", err)
	}
	long := cluster("c2")
	long.Labels = map[string]string{"k": strings.Repeat("x", 64)}
	if _, err := r.Create(ctx, long); !errors.As(err, &ve) {
		t.Fatalf("long label value err = %v", err)
	}
	many := cluster("c3")
	many.Labels = map[string]string{}
	for i := range 65 {
		many.Labels[fmt.Sprintf("k%d", i)] = "v"
	}
	if _, err := r.Create(ctx, many); !errors.As(err, &ve) {
		t.Fatalf("too many labels err = %v", err)
	}
}
