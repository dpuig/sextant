package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/storage/storagetest"
	"github.com/dpuig/sextant/pkg/tenancy"
)

func newTokens(t *testing.T) *storage.Tokens {
	t.Helper()
	return storage.NewTokens(storagetest.NewPool(t))
}

func TestToken_RedeemOnce(t *testing.T) {
	tk := newTokens(t)
	tok, err := tk.Create(ctxFor(t, "acme"), "cluster-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tenant, agent, err := tk.Redeem(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if tenant.String() != "acme" || agent != "cluster-1" {
		t.Fatalf("got %q %q", tenant, agent)
	}
	if _, _, err := tk.Redeem(context.Background(), tok); !errors.Is(err, storage.ErrInvalidToken) {
		t.Fatalf("second redeem err = %v, want ErrInvalidToken", err)
	}
}

func TestToken_ConcurrentRedeemExactlyOneWins(t *testing.T) {
	tk := newTokens(t)
	tok, _ := tk.Create(ctxFor(t, "acme"), "c1", time.Hour)
	res := make(chan error, 10)
	for range 10 {
		go func() { _, _, err := tk.Redeem(context.Background(), tok); res <- err }()
	}
	wins := 0
	for range 10 {
		if err := <-res; err == nil {
			wins++
		} else if !errors.Is(err, storage.ErrInvalidToken) {
			t.Fatalf("unexpected err: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d redemptions succeeded, want exactly 1", wins)
	}
}

func TestToken_ExpiredAndUnknownAndMalformedAreIndistinguishable(t *testing.T) {
	tk := newTokens(t)
	expired, _ := tk.Create(ctxFor(t, "acme"), "c1", time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	good, _ := tk.Create(ctxFor(t, "acme"), "c2", time.Hour)
	forged := good[:len(good)-4] + "AAAA"
	for name, tok := range map[string]string{
		"expired": expired, "forged secret": forged, "garbage": "nope", "empty": "",
		"wrong tenant prefix": strings.Replace(good, ".acme.", ".globex.", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := tk.Redeem(context.Background(), tok); !errors.Is(err, storage.ErrInvalidToken) {
				t.Fatalf("err = %v, want ErrInvalidToken", err)
			}
		})
	}
	// The good token is untouched by the failed attempts above.
	if _, _, err := tk.Redeem(context.Background(), good); err != nil {
		t.Fatalf("good token consumed by bad attempts: %v", err)
	}
}

func TestToken_OnlyHashIsStored(t *testing.T) {
	pool := storagetest.NewPool(t)
	tok, _ := storage.NewTokens(pool).Create(ctxFor(t, "acme"), "c1", time.Hour)
	secret := tok[strings.LastIndex(tok, ".")+1:]
	var n int
	// Query as the app role under RLS for the right tenant: the secret must not appear anywhere.
	tx, _ := pool.Begin(context.Background())
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, _ = tx.Exec(context.Background(), `SELECT set_config('sextant.tenant_id','acme',true)`)
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM registration_tokens WHERE token_hash = $1 OR agent_name = $1`, secret).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("raw token secret found in table")
	}
}

func TestToken_CreateRequiresTenantAndValidates(t *testing.T) {
	tk := newTokens(t)
	if _, err := tk.Create(context.Background(), "c1", time.Hour); err == nil {
		t.Fatal("create without tenant succeeded")
	}
	if _, err := tk.Create(ctxFor(t, "acme"), "Bad Name", time.Hour); err == nil {
		t.Fatal("invalid agent name accepted")
	}
	if _, err := tk.Create(ctxFor(t, "acme"), "c1", 0); err == nil {
		t.Fatal("zero ttl accepted")
	}
	if _, err := tk.Create(ctxFor(t, "acme"), "c1", 25*time.Hour); err == nil {
		t.Fatal("ttl over 24h accepted")
	}
}

func TestToken_TenantsCannotRedeemEachOthersTokens(t *testing.T) {
	tk := newTokens(t)
	tok, _ := tk.Create(ctxFor(t, "acme"), "c1", time.Hour)
	// Re-label the token as globex's: tenant comes from the token, so this
	// looks up (globex, hash) which does not exist.
	forged := strings.Replace(tok, ".acme.", ".globex.", 1)
	if _, _, err := tk.Redeem(context.Background(), forged); !errors.Is(err, storage.ErrInvalidToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestToken_RedeemWithRollsBackWhenCallbackFails(t *testing.T) {
	tk := newTokens(t)
	tok, _ := tk.Create(ctxFor(t, "acme"), "c1", time.Hour)
	boom := errors.New("issuance failed")
	err := tk.RedeemWith(context.Background(), tok, func(tenancy.ID, string) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want callback error", err)
	}
	// Not spent: a retry succeeds, exactly once.
	if _, _, err := tk.Redeem(context.Background(), tok); err != nil {
		t.Fatalf("token burned by failed callback: %v", err)
	}
	if _, _, err := tk.Redeem(context.Background(), tok); !errors.Is(err, storage.ErrInvalidToken) {
		t.Fatalf("second redeem err = %v", err)
	}
}
