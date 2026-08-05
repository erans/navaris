//go:build firecracker

package firecracker

import (
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

// lockFor returns the per-VM file lock held, or an error wrapping
// domain.ErrVMStopped if the VM is mid-teardown. Caller must unlock fl.mu.
//
// Lookup is under p.vmMu; the per-VM mu is acquired standalone so different
// VMs never block each other. Fail-fast: late writers receive the error
// immediately rather than blocking on a stopping VM.
func (p *Provider) lockFor(vmID string) (*vmFileLock, error) {
	p.vmMu.Lock()
	fl, ok := p.fileMu[vmID]
	if !ok {
		fl = &vmFileLock{}
		p.fileMu[vmID] = fl
	}
	// Fast path: check stopped under vmMu so a late writer fails immediately
	// WITHOUT acquiring fl.mu — which StopSandbox holds for its entire
	// teardown (up to 30s). Acquiring fl.mu first would block, defeating
	// fail-fast.
	if fl.stopped.Load() {
		p.vmMu.Unlock()
		return nil, fmt.Errorf("firecracker: vm %s is stopping: %w", vmID, domain.ErrVMStopped)
	}
	p.vmMu.Unlock()

	fl.mu.Lock()
	// Re-check after acquiring: StopSandbox may have flipped stopped between
	// the vmMu.Unlock above and this Lock. If so, bail.
	if fl.stopped.Load() {
		fl.mu.Unlock()
		return nil, fmt.Errorf("firecracker: vm %s is stopping: %w", vmID, domain.ErrVMStopped)
	}
	return fl, nil
}
