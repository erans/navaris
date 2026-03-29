//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
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
}

func (c *Config) defaults() {
	if c.ChrootBase == "" {
		c.ChrootBase = "/srv/firecracker"
	}
	if c.VsockCIDBase == 0 {
		c.VsockCIDBase = 100
	}
}

// Provider implements domain.Provider for Firecracker microVMs.
type Provider struct {
	config    Config
	subnets   *network.Allocator
	uids      *jailer.UIDAllocator
	cidNext   uint32
	cidMu     sync.Mutex
	vms       map[string]*VMInfo
	vmMu      sync.RWMutex
	hostIface string
}

// New creates a Firecracker provider and recovers any orphaned VMs.
func New(cfg Config) (*Provider, error) {
	cfg.defaults()

	// Validate required fields.
	for _, check := range []struct{ name, val string }{
		{"firecracker-bin", cfg.FirecrackerBin},
		{"jailer-bin", cfg.JailerBin},
		{"kernel-path", cfg.KernelPath},
		{"image-dir", cfg.ImageDir},
	} {
		if check.val == "" {
			return nil, fmt.Errorf("firecracker: %s is required", check.name)
		}
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
		config:    cfg,
		subnets:   network.NewAllocator(),
		uids:      jailer.NewUIDAllocator(10000),
		cidNext:   cfg.VsockCIDBase,
		vms:       make(map[string]*VMInfo),
		hostIface: hostIface,
	}

	// Recover orphaned VMs from disk.
	if err := p.recover(); err != nil {
		slog.Warn("firecracker: recovery scan", "error", err)
	}

	return p, nil
}

func (p *Provider) recover() error {
	infos, err := ScanVMDirs(p.config.ChrootBase)
	if err != nil {
		return err
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
	}
	return nil
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
