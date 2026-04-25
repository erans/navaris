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
		name         string
		opts         CreateSandboxOpts
		fromSnapshot bool
		wantErr      bool
		wantMatch    string // substring expected in error message; "" = any
	}{
		// Both nil — always OK.
		{name: "both nil, normal", opts: CreateSandboxOpts{}, wantErr: false},
		{name: "both nil, from-snapshot", opts: CreateSandboxOpts{}, fromSnapshot: true, wantErr: false},

		// CPU bounds (normal).
		{name: "cpu 0 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(0)}, wantErr: true, wantMatch: "cpu_limit"},
		{name: "cpu -1 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(-1)}, wantErr: true, wantMatch: "cpu_limit"},
		{name: "cpu 33 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(33)}, wantErr: true, wantMatch: "cpu_limit"},
		{name: "cpu 1 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(1)}, wantErr: false},
		{name: "cpu 16 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(16)}, wantErr: false},
		{name: "cpu 32 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(32)}, wantErr: false},

		// Memory bounds (normal).
		{name: "mem 0 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(0)}, wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "mem 127 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(127)}, wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "mem 8193 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(8193)}, wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "mem 128 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(128)}, wantErr: false},
		{name: "mem 1024 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(1024)}, wantErr: false},
		{name: "mem 8192 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(8192)}, wantErr: false},

		// From-snapshot: any non-nil rejected.
		{name: "cpu set on from-snapshot", opts: CreateSandboxOpts{CPULimit: ptrInt(2)}, fromSnapshot: true, wantErr: true, wantMatch: "cpu_limit cannot be set on from-snapshot"},
		{name: "mem set on from-snapshot", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(512)}, fromSnapshot: true, wantErr: true, wantMatch: "memory_limit_mb cannot be set on from-snapshot"},
		{name: "both set on from-snapshot reports first", opts: CreateSandboxOpts{CPULimit: ptrInt(2), MemoryLimitMB: ptrInt(512)}, fromSnapshot: true, wantErr: true, wantMatch: "cpu_limit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLimits(tc.opts, tc.fromSnapshot)
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
