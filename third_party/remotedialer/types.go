package remotedialer

import (
	"sync/atomic"
	"time"
)

// sextant patch: liveness timing was a pair of consts (5s ping / 60s wait),
// which made a silent partition take 60s to detect. Now configurable.
const (
	DefaultPingWriteInterval = 5 * time.Second
	DefaultPingWaitDuration  = 15 * time.Second
)

var (
	pingWriteIntervalNs atomic.Int64
	pingWaitDurationNs  atomic.Int64
)

func init() { SetLiveness(DefaultPingWriteInterval, DefaultPingWaitDuration) }

// SetLiveness sets how often a client pings and how long either side waits
// for traffic before declaring the peer dead. Safe to call at any time;
// affects sessions on their next ping/read-deadline refresh.
func SetLiveness(pingInterval, wait time.Duration) {
	pingWriteIntervalNs.Store(int64(pingInterval))
	pingWaitDurationNs.Store(int64(wait))
}

func pingWriteInterval() time.Duration { return time.Duration(pingWriteIntervalNs.Load()) }
func pingWaitDuration() time.Duration  { return time.Duration(pingWaitDurationNs.Load()) }

const (
	// SyncConnectionsInterval is the time after which the client will send the list of active connection IDs
	SyncConnectionsInterval = 60 * time.Second
	// SyncConnectionsTimeout sets the maximum duration for a SyncConnections operation
	SyncConnectionsTimeout = 60 * time.Second
	MaxRead                = 8192
	HandshakeTimeOut       = 10 * time.Second
	// SendErrorTimeout sets the maximum duration for sending an error message to close a single connection
	SendErrorTimeout = 5 * time.Second
)
