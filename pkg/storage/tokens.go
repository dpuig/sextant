package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dpuig/sextant/pkg/tenancy"
)

// ErrInvalidToken is returned for every redemption failure (unknown, expired,
// already used, malformed) so callers cannot probe which tokens exist.
var ErrInvalidToken = errors.New("storage: invalid registration token")

// MaxTokenTTL bounds how long a registration token may live.
const MaxTokenTTL = 24 * time.Hour

const tokenPrefix = "sxt1"

var agentNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Tokens issues and redeems single-use agent registration tokens. A token is
// "sxt1.<tenant>.<secret>"; the tenant is embedded so redemption can run under
// that tenant's row-level security without a cross-tenant lookup. Only the
// SHA-256 of the secret is stored.
type Tokens struct{ pool *pgxpool.Pool }

func NewTokens(pool *pgxpool.Pool) *Tokens { return &Tokens{pool: pool} }

func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// Create stores a new token for agent in the context's tenant and returns it.
// The plaintext is returned once and never stored.
func (t *Tokens) Create(ctx context.Context, agent string, ttl time.Duration) (string, error) {
	tid, err := tenancy.FromContext(ctx)
	if err != nil {
		return "", err
	}
	if !agentNameRE.MatchString(agent) {
		return "", fmt.Errorf("storage: invalid agent name %q", agent)
	}
	if ttl <= 0 || ttl > MaxTokenTTL {
		return "", fmt.Errorf("storage: token ttl %s outside (0, %s]", ttl, MaxTokenTTL)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	err = t.inTx(ctx, tid, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO registration_tokens (tenant_id, token_hash, agent_name, expires_at) VALUES ($1, $2, $3, now() + $4 * interval '1 second')`,
			tid.String(), hashSecret(secret), agent, ttl.Seconds())
		return err
	})
	if err != nil {
		return "", err
	}
	return tokenPrefix + "." + tid.String() + "." + secret, nil
}

// Redeem atomically consumes token and returns its tenant and agent name.
// It succeeds at most once per token.
func (t *Tokens) Redeem(ctx context.Context, token string) (tenancy.ID, string, error) {
	var (
		tid   tenancy.ID
		agent string
	)
	err := t.RedeemWith(ctx, token, func(t2 tenancy.ID, a string) error { tid, agent = t2, a; return nil })
	return tid, agent, err
}

// RedeemWith consumes token and runs fn in the same transaction. If fn returns
// an error the redemption is rolled back and the token stays usable, so a
// failure while issuing a credential does not burn the token. At most one
// concurrent caller can succeed.
func (t *Tokens) RedeemWith(ctx context.Context, token string, fn func(tenant tenancy.ID, agent string) error) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix || parts[2] == "" {
		return ErrInvalidToken
	}
	tid, err := tenancy.ParseID(parts[1])
	if err != nil {
		return ErrInvalidToken
	}
	err = t.inTx(ctx, tid, func(tx pgx.Tx) error {
		var agent string
		if err := tx.QueryRow(ctx,
			`UPDATE registration_tokens SET used_at = now()
			 WHERE tenant_id = $1 AND token_hash = $2 AND used_at IS NULL AND expires_at > now()
			 RETURNING agent_name`,
			tid.String(), hashSecret(parts[2])).Scan(&agent); err != nil {
			return err
		}
		return fn(tid, agent)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	return err
}

func (t *Tokens) inTx(ctx context.Context, tid tenancy.ID, fn func(pgx.Tx) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('sextant.tenant_id', $1, true)`, tid.String()); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
