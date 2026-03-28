//go:build incus

package incus

import (
	"context"
	"fmt"
	"sync"
	"time"

	incusclient "github.com/lxc/incus/v6/client"
	"github.com/navaris/navaris/internal/domain"
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

	return &IncusProvider{
		client:   client,
		config:   cfg,
		nextPort: cfg.PortRangeMin,
	}, nil
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
