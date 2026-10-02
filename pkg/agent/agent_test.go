package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// selfSignedCreds makes a credential whose leaf is valid [notBefore, notAfter].
func selfSignedCreds(t *testing.T, notBefore, notAfter time.Time) *Credentials {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "t"}, NotBefore: notBefore, NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &Credentials{Key: key, Chain: [][]byte{der}}
}

func TestFileStore_RoundTripAndPermissions(t *testing.T) {
	s := FileStore{Dir: filepath.Join(t.TempDir(), "state")}
	if got, err := s.Load(); got != nil || err != nil {
		t.Fatalf("empty store Load = %v, %v", got, err)
	}
	now := time.Now()
	c := selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour))
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got == nil {
		t.Fatalf("Load = %v, %v", got, err)
	}
	if !got.Key.Equal(c.Key) || len(got.Chain) != 1 {
		t.Fatal("round trip changed credentials")
	}
	if fi, _ := os.Stat(s.path()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(s.Dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestFileStore_RejectsMismatchedKeyAndChain(t *testing.T) {
	s := FileStore{Dir: t.TempDir()}
	now := time.Now()
	a, b := selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour)), selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour))
	if err := s.Save(&Credentials{Key: b.Key, Chain: a.Chain}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("mismatched key/cert accepted")
	}
}

func TestFileStore_SaveIsAtomicOneFileNoStrays(t *testing.T) {
	s := FileStore{Dir: t.TempDir()}
	now := time.Now()
	_ = s.Save(selfSignedCreds(t, now.Add(-time.Hour), now.Add(time.Hour)))
	_ = s.Save(selfSignedCreds(t, now.Add(-time.Hour), now.Add(2*time.Hour))) // overwrite
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 1 || entries[0].Name() != "agent-credentials.pem" {
		t.Fatalf("state dir = %v, want exactly agent-credentials.pem", entries)
	}
}

func TestFileStore_TruncatedFileIsAnErrorNotACrash(t *testing.T) {
	s := FileStore{Dir: t.TempDir()}
	_ = os.WriteFile(s.path(), []byte("-----BEGIN EC PRIVATE KEY-----\ngarbage"), 0o600)
	if _, err := s.Load(); err == nil {
		t.Fatal("truncated file accepted")
	}
}

func TestManager_CorruptStateWithTokenReenrollsWithoutTokenFails(t *testing.T) {
	now := time.Now()
	store := FileStore{Dir: t.TempDir()}
	_ = os.WriteFile(store.path(), []byte("garbage"), 0o600)
	fi := &fakeIssuer{next: func() *Credentials { return selfSignedCreds(t, now.Add(-time.Minute), now.Add(time.Hour)) }}

	if err := newManager(fi, store, "", quiet).Init(context.Background()); err == nil {
		t.Fatal("corrupt state with no token should fail loudly")
	}
	if err := newManager(fi, store, "fresh", quiet).Init(context.Background()); err != nil || fi.enrolls != 1 {
		t.Fatalf("corrupt state with a token should re-enroll: err=%v enrolls=%d", err, fi.enrolls)
	}
}

// fakeIssuer records calls and hands out preset credentials.
type fakeIssuer struct {
	mu        sync.Mutex
	enrolls   int
	renews    int
	enrollErr error
	renewErr  error
	next      func() *Credentials
}

func (f *fakeIssuer) Enroll(context.Context, string) (*Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enrolls++
	if f.enrollErr != nil {
		return nil, f.enrollErr
	}
	return f.next(), nil
}

func (f *fakeIssuer) Renew(context.Context, *Credentials) (*Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renews++
	if f.renewErr != nil {
		return nil, f.renewErr
	}
	return f.next(), nil
}

