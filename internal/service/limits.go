package service

import (
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

// Backend identifiers for backend-specific limit bounds.
const (
	backendFirecracker = "firecracker"
)

// firecracker bounds match Firecracker's hardware/policy:
//   - 32 = MAX_SUPPORTED_VCPUS in upstream Firecracker.
//   - 128 MB = the floor for booting most modern guest kernels.
//   - 8192 MB = sandbox-policy ceiling.
const (
	limitFCMinCPU   = 1
	limitFCMaxCPU   = 32
	limitFCMinMemMB = 128
	limitFCMaxMemMB = 8192
)

// generic bounds apply to non-Firecracker backends (Incus, mock, etc).
// Generous because containers can run with much higher limits than VMs;
// these bounds exist purely as sanity checks against absurd inputs.
const (
	limitGenericMinCPU   = 1
	limitGenericMaxCPU   = 256
	limitGenericMinMemMB = 16
	limitGenericMaxMemMB = 524288 // 512 GiB
)

// validateResourceBounds checks raw CPU / memory pointers against
// backend-specific bounds. Wraps domain.ErrInvalidArgument so the API maps
// to 400. Nil pointers are allowed and treated as "unchanged".
func validateResourceBounds(cpu *int, mem *int, backend string) error {
	minCPU, maxCPU, minMem, maxMem := limitGenericMinCPU, limitGenericMaxCPU, limitGenericMinMemMB, limitGenericMaxMemMB
	if backend == backendFirecracker {
		minCPU, maxCPU, minMem, maxMem = limitFCMinCPU, limitFCMaxCPU, limitFCMinMemMB, limitFCMaxMemMB
	}
	if cpu != nil {
		v := *cpu
		if v < minCPU || v > maxCPU {
			return fmt.Errorf("cpu_limit must be %d..%d for backend %q, got %d: %w", minCPU, maxCPU, backend, v, domain.ErrInvalidArgument)
		}
	}
	if mem != nil {
		v := *mem
		if v < minMem || v > maxMem {
			return fmt.Errorf("memory_limit_mb must be %d..%d for backend %q, got %d: %w", minMem, maxMem, backend, v, domain.ErrInvalidArgument)
		}
	}
	return nil
}

// validateLimits is the create-time bounds check; preserved for callers in
// service/sandbox.go.
func validateLimits(opts CreateSandboxOpts, backend string) error {
	return validateResourceBounds(opts.CPULimit, opts.MemoryLimitMB, backend)
}
