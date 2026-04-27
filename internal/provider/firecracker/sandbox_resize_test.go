//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestUpdateResources_CPU_AppliedViaCgroup(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config: Config{
			CgroupRoot:   tmp,
			ChrootBase:   tmp,
			EnableJailer: false,
		},
		cgroupVersion:     "2",
		cgroupSkipFSCheck: true, // tempdir is tmpfs, not cgroupfs
		vms: map[string]*VMInfo{
			"vm-cpu": {
				ID:           "vm-cpu",
				PID:          os.Getpid(),
				LimitCPU:     1,
				CeilingCPU:   4,
				CgroupActive: true,
			},
		},
	}
	// Pre-create the cgroup directory like setupCgroup would have at boot
	// (we're skipping setupCgroup here and testing the resize path
	// in isolation).
	cgDir := filepath.Join(tmp, "vm-cpu")
	if err := os.MkdirAll(cgDir, 0755); err != nil {
		t.Fatal(err)
	}
	// vmInfoPath() uses ChrootBase/<vmID>/vminfo.json — same dir.

	cpu := 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-cpu"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}

	// Verify cpu.max was written.
	got, err := os.ReadFile(filepath.Join(cgDir, "cpu.max"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "200000 100000" {
		t.Errorf("cpu.max = %q, want %q", string(got), "200000 100000")
	}

	// Verify VMInfo.LimitCPU was updated.
	if p.vms["vm-cpu"].LimitCPU != 2 {
		t.Errorf("LimitCPU = %d, want 2", p.vms["vm-cpu"].LimitCPU)
	}
}

func TestUpdateResources_CPU_ExceedsCeiling(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config:            Config{CgroupRoot: tmp, ChrootBase: tmp, EnableJailer: false},
		cgroupVersion:     "2",
		cgroupSkipFSCheck: true,
		vms: map[string]*VMInfo{
			"vm-c": {
				ID:           "vm-c",
				PID:          os.Getpid(),
				LimitCPU:     1,
				CeilingCPU:   2,
				CgroupActive: true,
			},
		},
	}
	cpu := 4 // > ceiling of 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-c"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("expected ProviderResizeError, got %T: %v", err, err)
	}
	if prErr.Reason != domain.ResizeReasonExceedsCeiling {
		t.Errorf("reason = %q, want %q", prErr.Reason, domain.ResizeReasonExceedsCeiling)
	}
}

func TestUpdateResources_CPU_NoCgroup_Unavailable(t *testing.T) {
	p := &Provider{
		cgroupVersion: "2",
		vms: map[string]*VMInfo{
			"vm-nc": {
				ID:           "vm-nc",
				LimitCPU:     1,
				CeilingCPU:   4,
				CgroupActive: false, // setup failed at boot
			},
		},
	}
	cpu := 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-nc"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("expected ProviderResizeError, got %T", err)
	}
	if prErr.Reason != domain.ResizeReasonCgroupUnavailable {
		t.Errorf("reason = %q, want %q", prErr.Reason, domain.ResizeReasonCgroupUnavailable)
	}
}

func TestUpdateResources_FC_MemoryAboveCeiling(t *testing.T) {
	p := &Provider{
		config: Config{VcpuHeadroomMult: 2.0, MemHeadroomMult: 2.0},
		vms: map[string]*VMInfo{
			"vm-1": {ID: "vm-1", LimitMemMib: 256, CeilingMemMib: 512, MemSizeMib: 512},
		},
	}
	mem := 1024 // above ceiling 512
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-1"},
		domain.UpdateResourcesRequest{MemoryLimitMB: &mem})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) || prErr.Reason != domain.ResizeReasonExceedsCeiling {
		t.Fatalf("err = %v, want ProviderResizeError(exceeds_ceiling)", err)
	}
}

func TestUpdateResources_FC_VMNotFound(t *testing.T) {
	p := &Provider{
		config: Config{VcpuHeadroomMult: 2.0, MemHeadroomMult: 2.0},
		vms:    map[string]*VMInfo{},
	}
	mem := 256
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "missing"},
		domain.UpdateResourcesRequest{MemoryLimitMB: &mem})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
