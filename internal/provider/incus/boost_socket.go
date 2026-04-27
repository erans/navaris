//go:build incus

package incus

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
)

// incusBoostListener mirrors the Firecracker per-VM listener: each container
// gets one host UDS, bind-mounted into the container at /var/run/navaris-guest.sock
// via Incus's unix-socket device type.
type incusBoostListener struct {
	containerName string
	sandboxID     string
	udsPath       string
	listener      net.Listener
	cancel        context.CancelFunc
}

const boostDeviceName = "navaris-boost"

func (p *IncusProvider) startBoostChannel(ctx context.Context, name string, sandboxID string) error {
	if p.boostHandler == nil || p.config.BoostChannelDir == "" {
		return nil
	}

	if err := os.MkdirAll(p.config.BoostChannelDir, 0o755); err != nil {
		return fmt.Errorf("create boost channel dir: %w", err)
	}

	udsPath := filepath.Join(p.config.BoostChannelDir, sandboxID+".sock")
	_ = os.Remove(udsPath)

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", udsPath, err)
	}
	_ = os.Chmod(udsPath, 0o666)

	listenerCtx, cancel := context.WithCancel(ctx)
	bl := &incusBoostListener{containerName: name, sandboxID: sandboxID, udsPath: udsPath, listener: ln, cancel: cancel}

	p.boostMu.Lock()
	p.boostListeners[name] = bl
	p.boostMu.Unlock()

	go bl.acceptLoop(listenerCtx, p.boostHandler)

	// Add the unix-socket device that bind-mounts the host UDS into the container.
	inst, etag, err := p.client.GetInstance(name)
	if err != nil {
		ln.Close()
		os.Remove(udsPath)
		return fmt.Errorf("get instance for boost device add: %w", err)
	}
	if inst.Devices == nil {
		inst.Devices = map[string]map[string]string{}
	}
	inst.Devices[boostDeviceName] = map[string]string{
		"type":   "unix-socket",
		"source": udsPath,
		"path":   "/var/run/navaris-guest.sock",
	}
	op, err := p.client.UpdateInstance(name, inst.Writable(), etag)
	if err != nil {
		ln.Close()
		os.Remove(udsPath)
		return fmt.Errorf("incus update instance (add boost device): %w", err)
	}
	if err := op.WaitContext(ctx); err != nil {
		ln.Close()
		os.Remove(udsPath)
		return fmt.Errorf("incus update wait: %w", err)
	}

	slog.Info("incus: boost channel started", "container", name, "path", udsPath)
	return nil
}

func (p *IncusProvider) stopBoostChannel(name string) {
	p.boostMu.Lock()
	bl, ok := p.boostListeners[name]
	delete(p.boostListeners, name)
	p.boostMu.Unlock()
	if !ok {
		return
	}
	bl.cancel()
	bl.listener.Close()
	_ = os.Remove(bl.udsPath)

	// Best-effort: remove the device from the container if it still exists.
	if inst, etag, err := p.client.GetInstance(name); err == nil {
		if _, has := inst.Devices[boostDeviceName]; has {
			delete(inst.Devices, boostDeviceName)
			if op, err := p.client.UpdateInstance(name, inst.Writable(), etag); err == nil {
				_ = op.WaitContext(context.Background())
			}
		}
	}
}

func (bl *incusBoostListener) acceptLoop(ctx context.Context, handler boostServer) {
	for {
		conn, err := bl.listener.Accept()
		if err != nil {
			return
		}
		go handler.Serve(ctx, conn, bl.sandboxID)
	}
}
