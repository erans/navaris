//go:build firecracker

package firecracker

import (
	"strings"
	"testing"
)

func TestValidateDefaults(t *testing.T) {
	cases := []struct {
		name    string
		vcpu    int
		mem     int
		wantErr string
	}{
		{"ok", 2, 512, ""},
		{"vcpu_too_low", 0, 512, "firecracker-default-vcpu"},
		{"vcpu_negative", -1, 512, "firecracker-default-vcpu"},
		{"vcpu_too_high", 33, 512, "firecracker-default-vcpu"},
		{"mem_too_low", 1, 64, "firecracker-default-memory-mb"},
		{"mem_too_high", 1, 8193, "firecracker-default-memory-mb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				DefaultVcpuCount: tc.vcpu,
				DefaultMemoryMib: tc.mem,
				VcpuHeadroomMult: 2.0,
				MemHeadroomMult:  2.0,
			}
			err := c.validateDefaults()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q should contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateDefaults_HeadroomMultipliers(t *testing.T) {
	cases := []struct {
		name        string
		vcpuMult    float64
		memMult     float64
		wantErrPart string
	}{
		{"vcpu below 1.0", 0.5, 2.0, "vcpu-headroom-mult"},
		{"mem below 1.0", 2.0, 0.5, "mem-headroom-mult"},
		{"both at 1.0 ok", 1.0, 1.0, ""},
		{"both at 4.0 ok", 4.0, 4.0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				DefaultVcpuCount: 1,
				DefaultMemoryMib: 256,
				VcpuHeadroomMult: tc.vcpuMult,
				MemHeadroomMult:  tc.memMult,
			}
			err := c.validateDefaults()
			if tc.wantErrPart == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErrPart)
			}
		})
	}
}

func TestValidateDefaults_CgroupRoot(t *testing.T) {
	cases := []struct {
		name        string
		root        string
		wantErrPart string
	}{
		{"empty ok (defaults applied later)", "", ""},
		{"under unified hierarchy ok", "/sys/fs/cgroup/navaris-fc", ""},
		{"nested under unified hierarchy ok", "/sys/fs/cgroup/team/navaris-fc", ""},
		{"unified root itself ok", "/sys/fs/cgroup", ""},
		{"non-/sys/fs/cgroup rejected", "/mnt/cgroups/navaris", "cgroup-root"},
		{"tmp path rejected", "/tmp/nav", "cgroup-root"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				DefaultVcpuCount: 1,
				DefaultMemoryMib: 256,
				VcpuHeadroomMult: 1.0,
				MemHeadroomMult:  1.0,
				CgroupRoot:       tc.root,
			}
			err := c.validateDefaults()
			if tc.wantErrPart == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrPart)
			}
			if !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErrPart)
			}
		})
	}
}

// TestValidateDefaults_CgroupRoot_JailerSkipsValidation guards that an
// invalid CgroupRoot does NOT fail jailer-mode startup, since CgroupRoot
// is unused under jailer (the jailer manages its own cgroup tree).
func TestValidateDefaults_CgroupRoot_JailerSkipsValidation(t *testing.T) {
	c := Config{
		DefaultVcpuCount: 1,
		DefaultMemoryMib: 256,
		VcpuHeadroomMult: 1.0,
		MemHeadroomMult:  1.0,
		CgroupRoot:       "/mnt/old-non-jailer-config", // would be rejected without the jailer skip
		EnableJailer:     true,
	}
	if err := c.validateDefaults(); err != nil {
		t.Errorf("jailer mode must not validate CgroupRoot, got: %v", err)
	}
}
