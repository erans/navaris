package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

// UpdateResourcesOpts describes a CPU / memory resize of an existing sandbox.
type UpdateResourcesOpts struct {
	SandboxID     string
	CPULimit      *int
	MemoryLimitMB *int
	// ApplyLiveOnly skips the SQLite persistence step. The provider is
	// still called when the sandbox is running. Used by the boost path,
	// where the boosted limits are a transient overlay and the persisted
	// columns must continue to track the user's steady-state intent.
	// See docs/superpowers/specs/2026-04-26-sandbox-boost-design.md §3.7.
	ApplyLiveOnly bool
}

// UpdateResourcesResult is what UpdateResources returns on success.
type UpdateResourcesResult struct {
	Sandbox     *domain.Sandbox
	AppliedLive bool
}

// UpdateResources applies new CPU / memory limits to an existing sandbox.
//
// If the sandbox is running, the provider is asked to apply the change live.
// If stopped, only the persisted limits are updated; they take effect on
// next start. Errors:
//   - ErrInvalidArgument: both fields nil, or bounds violation
//   - ErrNotFound: no such sandbox
//   - ErrInvalidState: sandbox is destroyed/failed
//   - *ProviderResizeError: backend rejected the live resize
func (s *SandboxService) UpdateResources(ctx context.Context, opts UpdateResourcesOpts) (*UpdateResourcesResult, error) {
	if opts.CPULimit == nil && opts.MemoryLimitMB == nil {
		return nil, fmt.Errorf("at least one of cpu_limit, memory_limit_mb must be supplied: %w", domain.ErrInvalidArgument)
	}

	sbx, err := s.sandboxes.Get(ctx, opts.SandboxID)
	if err != nil {
		return nil, err
	}

	if sbx.State == domain.SandboxDestroyed || sbx.State == domain.SandboxFailed {
		return nil, fmt.Errorf("cannot resize sandbox in state %s: %w", sbx.State, domain.ErrInvalidState)
	}

	if err := validateResourceBounds(opts.CPULimit, opts.MemoryLimitMB, sbx.Backend); err != nil {
		return nil, err
	}

	prevCPU := sbx.CPULimit
	prevMem := sbx.MemoryLimitMB

	if opts.CPULimit != nil {
		v := *opts.CPULimit
		sbx.CPULimit = &v
	}
	if opts.MemoryLimitMB != nil {
		v := *opts.MemoryLimitMB
		sbx.MemoryLimitMB = &v
	}
	sbx.UpdatedAt = time.Now().UTC()

	if !opts.ApplyLiveOnly {
		if err := s.sandboxes.Update(ctx, sbx); err != nil {
			return nil, fmt.Errorf("persist resize: %w", err)
		}
	}

	appliedLive := false
	if sbx.State == domain.SandboxRunning {
		req := domain.UpdateResourcesRequest{
			CPULimit:      opts.CPULimit,
			MemoryLimitMB: opts.MemoryLimitMB,
		}
		if err := s.provider.UpdateResources(ctx, domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}, req); err != nil {
			// Roll back persisted limits on provider failure so DB state stays
			// consistent with the running VM.
			if !opts.ApplyLiveOnly {
				sbx.CPULimit = prevCPU
				sbx.MemoryLimitMB = prevMem
				if rbErr := s.sandboxes.Update(ctx, sbx); rbErr != nil {
					return nil, fmt.Errorf("provider resize failed: %v; rollback also failed: %w", err, rbErr)
				}
			}
			var prErr *domain.ProviderResizeError
			if errors.As(err, &prErr) {
				return nil, prErr
			}
			return nil, err
		}
		appliedLive = true
	}

	_ = s.events.Publish(ctx, domain.Event{
		Type:      domain.EventSandboxResourcesUpdated,
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"sandbox_id":      sbx.SandboxID,
			"cpu_limit":       sbx.CPULimit,
			"memory_limit_mb": sbx.MemoryLimitMB,
			"applied_live":    appliedLive,
		},
	})

	return &UpdateResourcesResult{Sandbox: sbx, AppliedLive: appliedLive}, nil
}
