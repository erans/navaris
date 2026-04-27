//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
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

func (bl *boostListener) acceptLoop(ctx context.Context, handler boostServer) {
	for {
		conn, err := bl.listener.Accept()
		if err != nil {
			return
		}
		go handler.Serve(ctx, conn, bl.sandboxID)
	}
}

func (p *Provider) boostUDSPath(vmID string) string {
	if p.config.EnableJailer {
		return filepath.Join(p.vmDir(vmID), "root", "vsock_1025")
	}
	return filepath.Join(p.vmDir(vmID), "vsock_1025")
}
