package service

import (
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

// Sandbox resource limit bounds. The CPU upper bound is Firecracker's
// MAX_SUPPORTED_VCPUS (32). The memory bounds are policy: 128 MB is the
// floor where most modern guest kernels boot without panic; 8192 MB is a
// sane sandbox ceiling. Operators who need higher must edit these
// constants — no daemon flag is exposed (deliberately, to keep policy
// decisions out of operator hands at this stage).
const (
	limitMinCPU   = 1
	limitMaxCPU   = 32
	limitMinMemMB = 128
	limitMaxMemMB = 8192
)

// validateLimits checks CPULimit / MemoryLimitMB against the bounds
// above and rejects any non-nil value when fromSnapshot is true (snapshot
// restores carry vmstate.bin-baked values; an override would silently or
// noisily diverge). Returns nil if all checks pass.
//
// Errors wrap domain.ErrInvalidArgument so the API maps them to 400.
func validateLimits(opts CreateSandboxOpts, fromSnapshot bool) error {
	if fromSnapshot {
		if opts.CPULimit != nil {
			return fmt.Errorf("cpu_limit cannot be set on from-snapshot create; vCPU count is baked into the snapshot: %w", domain.ErrInvalidArgument)
		}
		if opts.MemoryLimitMB != nil {
			return fmt.Errorf("memory_limit_mb cannot be set on from-snapshot create; memory size is baked into the snapshot: %w", domain.ErrInvalidArgument)
		}
		return nil
	}
	if opts.CPULimit != nil {
		v := *opts.CPULimit
		if v < limitMinCPU || v > limitMaxCPU {
			return fmt.Errorf("cpu_limit must be %d..%d, got %d: %w", limitMinCPU, limitMaxCPU, v, domain.ErrInvalidArgument)
		}
	}
	if opts.MemoryLimitMB != nil {
		v := *opts.MemoryLimitMB
		if v < limitMinMemMB || v > limitMaxMemMB {
			return fmt.Errorf("memory_limit_mb must be %d..%d, got %d: %w", limitMinMemMB, limitMaxMemMB, v, domain.ErrInvalidArgument)
		}
	}
	return nil
}
