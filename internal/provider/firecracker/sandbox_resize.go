//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"path/filepath"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/navaris/navaris/internal/domain"
)

// UpdateResources applies new memory limit to a running Firecracker VM by
// inflating or deflating the balloon device. CPU live-resize is not
// supported by the pinned firecracker-go-sdk@v1.0.0 (no PatchMachineConfiguration);
// any CPU change request returns ProviderResizeError(cpu_resize_unsupported_by_backend).
func (p *Provider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	if req.CPULimit != nil {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonCPUUnsupportedByBackend,
			Detail: "Firecracker provider in this build does not support live vCPU resize",
		}
	}
	if req.MemoryLimitMB == nil {
		return nil // nothing to do
	}

	p.vmMu.RLock()
	info, ok := p.vms[ref.Ref]
	p.vmMu.RUnlock()
	if !ok {
		return fmt.Errorf("firecracker: vm %q not found: %w", ref.Ref, domain.ErrNotFound)
	}

	newLimit := int64(*req.MemoryLimitMB)
	ceiling := info.CeilingMemMib
	if ceiling == 0 {
		// Pre-headroom sandbox (created before Task 8): treat MemSizeMib as the ceiling.
		ceiling = info.MemSizeMib
	}
	if newLimit > ceiling {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonExceedsCeiling,
			Detail: fmt.Sprintf("memory_limit_mb %d > ceiling %d", newLimit, ceiling),
		}
	}

	balloonAmount := ceiling - newLimit
	if err := p.patchBalloon(ctx, ref.Ref, balloonAmount); err != nil {
		return fmt.Errorf("firecracker: patch balloon: %w", err)
	}

	p.vmMu.Lock()
	info.LimitMemMib = newLimit
	p.vmMu.Unlock()
	if err := info.Write(p.vmInfoPath(ref.Ref)); err != nil {
		return fmt.Errorf("firecracker: persist vminfo after resize: %w", err)
	}
	return nil
}

// patchBalloon issues PATCH /balloon to the running VM's API socket,
// setting amount_mib to the requested value. amountMib of 0 fully deflates
// the balloon (guest sees full memory); amountMib equal to (ceiling -
// limit) at boot reserves that delta from the guest.
func (p *Provider) patchBalloon(ctx context.Context, vmID string, amountMib int64) error {
	var sockPath string
	if p.config.EnableJailer {
		// Under jailer the socket lives inside the chroot root; match snapshot.go pattern.
		sockPath = filepath.Join(p.vmDir(vmID), "root", "run", "firecracker.socket")
	} else {
		sockPath = p.socketPath(vmID)
	}
	machine, err := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
	if err != nil {
		return fmt.Errorf("attach to vm: %w", err)
	}
	return machine.UpdateBalloon(ctx, amountMib)
}
