// Package storagetest provisions throwaway Postgres databases for tests.
package storagetest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dpuig/sextant/pkg/storage"
)

// AdminDSN returns the superuser DSN from SEXTANT_TEST_PG_DSN, skipping the
// test when it is unset (see `make test-integration`).
func AdminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SEXTANT_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("SEXTANT_TEST_PG_DSN not set")
	}
	return dsn
}

// NewDatabase creates an empty database and returns its owner DSN. The
// database is dropped when the test ends.
func NewDatabase(t *testing.T) string {
	t.Helper()
	admin := AdminDSN(t)
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
	return u.String()
}

// NewPool creates a migrated database and returns a pool connected as the
// non-owner sextant_app role, so row-level security applies to it.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	ownerDSN := NewDatabase(t)
	owner, err := pgx.Connect(ctx, ownerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(ctx) }()
	if err := storage.Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	enableAppLogin(t, ctx)
	u, _ := url.Parse(ownerDSN)
	u.User = url.UserPassword("sextant_app", "app")
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// enableAppLogin grants LOGIN to the cluster-wide app role. Test packages run
// in parallel, and concurrent ALTER ROLE fails with "tuple concurrently
// updated", so serialise on an advisory lock in the shared admin database
// (advisory locks are per-database).
func enableAppLogin(t *testing.T, ctx context.Context) {
	t.Helper()
	admin, err := pgx.Connect(ctx, AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(7357)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, `SELECT pg_advisory_unlock(7357)`) }()
	if _, err := admin.Exec(ctx, `ALTER ROLE sextant_app LOGIN PASSWORD 'app'`); err != nil {
		t.Fatal(err)
	}
}
