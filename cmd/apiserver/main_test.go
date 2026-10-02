package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCheckListen(t *testing.T) {
	tests := []struct {
		name                string
		addr                string
		tls, dev, devRemote bool
		wantErr             bool
	}{
		{"loopback plain", "127.0.0.1:8443", false, false, false, false},
		{"ipv6 loopback plain", "[::1]:8443", false, false, false, false},
		{"localhost plain", "localhost:8443", false, false, false, false},
		{"all interfaces plain", "0.0.0.0:8443", false, false, false, true},
		{"empty host plain (all interfaces)", ":8443", false, false, false, true},
		{"public plain", "10.0.0.5:8443", false, false, false, true},
		{"public with tls", "0.0.0.0:8443", true, false, false, false},
		{"dev on loopback", "127.0.0.1:8443", false, true, false, false},
		{"dev on loopback with tls", "127.0.0.1:8443", true, true, false, false},
		{"dev on public with tls but no hazard flag", "0.0.0.0:8443", true, true, false, true},
		{"dev on public with hazard flag but no tls", "0.0.0.0:8443", false, true, true, true},
		{"dev on public with tls and hazard flag", "0.0.0.0:8443", true, true, true, false},
		{"hazard flag alone grants nothing without dev", "0.0.0.0:8443", false, false, true, true},
		{"malformed", "nonsense", false, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkListen(tt.addr, tt.tls, tt.dev, tt.devRemote); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Rolling restarts: Kubernetes removes the pod from the Service endpoints
// asynchronously after SIGTERM, so the pod must keep serving for a moment
// (delay), THEN hand agents over (drain), THEN stop (shutdown), and drop any
// agent that reattached while it was stopping.
func TestShutdownSequence_DelayThenDrainThenShutdownThenDrainAgain(t *testing.T) {
	var events []string
	var firstDrain time.Time
	start := time.Now()
	err := shutdownSequence(150*time.Millisecond, nil,
		func() {
			if firstDrain.IsZero() {
				firstDrain = time.Now()
			}
			events = append(events, "drain")
		},
		func() error { events = append(events, "shutdown"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "drain,shutdown,drain" {
		t.Fatalf("events = %v, want drain,shutdown,drain", events)
	}
	if firstDrain.Sub(start) < 150*time.Millisecond {
		t.Fatalf("drained after %v, before the delay elapsed", firstDrain.Sub(start))
	}
}

func TestShutdownSequence_InterruptCutsTheDelayShort(t *testing.T) {
	interrupt := make(chan struct{})
	close(interrupt)
	start := time.Now()
	var drained bool
	_ = shutdownSequence(10*time.Second, interrupt, func() { drained = true }, func() error { return nil })
	if !drained || time.Since(start) > time.Second {
		t.Fatalf("a second signal should skip the delay (drained=%v after %v)", drained, time.Since(start))
	}
}

func TestShutdownSequence_ZeroDelayDoesNotWait(t *testing.T) {
	start := time.Now()
	_ = shutdownSequence(0, nil, func() {}, func() error { return nil })
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("zero delay should not sleep")
	}
}

func TestShutdownSequence_ReturnsShutdownErrorAndStillDrains(t *testing.T) {
	want := errors.New("boom")
	drains := 0
	if got := shutdownSequence(0, nil, func() { drains++ }, func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("got %v", got)
	}
	if drains != 2 {
		t.Fatalf("drains = %d, want 2 (a failed Shutdown must not skip the final drain)", drains)
	}
}
