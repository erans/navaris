//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
	"github.com/navaris/navaris/internal/telemetry"
)

const backendName = "firecracker"

// Config holds the provider configuration.
type Config struct {
	FirecrackerBin string
	JailerBin      string
	KernelPath     string
	ImageDir       string
	ChrootBase     string
	VsockCIDBase   uint32
	HostInterface  string
	SnapshotDir    string
	EnableJailer   bool
}

func (c *Config) defaults() {
	if c.ChrootBase == "" {
		c.ChrootBase = "/srv/firecracker"
	}
	if c.VsockCIDBase == 0 {
		c.VsockCIDBase = 100
	}
	if c.SnapshotDir == "" {
		c.SnapshotDir = "/srv/firecracker/snapshots"
	}
}

// Provider implements domain.Provider for Firecracker microVMs.
type Provider struct {
	config        Config
	subnets       *network.Allocator
	uids          *jailer.UIDAllocator
	portAlloc     *network.PortAllocator
	cidNext       uint32
	cidMu         sync.Mutex
	vms           map[string]*VMInfo
	vmMu          sync.RWMutex
	hostIface     string
	cgroupVersion string
}

// New creates a Firecracker provider and recovers any orphaned VMs.
func New(cfg Config) (*Provider, error) {
	cfg.defaults()

	if err := os.MkdirAll(cfg.SnapshotDir, 0o755); err != nil {
		return nil, fmt.Errorf("firecracker: create snapshot dir: %w", err)
	}

	// Validate required fields.
	for _, check := range []struct{ name, val string }{
		{"firecracker-bin", cfg.FirecrackerBin},
		{"kernel-path", cfg.KernelPath},
		{"image-dir", cfg.ImageDir},
	} {
		if check.val == "" {
			return nil, fmt.Errorf("firecracker: %s is required", check.name)
		}
	}

	if cfg.EnableJailer && cfg.JailerBin == "" {
		return nil, fmt.Errorf("firecracker: jailer-bin is required when jailer is enabled")
	}

	// Detect host interface.
	hostIface := cfg.HostInterface
	if hostIface == "" {
		detected, err := network.DetectDefaultInterface()
		if err != nil {
			return nil, fmt.Errorf("firecracker: %w", err)
		}
		hostIface = detected
	}

	// Check ip_forward.
	if err := network.CheckIPForward(); err != nil {
		return nil, fmt.Errorf("firecracker: %w", err)
	}

	p := &Provider{
		config:        cfg,
		subnets:       network.NewAllocator(),
		uids:          jailer.NewUIDAllocator(10000),
		portAlloc:     network.NewPortAllocator(),
		cidNext:       cfg.VsockCIDBase,
		vms:           make(map[string]*VMInfo),
		hostIface:     hostIface,
		cgroupVersion: detectCgroupVersion(),
	}

	// Recover orphaned VMs from disk.
	if err := p.recover(); err != nil {
		slog.Warn("firecracker: recovery scan", "error", err)
	}

	telemetry.RegisterSandboxCountGauge(backendName, func() map[string]int64 {
		p.vmMu.RLock()
		defer p.vmMu.RUnlock()
		counts := map[string]int64{}
		for _, info := range p.vms {
			switch {
			case info.Stopping:
				counts["stopping"]++
			case info.PID > 0 && processAlive(info.PID):
				counts["running"]++
			default:
				counts["stopped"]++
			}
		}
		return counts
	})

	return p, nil
}

func (p *Provider) recover() error {
	var pattern string
	if p.config.EnableJailer {
		pattern = filepath.Join(p.config.ChrootBase, "firecracker", "nvrs-fc-*")
	} else {
		pattern = filepath.Join(p.config.ChrootBase, "nvrs-fc-*")
	}
	infos, errs := ScanVMDirsGlob(pattern)
	for _, e := range errs {
		slog.Warn("firecracker: recovery skip", "error", e)
	}
	for _, info := range infos {
		p.vmMu.Lock()
		p.vms[info.ID] = info
		p.vmMu.Unlock()

		// Advance allocators past in-use values.
		p.cidMu.Lock()
		if info.CID >= p.cidNext {
			p.cidNext = info.CID + 1
		}
		p.cidMu.Unlock()
		p.uids.InitPast(info.UID)
		if info.TapDevice != "" {
			p.subnets.InitPast(info.SubnetIdx)
		}
		slog.Info("firecracker: recovered VM", "id", info.ID, "pid", info.PID)

		// Port recovery: re-establish or clean up port rules.
		if len(info.Ports) > 0 {
			alive := info.PID > 0 && processAlive(info.PID)
			if alive && info.TapDevice != "" {
				// Running VM — re-establish iptables rules.
				guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
				for hp, tp := range info.Ports {
					p.portAlloc.MarkUsed(hp)
					if err := network.AddDNAT(hp, guestIP, tp); err != nil {
						slog.Warn("firecracker: recovery re-add dnat", "vm", info.ID, "port", hp, "error", err)
					}
				}
			} else {
				// Dead VM — best-effort remove stale rules, clear ports.
				if info.TapDevice != "" {
					guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
					for hp, tp := range info.Ports {
						network.RemoveDNAT(hp, guestIP, tp)
					}
				}
				info.Ports = nil
				infoPath := p.vmInfoPath(info.ID)
				if err := info.Write(infoPath); err != nil {
					slog.Warn("firecracker: recovery write vminfo", "vm", info.ID, "error", err)
				}
			}
		}
	}
	return nil
}

func (p *Provider) vmDir(vmID string) string {
	if p.config.EnableJailer {
		return jailer.ChrootPath(p.config.ChrootBase, vmID)
	}
	return filepath.Join(p.config.ChrootBase, vmID)
}

func (p *Provider) vmInfoPath(vmID string) string {
	return filepath.Join(p.vmDir(vmID), "vminfo.json")
}

func (p *Provider) vsockPath(vmID string) string {
	if p.config.EnableJailer {
		return filepath.Join(p.vmDir(vmID), "root", "vsock")
	}
	return filepath.Join(p.vmDir(vmID), "vsock")
}

// vsockUDSPath returns the uds_path value for the Firecracker vsock config.
// With jailer it's relative (to chroot root); without it's absolute.
func (p *Provider) vsockUDSPath(vmID string) string {
	if p.config.EnableJailer {
		return "vsock"
	}
	return filepath.Join(p.vmDir(vmID), "vsock")
}

func (p *Provider) socketPath(vmID string) string {
	if p.config.EnableJailer {
		return "firecracker.sock" // relative, SDK translates via jailer chroot
	}
	return filepath.Join(p.vmDir(vmID), "firecracker.sock") // absolute
}

func (p *Provider) allocateCID() uint32 {
	p.cidMu.Lock()
	defer p.cidMu.Unlock()
	cid := p.cidNext
	p.cidNext++
	return cid
}

// Health checks if the Firecracker binary is accessible.
func (p *Provider) Health(ctx context.Context) domain.ProviderHealth {
	start := time.Now()
	_, err := os.Stat(p.config.FirecrackerBin)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return domain.ProviderHealth{
			Backend: backendName, Healthy: false,
			LatencyMS: latency, Error: fmt.Sprintf("firecracker binary not found: %v", err),
		}
	}
	return domain.ProviderHealth{
		Backend: backendName, Healthy: true, LatencyMS: latency,
	}
}

var _ domain.Provider = (*Provider)(nil)

// detectCgroupVersion returns "2" if the system uses cgroup v2, otherwise "1".
func detectCgroupVersion() string {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return "2"
	}
	return "1"
}
