package storage_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
)

// Integration tests need a Postgres superuser DSN in SEXTANT_TEST_PG_DSN
// (see `make test-integration`); they skip otherwise.
type env struct {
	store *storage.Store
	pool  *pgxpool.Pool
}

func setup(t *testing.T) *env {
	t.Helper()
	admin := os.Getenv("SEXTANT_TEST_PG_DSN")
	if admin == "" {
		t.Skip("SEXTANT_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	db := fmt.Sprintf("sextant_test_%d", rand.Uint32())
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE "+db+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})

	u, _ := url.Parse(admin)
	u.Path = "/" + db
	owner, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(ctx) }()
	if err := storage.Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(ctx, owner); err != nil { // idempotent
		t.Fatalf("second migrate: %v", err)
	}
	if _, err := conn.Exec(ctx, `ALTER ROLE sextant_app LOGIN PASSWORD 'app'`); err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("sextant_app", "app")
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &env{store: storage.New(pool), pool: pool}
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	id, err := tenancy.ParseID(tenant)
	if err != nil {
		t.Fatal(err)
	}
	return tenancy.WithTenant(context.Background(), id)
}

func TestCRUD(t *testing.T) {
	e := setup(t)
	ctx := ctxFor(t, "acme")
	o, err := e.store.Create(ctx, storage.Object{Kind: "Cluster", Name: "c1", Spec: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Create(ctx, storage.Object{Kind: "Cluster", Name: "c1"}); !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("duplicate create err = %v", err)
	}
	got, err := e.store.Get(ctx, "Cluster", "c1")
	if err != nil || got.ResourceVersion != o.ResourceVersion {
		t.Fatalf("get = %+v, %v", got, err)
	}
	upd, err := e.store.Update(ctx, storage.Object{Kind: "Cluster", Name: "c1", ResourceVersion: o.ResourceVersion, Spec: []byte(`{"a":2}`)})
	if err != nil || upd.ResourceVersion <= o.ResourceVersion {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	if _, err := e.store.Update(ctx, storage.Object{Kind: "Cluster", Name: "c1", ResourceVersion: o.ResourceVersion}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale update err = %v, want ErrConflict", err)
	}
	if _, err := e.store.Update(ctx, storage.Object{Kind: "Cluster", Name: "nope", ResourceVersion: 1}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("update missing err = %v, want ErrNotFound", err)
	}
	list, _ := e.store.List(ctx, "Cluster")
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	if err := e.store.Delete(ctx, "Cluster", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Delete(ctx, "Cluster", "c1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestNoTenantFailsClosed(t *testing.T) {
	e := setup(t)
	bg := context.Background()
	if _, err := e.store.Get(bg, "Cluster", "c1"); !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("Get err = %v", err)
	}
	if _, err := e.store.List(bg, "Cluster"); !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("List err = %v", err)
	}
	if _, err := e.store.Create(bg, storage.Object{Kind: "Cluster", Name: "x"}); !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("Create err = %v", err)
	}
}

func TestIsolation_AllVerbs(t *testing.T) {
	e := setup(t)
	a, b := ctxFor(t, "acme"), ctxFor(t, "globex")
	oa, err := e.store.Create(a, storage.Object{Kind: "Cluster", Name: "shared-name", Spec: []byte(`{"owner":"acme"}`)})
	if err != nil {
		t.Fatal(err)
	}
	// Same name in another tenant is a different object, not a conflict.
	if _, err := e.store.Create(b, storage.Object{Kind: "Cluster", Name: "shared-name", Spec: []byte(`{"owner":"globex"}`)}); err != nil {
		t.Fatalf("same name in other tenant: %v", err)
	}
	if _, err := e.store.Create(a, storage.Object{Kind: "Cluster", Name: "only-acme"}); err != nil {
		t.Fatal(err)
	}

	got, _ := e.store.Get(b, "Cluster", "shared-name")
	if string(got.Spec) != `{"owner": "globex"}` {
		t.Fatalf("globex saw %s", got.Spec)
	}
	if _, err := e.store.Get(b, "Cluster", "only-acme"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("cross-tenant get err = %v", err)
	}
	list, _ := e.store.List(b, "Cluster")
	if len(list) != 1 {
		t.Fatalf("globex list len = %d, want 1", len(list))
	}
	if _, err := e.store.Update(b, storage.Object{Kind: "Cluster", Name: "only-acme", ResourceVersion: oa.ResourceVersion}); err == nil {
		t.Fatal("cross-tenant update succeeded")
	}
	if err := e.store.Delete(b, "Cluster", "only-acme"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("cross-tenant delete err = %v", err)
	}
	if _, err := e.store.Get(a, "Cluster", "only-acme"); err != nil {
		t.Fatalf("acme object damaged: %v", err)
	}
}

// The application layer is not the only barrier: even a query with NO tenant
// filter, run as the app role, only sees the bound tenant's rows.
func TestRLS_UnfilteredQueryStillScoped(t *testing.T) {
	e := setup(t)
	a, b := ctxFor(t, "acme"), ctxFor(t, "globex")
	_, _ = e.store.Create(a, storage.Object{Kind: "Cluster", Name: "a1"})
	_, _ = e.store.Create(b, storage.Object{Kind: "Cluster", Name: "b1"})

	ctx := context.Background()
	count := func(tenantSetting string) int {
		tx, err := e.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if tenantSetting != "" {
			if _, err := tx.Exec(ctx, `SELECT set_config('sextant.tenant_id', $1, true)`, tenantSetting); err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM objects`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("acme"); n != 1 {
		t.Fatalf("acme-bound unfiltered count = %d, want 1", n)
	}
	if n := count(""); n != 0 {
		t.Fatalf("unbound count = %d, want 0 (fail closed)", n)
	}
}

func TestRLS_CannotWriteAnotherTenantsRow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tx, _ := e.pool.Begin(ctx)
	defer func() { _ = tx.Rollback(ctx) }()
	_, _ = tx.Exec(ctx, `SELECT set_config('sextant.tenant_id', 'acme', true)`)
	if _, err := tx.Exec(ctx, `INSERT INTO objects (tenant_id, kind, name) VALUES ('globex','Cluster','forged')`); err == nil {
		t.Fatal("RLS WITH CHECK allowed inserting a row for another tenant")
	}
}

func TestMigrate_ConcurrentRunnersAreSafe(t *testing.T) {
	admin := os.Getenv("SEXTANT_TEST_PG_DSN")
	if admin == "" {
		t.Skip("SEXTANT_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	root, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	db := fmt.Sprintf("sextant_test_%d", rand.Uint32())
	if _, err := root.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = root.Exec(ctx, "DROP DATABASE "+db+" WITH (FORCE)")
		_ = root.Close(ctx)
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + db

	errs := make(chan error, 6)
	for range 6 {
		go func() {
			c, err := pgx.Connect(ctx, u.String())
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = c.Close(ctx) }()
			errs <- storage.Migrate(ctx, c)
		}()
	}
	for range 6 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
}

func TestUpdate_ConcurrentWritersExactlyOneWins(t *testing.T) {
	e := setup(t)
	ctx := ctxFor(t, "acme")
	o, _ := e.store.Create(ctx, storage.Object{Kind: "Cluster", Name: "c1"})
	res := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := e.store.Update(ctx, storage.Object{Kind: "Cluster", Name: "c1", ResourceVersion: o.ResourceVersion})
			res <- err
		}()
	}
	wins, conflicts := 0, 0
	for range 8 {
		switch err := <-res; {
		case err == nil:
			wins++
		case errors.Is(err, storage.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 || conflicts != 7 {
		t.Fatalf("wins=%d conflicts=%d, want 1 and 7", wins, conflicts)
	}
}
