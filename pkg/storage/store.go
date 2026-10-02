package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dpuig/sextant/pkg/tenancy"
)

var (
	ErrNotFound      = errors.New("storage: not found")
	ErrAlreadyExists = errors.New("storage: already exists")
	// ErrConflict means the caller's ResourceVersion is stale.
	ErrConflict = errors.New("storage: resource version conflict")
)

// Object is a stored API object. Spec and Status are opaque JSON here; typed
// encoding lives with the API types.
type Object struct {
	Kind            string
	Name            string
	ResourceVersion int64
	Labels          map[string]string
	Spec            json.RawMessage
	Status          json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Store reads and writes objects for the tenant found in each call's context.
// Its pool must connect as the non-owner sextant_app role so RLS applies.
type Store struct{ pool *pgxpool.Pool }

// New returns a Store over pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTenantTx runs fn in a transaction with the RLS tenant setting bound to
// the context's tenant (transaction-local, so pooled connections never leak it).
func (s *Store) inTenantTx(ctx context.Context, fn func(tx pgx.Tx, tenant string) error) error {
	tid, err := tenancy.FromContext(ctx)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('sextant.tenant_id', $1, true)`, tid.String()); err != nil {
		return err
	}
	if err := fn(tx, tid.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const cols = `kind, name, resource_version, labels, spec, status, created_at, updated_at`

func scan(row pgx.Row) (*Object, error) {
	var o Object
	if err := row.Scan(&o.Kind, &o.Name, &o.ResourceVersion, &o.Labels, &o.Spec, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

func labelsOrEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func orEmpty(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage(`{}`)
	}
	return m
}

// Create inserts o. ResourceVersion in o is ignored.
func (s *Store) Create(ctx context.Context, o Object) (*Object, error) {
	var out *Object
	err := s.inTenantTx(ctx, func(tx pgx.Tx, tenant string) error {
		var err error
		out, err = scan(tx.QueryRow(ctx,
			`INSERT INTO objects (tenant_id, kind, name, labels, spec, status) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+cols,
			tenant, o.Kind, o.Name, labelsOrEmpty(o.Labels), orEmpty(o.Spec), orEmpty(o.Status)))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAlreadyExists
		}
		return err
	})
	return out, err
}

// Get returns one object, or ErrNotFound.
func (s *Store) Get(ctx context.Context, kind, name string) (*Object, error) {
	var out *Object
	err := s.inTenantTx(ctx, func(tx pgx.Tx, tenant string) error {
		var err error
		out, err = scan(tx.QueryRow(ctx,
			`SELECT `+cols+` FROM objects WHERE tenant_id = $1 AND kind = $2 AND name = $3`, tenant, kind, name))
		return err
	})
	return out, err
}

// List returns all objects of kind, ordered by name.
func (s *Store) List(ctx context.Context, kind string) ([]*Object, error) {
	var out []*Object
	err := s.inTenantTx(ctx, func(tx pgx.Tx, tenant string) error {
		rows, err := tx.Query(ctx,
			`SELECT `+cols+` FROM objects WHERE tenant_id = $1 AND kind = $2 ORDER BY name`, tenant, kind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

// Update replaces spec and status if o.ResourceVersion matches the stored one;
// otherwise it returns ErrConflict (or ErrNotFound if the object is gone).
// The row is locked first so the version check and write are one atomic step.
func (s *Store) Update(ctx context.Context, o Object) (*Object, error) {
	var out *Object
	err := s.inTenantTx(ctx, func(tx pgx.Tx, tenant string) error {
		var current int64
		err := tx.QueryRow(ctx,
			`SELECT resource_version FROM objects WHERE tenant_id = $1 AND kind = $2 AND name = $3 FOR UPDATE`,
			tenant, o.Kind, o.Name).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current != o.ResourceVersion {
			return ErrConflict
		}
		out, err = scan(tx.QueryRow(ctx,
			`UPDATE objects SET labels = $4, spec = $5, status = $6, resource_version = nextval('resource_version_seq'), updated_at = now()
			 WHERE tenant_id = $1 AND kind = $2 AND name = $3 RETURNING `+cols,
			tenant, o.Kind, o.Name, labelsOrEmpty(o.Labels), orEmpty(o.Spec), orEmpty(o.Status)))
		return err
	})
	return out, err
}

// Delete removes an object, or returns ErrNotFound.
func (s *Store) Delete(ctx context.Context, kind, name string) error {
	return s.inTenantTx(ctx, func(tx pgx.Tx, tenant string) error {
		tag, err := tx.Exec(ctx, `DELETE FROM objects WHERE tenant_id = $1 AND kind = $2 AND name = $3`, tenant, kind, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%s/%s: %w", kind, name, ErrNotFound)
		}
		return nil
	})
}
