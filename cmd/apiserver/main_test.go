package main

import "testing"

func TestCheckListen(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		tls, dev bool
		wantErr  bool
	}{
		{"loopback plain", "127.0.0.1:8443", false, false, false},
		{"ipv6 loopback plain", "[::1]:8443", false, false, false},
		{"localhost plain", "localhost:8443", false, false, false},
		{"all interfaces plain", "0.0.0.0:8443", false, false, true},
		{"empty host plain (all interfaces)", ":8443", false, false, true},
		{"public plain", "10.0.0.5:8443", false, false, true},
		{"public with tls", "0.0.0.0:8443", true, false, false},
		{"dev on loopback", "127.0.0.1:8443", false, true, false},
		{"dev on loopback with tls", "127.0.0.1:8443", true, true, false},
		{"dev on public even with tls", "0.0.0.0:8443", true, true, true},
		{"malformed", "nonsense", false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkListen(tt.addr, tt.tls, tt.dev); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
