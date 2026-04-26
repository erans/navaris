//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
	"github.com/navaris/navaris/internal/storage"
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
	// Storage is required: it owns CoW cloning of rootfs files. Pass a
	// Registry whose roots include ImageDir, ChrootBase, and SnapshotDir.
	Storage *storage.Registry

	// DefaultVcpuCount is used when CreateSandboxRequest.CPULimit is nil.
	// Set via --firecracker-default-vcpu on the daemon.
	DefaultVcpuCount int

	// DefaultMemoryMib is used when CreateSandboxRequest.MemoryLimitMB is
	// nil. Set via --firecracker-default-memory-mb on the daemon. Note
	// that the API field is named ...MB but is fed straight to MemSizeMib;
	// see docs/superpowers/specs/2026-04-25-resource-limits-design.md §3.5.
	DefaultMemoryMib int

	// VcpuHeadroomMult controls boot-time vCPU headroom: every new VM boots
	// with vcpu_count = ceil(limit * VcpuHeadroomMult). Must be >= 1.0.
	// Default 1.0 (no headroom; boot at exact limit). Set > 1.0 to enable
	// grow-resize within the boot-time ceiling.
	// Set via --firecracker-vcpu-headroom-mult.
	VcpuHeadroomMult float64

	// MemHeadroomMult controls boot-time memory headroom analogously.
	// Memory above the user's limit is reclaimed via a balloon device
	// inflated to (ceiling - limit) MiB at boot.
	// Default 1.0 (no headroom). Set > 1.0 to enable grow-resize.
	MemHeadroomMult float64
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
	if c.DefaultVcpuCount == 0 {
		c.DefaultVcpuCount = 1
	}
	if c.DefaultMemoryMib == 0 {
		c.DefaultMemoryMib = 256
	}
	if c.VcpuHeadroomMult == 0 {
		c.VcpuHeadroomMult = 1.0
	}
	if c.MemHeadroomMult == 0 {
		c.MemHeadroomMult = 1.0
	}
}

// Firecracker hardware/policy bounds — must match the validation bounds the
// service layer applies to per-sandbox limits (see internal/service/limits.go).
// Defaults outside this range would let nil-limit sandboxes persist invalid
// machine sizes and only fail at VM boot.
const (
	defaultMinVcpu  = 1
	defaultMaxVcpu  = 32
	defaultMinMemMB = 128
	defaultMaxMemMB = 8192
)

func (c *Config) validateDefaults() error {
	if c.DefaultVcpuCount < defaultMinVcpu || c.DefaultVcpuCount > defaultMaxVcpu {
		return fmt.Errorf("firecracker-default-vcpu=%d out of range %d..%d", c.DefaultVcpuCount, defaultMinVcpu, defaultMaxVcpu)
	}
	if c.DefaultMemoryMib < defaultMinMemMB || c.DefaultMemoryMib > defaultMaxMemMB {
		return fmt.Errorf("firecracker-default-memory-mb=%d out of range %d..%d", c.DefaultMemoryMib, defaultMinMemMB, defaultMaxMemMB)
	}
	if c.VcpuHeadroomMult < 1.0 {
		return fmt.Errorf("firecracker-vcpu-headroom-mult=%g must be >= 1.0", c.VcpuHeadroomMult)
	}
	if c.MemHeadroomMult < 1.0 {
		return fmt.Errorf("firecracker-mem-headroom-mult=%g must be >= 1.0", c.MemHeadroomMult)
	}
	return nil
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
	storage       *storage.Registry
}

// New creates a Firecracker provider and recovers any orphaned VMs.
func New(cfg Config) (*Provider, error) {
	cfg.defaults()

	if err := cfg.validateDefaults(); err != nil {
		return nil, fmt.Errorf("firecracker: %w", err)
	}

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

	if cfg.Storage == nil {
		return nil, fmt.Errorf("firecracker: Storage registry is required")
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
	p.storage = cfg.Storage

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

	// Warm OS page cache so the first VM starts faster.
	go p.warmCache()

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
	if err := p.recoverForkPoints(); err != nil {
		slog.Warn("firecracker forkpoint recover", "error", err)
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

// warmCache reads the kernel and rootfs images sequentially so the OS
// page cache is hot before the first VM boots. This eliminates the cold-start
// penalty (~seconds of disk I/O) that makes the first Firecracker VM feel slow.
func (p *Provider) warmCache() {
	start := time.Now()
	warm := func(path string) {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		io.Copy(io.Discard, f)
	}

	warm(p.config.KernelPath)

	entries, err := os.ReadDir(p.config.ImageDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ext4") {
			warm(filepath.Join(p.config.ImageDir, e.Name()))
		}
	}
	slog.Info("firecracker: page cache warm-up complete", "duration", time.Since(start).Round(time.Millisecond))
}

var _ domain.Provider = (*Provider)(nil)

// detectCgroupVersion returns "2" if the system uses cgroup v2, otherwise "1".
func detectCgroupVersion() string {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return "2"
	}
	return "1"
}
