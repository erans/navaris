//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestUpdateResources_FC_RejectsCPUChange(t *testing.T) {
	p := &Provider{
		config: Config{VcpuHeadroomMult: 2.0, MemHeadroomMult: 2.0},
		vms:    map[string]*VMInfo{"vm-x": {ID: "vm-x"}},
	}
	cpu := 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-x"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) || prErr.Reason != domain.ResizeReasonCPUUnsupportedByBackend {
		t.Fatalf("err = %v, want ProviderResizeError(cpu_resize_unsupported_by_backend)", err)
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
