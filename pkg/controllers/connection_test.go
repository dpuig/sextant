package controllers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dpuig/sextant/pkg/apis/v1alpha1"
	"github.com/dpuig/sextant/pkg/controllers"
	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/storage/storagetest"
	"github.com/dpuig/sextant/pkg/tenancy"
)

func tid(t *testing.T, s string) tenancy.ID {
	t.Helper()
	id, err := tenancy.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func setup(t *testing.T) (*registry.Registry, *controllers.ConnectionTracker, context.Context) {
	t.Helper()
	reg := registry.New(storage.New(storagetest.NewPool(t)))
	tr := controllers.NewConnectionTracker(reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return reg, tr, ctx
}

func mkCluster(t *testing.T, reg *registry.Registry, tenant, name string) {
	t.Helper()
	_, err := reg.Create(tenancy.WithTenant(context.Background(), tid(t, tenant)), &v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: v1alpha1.ClusterSpec{Environment: "prod"}})
	if err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, reg *registry.Registry, tenant, name string) v1alpha1.ClusterStatus {
	t.Helper()
	o, err := reg.Get(tenancy.WithTenant(context.Background(), tid(t, tenant)), "Cluster", name)
	if err != nil {
		t.Fatal(err)
	}
	return o.(*v1alpha1.Cluster).Status
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTracker_RecordsConnectAndDisconnect(t *testing.T) {
	reg, tr, _ := setup(t)
	mkCluster(t, reg, "acme", "c1")

	tr.OnChange(tid(t, "acme"), "c1", true)
	eventually(t, "connected=true", func() bool { s := status(t, reg, "acme", "c1"); return s.Connected && s.LastSeen != nil })

	tr.OnChange(tid(t, "acme"), "c1", false)
	eventually(t, "connected=false", func() bool { return !status(t, reg, "acme", "c1").Connected })
}

func TestTracker_RapidFlapsConvergeOnTheLatestState(t *testing.T) {
	reg, tr, _ := setup(t)
	mkCluster(t, reg, "acme", "c1")
	for i := 0; i < 50; i++ {
		tr.OnChange(tid(t, "acme"), "c1", i%2 == 0)
	}
	tr.OnChange(tid(t, "acme"), "c1", true) // final state
	eventually(t, "final state", func() bool { return status(t, reg, "acme", "c1").Connected })
}

func TestTracker_UnknownClusterIsIgnoredAndDoesNotStallOthers(t *testing.T) {
	reg, tr, _ := setup(t)
	mkCluster(t, reg, "acme", "real")
	tr.OnChange(tid(t, "acme"), "ghost", true) // no such Cluster object
	tr.OnChange(tid(t, "acme"), "real", true)
	eventually(t, "real cluster updated", func() bool { return status(t, reg, "acme", "real").Connected })
	if _, err := reg.Get(tenancy.WithTenant(context.Background(), tid(t, "acme")), "Cluster", "ghost"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("tracker created a Cluster: %v", err)
	}
}

func TestTracker_TenantsDoNotInterfere(t *testing.T) {
	reg, tr, _ := setup(t)
	mkCluster(t, reg, "acme", "c1")
	mkCluster(t, reg, "globex", "c1")
	tr.OnChange(tid(t, "acme"), "c1", true)
	eventually(t, "acme connected", func() bool { return status(t, reg, "acme", "c1").Connected })
	if status(t, reg, "globex", "c1").Connected {
		t.Fatal("globex's same-named cluster was marked connected")
	}
}

func TestTracker_HeartbeatRefreshesLastSeenForConnectedAgents(t *testing.T) {
	reg := registry.New(storage.New(storagetest.NewPool(t)))
	tr := controllers.NewConnectionTracker(reg, nil)
	tr.Heartbeat = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	mkCluster(t, reg, "acme", "c1")
	tr.OnChange(tid(t, "acme"), "c1", true)
	var first time.Time
	eventually(t, "first lastSeen", func() bool {
		s := status(t, reg, "acme", "c1")
		if s.LastSeen == nil {
			return false
		}
		first = s.LastSeen.Time
		return true
	})
	eventually(t, "lastSeen advances without any new event", func() bool {
		s := status(t, reg, "acme", "c1")
		return s.LastSeen != nil && s.LastSeen.After(first)
	})
}

// failingStore fails status updates for one cluster name, forever.
type failingStore struct {
	*storage.Store
	bad string
}

func (f failingStore) Update(ctx context.Context, o storage.Object) (*storage.Object, error) {
	if o.Name == f.bad {
		return nil, errors.New("simulated database error")
	}
	return f.Store.Update(ctx, o)
}

func TestTracker_OneFailingClusterDoesNotStarveOthers(t *testing.T) {
	st := storage.New(storagetest.NewPool(t))
	reg := registry.New(failingStore{Store: st, bad: "bad"})
	tr := controllers.NewConnectionTracker(reg, nil)
	tr.RetryDelay = 5 * time.Second // far longer than the assertion window
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	mkCluster(t, reg, "acme", "bad")
	mkCluster(t, reg, "acme", "good")
	tr.OnChange(tid(t, "acme"), "bad", true)
	tr.OnChange(tid(t, "acme"), "good", true)

	deadline := time.Now().Add(1500 * time.Millisecond)
	for !status(t, reg, "acme", "good").Connected {
		if time.Now().After(deadline) {
			t.Fatal("a failing cluster held up the update of another")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
