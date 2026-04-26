package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func ptrInt(v int) *int { return &v }

func TestValidateLimits(t *testing.T) {
	cases := []struct {
		name      string
		opts      CreateSandboxOpts
		backend   string
		wantErr   bool
		wantMatch string // substring expected in error message
	}{
		// Both nil — always OK regardless of backend.
		{name: "both nil firecracker", opts: CreateSandboxOpts{}, backend: "firecracker", wantErr: false},
		{name: "both nil incus", opts: CreateSandboxOpts{}, backend: "incus", wantErr: false},
		{name: "both nil unknown", opts: CreateSandboxOpts{}, backend: "mock", wantErr: false},

		// Firecracker bounds: CPU 1..32, mem 128..8192.
		{name: "fc cpu 0", opts: CreateSandboxOpts{CPULimit: ptrInt(0)}, backend: "firecracker", wantErr: true, wantMatch: "cpu_limit"},
		{name: "fc cpu 33", opts: CreateSandboxOpts{CPULimit: ptrInt(33)}, backend: "firecracker", wantErr: true, wantMatch: "cpu_limit"},
		{name: "fc cpu 32 ok", opts: CreateSandboxOpts{CPULimit: ptrInt(32)}, backend: "firecracker", wantErr: false},
		{name: "fc mem 127", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(127)}, backend: "firecracker", wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "fc mem 8193", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(8193)}, backend: "firecracker", wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "fc mem 8192 ok", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(8192)}, backend: "firecracker", wantErr: false},

		// Generic (Incus) bounds: CPU 1..256, mem 16..524288.
		{name: "incus cpu 64 ok", opts: CreateSandboxOpts{CPULimit: ptrInt(64)}, backend: "incus", wantErr: false},
		{name: "incus cpu 256 ok", opts: CreateSandboxOpts{CPULimit: ptrInt(256)}, backend: "incus", wantErr: false},
		{name: "incus cpu 257", opts: CreateSandboxOpts{CPULimit: ptrInt(257)}, backend: "incus", wantErr: true, wantMatch: "cpu_limit"},
		{name: "incus mem 16384 ok", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(16384)}, backend: "incus", wantErr: false},
		{name: "incus mem 524288 ok", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(524288)}, backend: "incus", wantErr: false},
		{name: "incus mem 524289", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(524289)}, backend: "incus", wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "incus mem 15", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(15)}, backend: "incus", wantErr: true, wantMatch: "memory_limit_mb"},

		// Mock backend: same as generic.
		{name: "mock cpu 64 ok", opts: CreateSandboxOpts{CPULimit: ptrInt(64)}, backend: "mock", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLimits(tc.opts, tc.backend)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, domain.ErrInvalidArgument) {
					t.Errorf("error %v should wrap ErrInvalidArgument", err)
				}
				if tc.wantMatch != "" && !strings.Contains(err.Error(), tc.wantMatch) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantMatch)
				}
			} else if err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		})
	}
}
