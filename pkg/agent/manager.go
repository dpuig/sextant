package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// ErrCredentialsExpired means stored credentials are past their lifetime and
// no registration token was supplied to replace them.
var ErrCredentialsExpired = errors.New("agent: stored credentials expired; a new registration token is required")

// issuer is what Manager needs from Client (an interface so tests can fake it).
type issuer interface {
	Enroll(ctx context.Context, token string) (*Credentials, error)
	Renew(ctx context.Context, cur *Credentials) (*Credentials, error)
}

// Manager owns the agent's credentials: it loads or enrolls at startup and
// renews at 50% of the certificate's lifetime, swapping in the new certificate
// without a restart (the tunnel asks Certificate() on every reconnect).
type Manager struct {
	issuer issuer
	store  Store
	token  string // registration token; may be empty once enrolled
	log    *slog.Logger
	now    func() time.Time

	cur atomic.Pointer[Credentials]
}

// NewManager returns a Manager. token may be empty if credentials are already stored.
func NewManager(c *Client, store Store, token string, log *slog.Logger) *Manager {
	return newManager(c, store, token, log)
}

func newManager(i issuer, store Store, token string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{issuer: i, store: store, token: token, log: log, now: time.Now}
}

// Init loads stored credentials, or enrolls with the registration token.
func (m *Manager) Init(ctx context.Context) error {
	c, err := m.store.Load()
	if err != nil {
		if m.token == "" {
			return fmt.Errorf("load credentials: %w", err)
		}
		// Corrupt state plus a fresh token: start over rather than refuse to run.
		m.log.Warn("stored credentials unusable; re-enrolling", "err", err)
		c = nil
	}
	if c != nil {
		leaf, err := c.Leaf()
		if err != nil {
			return err
		}
		if m.now().Before(leaf.NotAfter) {
			m.cur.Store(c)
			m.log.Info("loaded stored agent credentials", "expires", leaf.NotAfter)
			return nil
		}
		if m.token == "" {
			return ErrCredentialsExpired
		}
	}
	if m.token == "" {
		return errors.New("agent: no stored credentials and no registration token")
	}
	c, err = m.issuer.Enroll(ctx, m.token)
	if err != nil {
		return err
	}
	if err := m.store.Save(c); err != nil {
		// Without persistence a restart would need a fresh (single-use) token.
		return fmt.Errorf("save credentials: %w", err)
	}
	m.cur.Store(c)
	m.log.Info("enrolled")
	return nil
}

// Certificate is the tunnel's certificate provider.
func (m *Manager) Certificate() (*tls.Certificate, error) {
	c := m.cur.Load()
	if c == nil {
		return nil, errors.New("agent: not enrolled")
	}
	return c.TLS(), nil
}

// renewIfDue renews when more than half the certificate's lifetime has passed.
func (m *Manager) renewIfDue(ctx context.Context) (bool, error) {
	cur := m.cur.Load()
	if cur == nil {
		return false, errors.New("agent: not enrolled")
	}
	leaf, err := cur.Leaf()
	if err != nil {
		return false, err
	}
	half := leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) / 2)
	if m.now().Before(half) {
		return false, nil
	}
	next, err := m.issuer.Renew(ctx, cur)
	if err != nil {
		return false, err
	}
	if err := m.store.Save(next); err != nil {
		return false, fmt.Errorf("save renewed credentials: %w", err)
	}
	m.cur.Store(next)
	m.log.Info("agent certificate renewed")
	return true, nil
}

// Run renews credentials until ctx is cancelled. Failures are retried with
// backoff; the old certificate stays in use meanwhile.
func (m *Manager) Run(ctx context.Context) {
	const (
		check      = time.Minute
		retryStart = 5 * time.Second
		retryMax   = 5 * time.Minute
	)
	wait, retry := check, retryStart
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if _, err := m.renewIfDue(ctx); err != nil {
			m.log.Warn("certificate renewal failed; will retry", "err", err, "in", retry)
			wait, retry = retry, min(retry*2, retryMax)
			continue
		}
		wait, retry = check, retryStart
	}
}
