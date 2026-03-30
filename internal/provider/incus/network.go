//go:build incus

package incus

import (
	"context"
	"fmt"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/telemetry"
)

// PublishPort adds an Incus proxy device to the container that forwards a host
// port to the specified container port.
func (p *IncusProvider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (_ domain.PublishedEndpoint, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "PublishPort")
	defer func() { endSpan(retErr) }()

	hostPort, err := p.allocatePort()
	if err != nil {
		return domain.PublishedEndpoint{}, fmt.Errorf("allocate host port: %w", err)
	}

	deviceName := fmt.Sprintf("proxy-%d", targetPort)
	device := map[string]string{
		"type":    "proxy",
		"listen":  fmt.Sprintf("tcp:0.0.0.0:%d", hostPort),
		"connect": fmt.Sprintf("tcp:127.0.0.1:%d", targetPort),
	}

	inst, etag, err := p.client.GetInstance(ref.Ref)
	if err != nil {
		return domain.PublishedEndpoint{}, fmt.Errorf("incus get instance %s: %w", ref.Ref, err)
	}

	if inst.Devices == nil {
		inst.Devices = map[string]map[string]string{}
	}
	inst.Devices[deviceName] = device

	op, err := p.client.UpdateInstance(ref.Ref, inst.Writable(), etag)
	if err != nil {
		return domain.PublishedEndpoint{}, fmt.Errorf("incus add proxy device %s: %w", ref.Ref, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return domain.PublishedEndpoint{}, fmt.Errorf("incus add proxy device wait: %w", err)
	}

	return domain.PublishedEndpoint{
		HostAddress:   "0.0.0.0",
		PublishedPort: hostPort,
	}, nil
}

// UnpublishPort removes the proxy device for the given published port from
// the container.
func (p *IncusProvider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "UnpublishPort")
	defer func() { endSpan(retErr) }()

	inst, etag, err := p.client.GetInstance(ref.Ref)
	if err != nil {
		return fmt.Errorf("incus get instance %s: %w", ref.Ref, err)
	}

	// Find the proxy device that listens on this published port.
	var found string
	for name, dev := range inst.Devices {
		if dev["type"] == "proxy" && dev["listen"] == fmt.Sprintf("tcp:0.0.0.0:%d", publishedPort) {
			found = name
			break
		}
	}
	if found == "" {
		return fmt.Errorf("no proxy device for port %d on %s", publishedPort, ref.Ref)
	}

	delete(inst.Devices, found)

	op, err := p.client.UpdateInstance(ref.Ref, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("incus remove proxy device %s: %w", ref.Ref, err)
	}
	return op.WaitContext(ctx)
}
