//go:build incus

package incus

import (
	"context"
	"fmt"
	"sync"
	"time"

	incusclient "github.com/lxc/incus/v6/client"
	incusapi "github.com/lxc/incus/v6/shared/api"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/telemetry"
)

const backendName = "incus"

// Config holds the configuration for the Incus provider.
type Config struct {
	// Socket is the path to the Incus unix socket.
	// Default: /var/lib/incus/unix.socket
	Socket string

	// PortRangeMin is the lower bound of the host port range used for
	// published sandbox ports. Default: 40000.
	PortRangeMin int

	// PortRangeMax is the upper bound of the host port range used for
	// published sandbox ports. Default: 49999.
	PortRangeMax int

	// Pool is the Incus storage pool name to inspect at startup. Default:
	// "default". Only used by the CoW capability check; sandbox creation
	// continues to use whichever pool Incus has configured for the project.
	Pool string

	// StrictPoolCoW: when true, the daemon refuses to start if the
	// configured Incus pool's driver is not CoW-capable. When false (default),
	// such pools produce a warning log only.
	StrictPoolCoW bool
}

func (c *Config) defaults() {
	if c.Socket == "" {
		c.Socket = "/var/lib/incus/unix.socket"
	}
	if c.PortRangeMin == 0 {
		c.PortRangeMin = 40000
	}
	if c.PortRangeMax == 0 {
		c.PortRangeMax = 49999
	}
	if c.Pool == "" {
		c.Pool = "default"
	}
}

// IncusProvider implements domain.Provider backed by an Incus instance server.
type IncusProvider struct {
	client incusclient.InstanceServer
	config Config

	// portMu guards nextPort to avoid races when allocating host ports.
	portMu   sync.Mutex
	nextPort int
}

// Verify interface compliance at compile time.
var _ domain.Provider = (*IncusProvider)(nil)

// New connects to the Incus daemon via the unix socket specified in cfg and
// returns a ready-to-use provider.
func New(cfg Config) (*IncusProvider, error) {
	cfg.defaults()

	client, err := incusclient.ConnectIncusUnix(cfg.Socket, nil)
	if err != nil {
		return nil, fmt.Errorf("incus connect %s: %w", cfg.Socket, err)
	}

	fetchDriver := func(ctx context.Context) (string, error) {
		pool, _, err := client.GetStoragePool(cfg.Pool)
		if err != nil {
			return "", fmt.Errorf("incus get storage pool %q: %w", cfg.Pool, err)
		}
		return pool.Driver, nil
	}
	if err := CheckPool(context.Background(), fetchDriver, cfg.StrictPoolCoW); err != nil {
		return nil, err
	}

	p := &IncusProvider{
		client:   client,
		config:   cfg,
		nextPort: cfg.PortRangeMin,
	}

	telemetry.RegisterSandboxCountGauge(backendName, func() map[string]int64 {
		instances, err := p.client.GetInstances(incusapi.InstanceTypeContainer)
		if err != nil {
			return nil
		}
		counts := map[string]int64{}
		for _, inst := range instances {
			switch inst.Status {
			case "Running":
				counts["running"]++
			case "Stopping", "Aborting", "Freezing":
				counts["stopping"]++
			default:
				counts["stopped"]++
			}
		}
		return counts
	})

	return p, nil
}

// Health reports whether the Incus daemon is reachable and responsive.
func (p *IncusProvider) Health(ctx context.Context) domain.ProviderHealth {
	start := time.Now()

	_, _, err := p.client.GetServer()
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return domain.ProviderHealth{
			Backend:   backendName,
			Healthy:   false,
			LatencyMS: latency,
			Error:     err.Error(),
		}
	}
	return domain.ProviderHealth{
		Backend:   backendName,
		Healthy:   true,
		LatencyMS: latency,
	}
}

// allocatePort returns the next available host port from the configured range.
// This is a simple sequential allocator; a production system would track
// freed ports.
func (p *IncusProvider) allocatePort() (int, error) {
	p.portMu.Lock()
	defer p.portMu.Unlock()

	if p.nextPort > p.config.PortRangeMax {
		return 0, domain.ErrCapacityExceeded
	}
	port := p.nextPort
	p.nextPort++
	return port, nil
}

// ForkSandbox is not supported by the Incus provider — containers do not
// have VM memory state to copy-on-write fork. Returns a wrapped
// domain.ErrNotSupported.
func (p *IncusProvider) ForkSandbox(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
	return nil, fmt.Errorf("incus: %w (containers do not have VM memory to CoW)", domain.ErrNotSupported)
}