func TestManager_EnrollsThenReusesStoredCredentialsWithoutToken(t *testing.T) {
	now := time.Now()
	store := FileStore{Dir: t.TempDir()}
	fi := &fakeIssuer{next: func() *Credentials { return selfSignedCreds(t, now.Add(-time.Minute), now.Add(24*time.Hour)) }}

	m1 := newManager(fi, store, "the-token", quiet)
	if err := m1.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fi.enrolls != 1 {
		t.Fatalf("enrolls = %d", fi.enrolls)
	}

	// "Restart": a new manager with NO token must come up from disk alone.
	m2 := newManager(fi, store, "", quiet)
	if err := m2.Init(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if fi.enrolls != 1 {
		t.Fatalf("restart re-enrolled (enrolls = %d)", fi.enrolls)
	}
	if c, err := m2.Certificate(); err != nil || c == nil {
		t.Fatalf("Certificate = %v, %v", c, err)
	}
}

func TestManager_NoCredentialsNoTokenFails(t *testing.T) {
	m := newManager(&fakeIssuer{}, FileStore{Dir: t.TempDir()}, "", quiet)
	if err := m.Init(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if _, err := m.Certificate(); err == nil {
		t.Fatal("Certificate() before enrollment should fail")
	}
}

func TestManager_ExpiredStoredCredentials(t *testing.T) {
	now := time.Now()
	store := FileStore{Dir: t.TempDir()}
	_ = store.Save(selfSignedCreds(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour)))

	m := newManager(&fakeIssuer{}, store, "", quiet)
	if err := m.Init(context.Background()); !errors.Is(err, ErrCredentialsExpired) {
		t.Fatalf("err = %v, want ErrCredentialsExpired", err)
	}

	// With a fresh token it re-enrolls instead.
	fi := &fakeIssuer{next: func() *Credentials { return selfSignedCreds(t, now.Add(-time.Minute), now.Add(24*time.Hour)) }}
	m = newManager(fi, store, "fresh", quiet)
	if err := m.Init(context.Background()); err != nil || fi.enrolls != 1 {
		t.Fatalf("re-enroll: err=%v enrolls=%d", err, fi.enrolls)
	}
}

func TestManager_FailedSaveFailsInit(t *testing.T) {
	now := time.Now()
	// A file where the state dir should be makes Save fail.
	bad := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(bad, []byte("x"), 0o600)
	fi := &fakeIssuer{next: func() *Credentials { return selfSignedCreds(t, now.Add(-time.Minute), now.Add(time.Hour)) }}
	m := newManager(fi, FileStore{Dir: filepath.Join(bad, "sub")}, "tok", quiet)
	if err := m.Init(context.Background()); err == nil {
		t.Fatal("Init succeeded although credentials could not be persisted")
	}
}

func TestManager_RenewsOnlyAfterHalfLife(t *testing.T) {
	base := time.Now()
	store := FileStore{Dir: t.TempDir()}
	first := selfSignedCreds(t, base, base.Add(24*time.Hour))
	second := selfSignedCreds(t, base.Add(13*time.Hour), base.Add(37*time.Hour))
	calls := 0
	fi := &fakeIssuer{next: func() *Credentials {
		calls++
		if calls == 1 {
			return first
		}
		return second
	}}
	m := newManager(fi, store, "tok", quiet)
	m.now = func() time.Time { return base.Add(time.Minute) }
	if err := m.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	m.now = func() time.Time { return base.Add(11 * time.Hour) }
	if renewed, err := m.renewIfDue(context.Background()); renewed || err != nil {
		t.Fatalf("renewed before half-life: %v %v", renewed, err)
	}
	m.now = func() time.Time { return base.Add(13 * time.Hour) }
	renewed, err := m.renewIfDue(context.Background())
	if !renewed || err != nil {
		t.Fatalf("did not renew after half-life: %v %v", renewed, err)
	}
	cert, _ := m.Certificate()
	if cert.PrivateKey.(*ecdsa.PrivateKey) != second.Key {
		t.Fatal("renewed credentials not in use")
	}
	if persisted, _ := store.Load(); persisted == nil || !persisted.Key.Equal(second.Key) {
		t.Fatal("renewed credentials not persisted")
	}
}

func TestManager_RenewFailureKeepsOldCredentials(t *testing.T) {
	base := time.Now()
	store := FileStore{Dir: t.TempDir()}
	first := selfSignedCreds(t, base, base.Add(24*time.Hour))
	fi := &fakeIssuer{next: func() *Credentials { return first }}
	m := newManager(fi, store, "tok", quiet)
	m.now = func() time.Time { return base.Add(time.Minute) }
	_ = m.Init(context.Background())

	m.now = func() time.Time { return base.Add(20 * time.Hour) }
	fi.renewErr = errors.New("management plane down")
	if _, err := m.renewIfDue(context.Background()); err == nil {
		t.Fatal("expected renew error")
	}
	cert, err := m.Certificate()
	if err != nil || cert.PrivateKey.(*ecdsa.PrivateKey) != first.Key {
		t.Fatal("old credentials were dropped after a failed renewal")
	}
}
