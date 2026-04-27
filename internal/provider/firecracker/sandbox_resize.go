//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/navaris/navaris/internal/domain"
)

// UpdateResources applies new CPU and/or memory limits live to a running
// Firecracker VM. CPU is enforced via cgroup CPU bandwidth (cpu.max v2 /
// cfs_quota_us v1) on the per-VM cgroup created at boot; memory is enforced
// via the virtio-balloon device.
//
// Atomicity: the implementation validates BOTH CPU and memory bounds first,
// then applies them in order (CPU → memory). If memory application fails
// after CPU has already been written to cgroup, we attempt to revert the
// cgroup write to the prior LimitCPU. We persist vminfo.json only after
// every requested mutation has succeeded. The service layer's SQLite
// rollback assumption (no provider-side mutation on error) is therefore
// honored unless the cgroup revert itself fails — a rare double-fault we
// log and surface so the operator can reconcile by hand.
func (p *Provider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	if req.CPULimit == nil && req.MemoryLimitMB == nil {
		return nil
	}

	p.vmMu.RLock()
	info, ok := p.vms[ref.Ref]
	p.vmMu.RUnlock()
	if !ok {
		return fmt.Errorf("firecracker: vm %q not found: %w", ref.Ref, domain.ErrNotFound)
	}

	// Validate first — fail fast before mutating anything.
	if req.CPULimit != nil {
		newCPU := int64(*req.CPULimit)
		ceiling := info.CeilingCPU
		if ceiling == 0 {
			// Pre-headroom sandbox (created before the spec): boot vCPU count
			// IS the ceiling — there's no extra headroom.
			ceiling = info.VcpuCount
		}
		if newCPU > ceiling {
			return &domain.ProviderResizeError{
				Reason: domain.ResizeReasonExceedsCeiling,
				Detail: fmt.Sprintf("cpu_limit %d over ceiling %d", newCPU, ceiling),
			}
		}
		if !info.CgroupActive {
			return &domain.ProviderResizeError{
				Reason: domain.ResizeReasonCgroupUnavailable,
				Detail: "boot-time cgroup setup did not succeed for this VM; restart it to enable live CPU resize",
			}
		}
	}
	var memCeiling, newMem int64
	if req.MemoryLimitMB != nil {
		newMem = int64(*req.MemoryLimitMB)
		memCeiling = info.CeilingMemMib
		if memCeiling == 0 {
			memCeiling = info.MemSizeMib
		}
		if newMem > memCeiling {
			return &domain.ProviderResizeError{
				Reason: domain.ResizeReasonExceedsCeiling,
				Detail: fmt.Sprintf("memory_limit_mb %d > ceiling %d", newMem, memCeiling),
			}
		}
	}

	// Apply CPU first (cgroup write is local + fast). On success, capture
	// the prior LimitCPU so we can revert if memory application fails. Use
	// effectiveCPULimit so legacy vminfo.json records (LimitCPU == 0) revert
	// to a sane non-zero quota instead of "no CPU at all".
	priorCPU := p.effectiveCPULimit(info)
	priorMem := info.LimitMemMib
	if priorMem == 0 {
		priorMem = info.MemSizeMib
	}
	priorBalloon := memCeiling - priorMem
	if memCeiling == 0 {
		priorBalloon = 0
	}

	cpuApplied := false
	if req.CPULimit != nil {
		newCPU := int64(*req.CPULimit)
		quota := newCPU * cpuPeriod
		if err := p.writeCPUMax(p.cgroupCPUDir(ref.Ref), quota, cpuPeriod); err != nil {
			return &domain.ProviderResizeError{
				Reason: domain.ResizeReasonCgroupWriteFailed,
				Detail: err.Error(),
			}
		}
		cpuApplied = true
	}

	// Apply memory (balloon — slower, may fail mid-operation).
	if req.MemoryLimitMB != nil {
		balloonAmount := memCeiling - newMem
		if err := p.patchBalloon(ctx, ref.Ref, balloonAmount); err != nil {
			// Best-effort revert of CPU so the operator's view of the running
			// VM matches the SQLite state the service layer is about to roll
			// back to. If revert fails, log and surface a multi-failure error
			// so the operator can reconcile by hand.
			if cpuApplied {
				revertQuota := priorCPU * cpuPeriod
				if revertErr := p.writeCPUMax(p.cgroupCPUDir(ref.Ref), revertQuota, cpuPeriod); revertErr != nil {
					return fmt.Errorf("firecracker: patch balloon: %w; cgroup revert ALSO failed: %v (vm cpu/mem are inconsistent — operator must reconcile)", err, revertErr)
				}
			}
			return fmt.Errorf("firecracker: patch balloon: %w", err)
		}
	}

	// Both branches succeeded — commit to in-memory state and disk. If the
	// vminfo.json write fails, attempt to revert: undo in-memory updates
	// and revert the running VM to the prior limits, so the service layer's
	// SQLite rollback leaves the system in a consistent state.
	p.vmMu.Lock()
	if req.CPULimit != nil {
		info.LimitCPU = int64(*req.CPULimit)
	}
	if req.MemoryLimitMB != nil {
		info.LimitMemMib = newMem
	}
	p.vmMu.Unlock()
	if err := info.Write(p.vmInfoPath(ref.Ref)); err != nil {
		// Revert in-memory state.
		p.vmMu.Lock()
		if req.CPULimit != nil {
			info.LimitCPU = priorCPU
		}
		if req.MemoryLimitMB != nil {
			info.LimitMemMib = priorMem
		}
		p.vmMu.Unlock()
		// Best-effort revert of the running VM. Log multi-failure if either
		// revert step itself fails — the operator needs to reconcile.
		if cpuApplied {
			revertQuota := priorCPU * cpuPeriod
			if rerr := p.writeCPUMax(p.cgroupCPUDir(ref.Ref), revertQuota, cpuPeriod); rerr != nil {
				return fmt.Errorf("firecracker: persist vminfo after resize: %w; cgroup revert ALSO failed: %v", err, rerr)
			}
		}
		if req.MemoryLimitMB != nil {
			if rerr := p.patchBalloon(ctx, ref.Ref, priorBalloon); rerr != nil {
				return fmt.Errorf("firecracker: persist vminfo after resize: %w; balloon revert ALSO failed: %v", err, rerr)
			}
		}
		return fmt.Errorf("firecracker: persist vminfo after resize: %w", err)
	}
	return nil
}

// patchBalloon issues PATCH /balloon to the running VM's API socket,
// setting amount_mib to the requested value. amountMib of 0 fully deflates
// the balloon (guest sees full memory); amountMib equal to (ceiling -
// limit) at boot reserves that delta from the guest.
//
// The guest's virtio-balloon driver activates as part of guest userspace
// init, which can lag a few hundred milliseconds behind the navarisd
// "running" state transition. PATCH /balloon arriving before activation
// is rejected with HTTP 400 "Device not activated yet"; we retry with
// linear backoff for up to ~3s before surfacing the error.
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

	const maxAttempts = 10
	const retryDelay = 300 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := machine.UpdateBalloon(ctx, amountMib); err == nil {
			return nil
		} else {
			lastErr = err
			if !strings.Contains(err.Error(), "not activated") {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
	return lastErr
}
