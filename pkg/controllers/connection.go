// Package controllers holds the management plane's reconcilers.
package controllers

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dpuig/sextant/pkg/apis/v1alpha1"
	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
)

// ConnectionTracker writes agent connectivity into Cluster.status.
//
// Events are coalesced per cluster (only the latest state matters) and applied
// by one worker, so a flapping agent can neither pile up work nor have its
// updates reordered. While an agent stays connected its status.lastSeen is
// refreshed every Heartbeat: if the replica that held the tunnel dies, no
// disconnect event is ever emitted, so consumers must treat connected=true
// with a stale lastSeen (older than ~3 heartbeats) as "unknown".
type ConnectionTracker struct {
	reg *registry.Registry
	log *slog.Logger
	now func() time.Time

	// Heartbeat is how often lastSeen is refreshed for connected agents.
	Heartbeat time.Duration
	// RetryDelay is how long a failed update waits before it is tried again.
	RetryDelay time.Duration

	mu      sync.Mutex
	pending map[key]bool // latest unapplied state per cluster
	live    map[key]bool // clusters currently connected, for heartbeats
	wake    chan struct{}
}

type key struct {
	tenant tenancy.ID
	agent  string
}

// NewConnectionTracker returns a tracker; call Run to start it. log may be nil.
func NewConnectionTracker(reg *registry.Registry, log *slog.Logger) *ConnectionTracker {
	if log == nil {
		log = slog.Default()
	}
	return &ConnectionTracker{
		reg: reg, log: log, now: time.Now, Heartbeat: 30 * time.Second, RetryDelay: time.Second,
		pending: map[key]bool{}, live: map[key]bool{}, wake: make(chan struct{}, 1),
	}
}

// OnChange records a connectivity change. It never blocks, so it is safe to
// call from the tunnel's connection goroutine.
func (c *ConnectionTracker) OnChange(t tenancy.ID, agent string, connected bool) {
	k := key{t, agent}
	c.mu.Lock()
	c.pending[k] = connected
	if connected {
		c.live[k] = true
	} else {
		delete(c.live, k)
	}
	c.mu.Unlock()
	c.signal()
}

func (c *ConnectionTracker) signal() {
	select {
	case c.wake <- struct{}{}:
	default: // a wake-up is already queued
	}
}

// Run applies updates until ctx is cancelled.
func (c *ConnectionTracker) Run(ctx context.Context) {
	beat := time.NewTicker(c.Heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			c.mu.Lock()
			for k := range c.live {
				if _, queued := c.pending[k]; !queued {
					c.pending[k] = true
				}
			}
			c.mu.Unlock()
		case <-c.wake:
		}
		c.drain(ctx)
	}
}

func (c *ConnectionTracker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		c.mu.Lock()
		var (
			k         key
			connected bool
			found     bool
		)
		for k, connected = range c.pending {
			delete(c.pending, k)
			found = true
			break
		}
		c.mu.Unlock()
		if !found {
			return
		}
		if err := c.apply(ctx, k, connected); err != nil {
			c.log.Warn("cluster status update failed; will retry", "tenant", k.tenant, "cluster", k.agent, "err", err)
			// Retry later without holding up other clusters.
			time.AfterFunc(c.RetryDelay, func() {
				c.mu.Lock()
				if _, newer := c.pending[k]; !newer { // never overwrite a newer event
					c.pending[k] = connected
				}
				c.mu.Unlock()
				c.signal()
			})
		}
	}
}

// apply sets the status, retrying optimistic-concurrency conflicts. A cluster
// that does not exist is ignored (the tracker never creates objects).
func (c *ConnectionTracker) apply(ctx context.Context, k key, connected bool) error {
	ctx = tenancy.WithTenant(ctx, k.tenant)
	var err error
	for range 5 {
		var obj v1alpha1.Resource
		obj, err = c.reg.Get(ctx, "Cluster", k.agent)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		cl := obj.(*v1alpha1.Cluster)
		seen := metav1.NewTime(c.now())
		cl.Status.Connected, cl.Status.LastSeen = connected, &seen
		if _, err = c.reg.UpdateStatus(ctx, cl); !errors.Is(err, storage.ErrConflict) {
			if errors.Is(err, storage.ErrNotFound) {
				return nil
			}
			return err
		}
	}
	return err
}
