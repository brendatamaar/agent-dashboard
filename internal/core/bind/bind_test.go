package bind_test

import (
	"errors"
	"testing"

	"agent-dashboard/internal/core/bind"
)

func TestValidateBindAddress(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		allowPublic bool
		wantErr     bool
	}{
		// Default secure mode (allowPublic = false)
		{
			name:        "refuse wildcard 0.0.0.0",
			addr:        "0.0.0.0:8080",
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "refuse ipv6 wildcard ::",
			addr:        "[::]:8080",
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "refuse empty host :8080",
			addr:        ":8080",
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "refuse public ipv4",
			addr:        "198.51.100.1:8080",
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "refuse lan private ip when not tailscale",
			addr:        "192.168.1.10:8080",
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "allow loopback 127.0.0.1",
			addr:        "127.0.0.1:8080",
			allowPublic: false,
			wantErr:     false,
		},
		{
			name:        "allow localhost",
			addr:        "localhost:8080",
			allowPublic: false,
			wantErr:     false,
		},
		{
			name:        "allow ipv6 loopback ::1",
			addr:        "[::1]:8080",
			allowPublic: false,
			wantErr:     false,
		},
		{
			name:        "allow tailscale cgnat ipv4 100.64.0.1",
			addr:        "100.64.0.1:8080",
			allowPublic: false,
			wantErr:     false,
		},
		{
			name:        "allow tailscale cgnat ipv4 100.115.92.10",
			addr:        "100.115.92.10:8080",
			allowPublic: false,
			wantErr:     false,
		},
		{
			name:        "allow tailscale MagicDNS",
			addr:        "my-node.ts.net:8080",
			allowPublic: false,
			wantErr:     false,
		},
		// Insecure mode (allowPublic = true)
		{
			name:        "allow wildcard 0.0.0.0 when explicitly enabled",
			addr:        "0.0.0.0:8080",
			allowPublic: true,
			wantErr:     false,
		},
		{
			name:        "allow empty host when explicitly enabled",
			addr:        ":8080",
			allowPublic: true,
			wantErr:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := bind.ValidateBindAddress(tc.addr, tc.allowPublic)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateBindAddress(%q, %v) error = %v, wantErr %v", tc.addr, tc.allowPublic, err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, bind.ErrPublicBindNotAllowed) {
				t.Errorf("expected ErrPublicBindNotAllowed, got %v", err)
			}
		})
	}
}
