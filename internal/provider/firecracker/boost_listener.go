//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/navaris/navaris/internal/provider"
)

func (p *Provider) startBoostListener(ctx context.Context, vmID string) error {
	if p.boostHandler == nil {
		return nil
	}

	p.vmMu.RLock()
	info, ok := p.vms[vmID]
	p.vmMu.RUnlock()
	if !ok {
		return fmt.Errorf("vm %s not registered", vmID)
	}
	if info.SandboxID == "" {
		return fmt.Errorf("vm %s has no SandboxID; cannot bind boost channel", vmID)
	}

	udsPath := p.boostUDSPath(vmID)
	_ = os.Remove(udsPath)

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", udsPath, err)
	}
	if p.config.EnableJailer {
		_ = os.Chown(udsPath, info.UID, info.UID)
	}

	listenerCtx, cancel := context.WithCancel(ctx)
	bl := &boostListener{vmID: vmID, sandboxID: info.SandboxID, udsPath: udsPath, listener: ln, cancel: cancel}

	p.boostMu.Lock()
	p.boostListeners[vmID] = bl
	p.boostMu.Unlock()

	go bl.acceptLoop(listenerCtx, p.boostHandler)
	slog.Info("firecracker: boost listener started", "vm", vmID, "sandbox", info.SandboxID, "path", udsPath)
	return nil
}

func (p *Provider) stopBoostListener(vmID string) {
	p.boostMu.Lock()
	bl, ok := p.boostListeners[vmID]
	delete(p.boostListeners, vmID)
	p.boostMu.Unlock()
	if !ok {
		return
	}
	bl.cancel()
	bl.listener.Close()
	_ = os.Remove(bl.udsPath)
}

func (bl *boostListener) acceptLoop(ctx context.Context, handler provider.BoostServer) {
	for {
		conn, err := bl.listener.Accept()
		if err != nil {
			return
		}
		go handler.Serve(ctx, conn, bl.sandboxID)
	}
}

// RestartBoostListeners walks the provider's known VMs and starts a boost
// listener for each live VM whose vminfo has EnableBoostChannel=true.
// Call this from the daemon entrypoint after SetBoostHandler is wired —
// recover() runs before the boost handler is set, so we replay here.
func (p *Provider) RestartBoostListeners(ctx context.Context) {
	if p.boostHandler == nil {
		return
	}
	p.vmMu.RLock()
	candidates := make([]*VMInfo, 0, len(p.vms))
	for _, info := range p.vms {
		candidates = append(candidates, info)
	}
	p.vmMu.RUnlock()

	for _, info := range candidates {
		if !info.EnableBoostChannel || info.SandboxID == "" {
			continue
		}
		if info.PID <= 0 || !processAlive(info.PID) {
			continue
		}
		if err := p.startBoostListener(ctx, info.ID); err != nil {
			slog.Warn("firecracker: replay boost listener", "vm", info.ID, "err", err)
		}
	}
}

func (p *Provider) boostUDSPath(vmID string) string {
	if p.config.EnableJailer {
		return filepath.Join(p.vmDir(vmID), "root", "vsock_1025")
	}
	return filepath.Join(p.vmDir(vmID), "vsock_1025")
}
