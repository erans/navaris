//go:build firecracker

package firecracker

import (
	"context"
	"fmt"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
	"github.com/navaris/navaris/internal/telemetry"
)

// addDNATFn/removeDNATFn wrap network.AddDNAT/RemoveDNAT so tests can stub
// the iptables path. Default to the real functions.
var (
	addDNATFn    = network.AddDNAT
	removeDNATFn = network.RemoveDNAT
)

func clonePorts(ports map[int]int) map[int]int {
	if ports == nil {
		return nil
	}
	clone := make(map[int]int, len(ports))
	for host, target := range ports {
		clone[host] = target
	}
	return clone
}

func (p *Provider) syncPortsCache(vmID string, ports map[int]int) {
	p.vmMu.Lock()
	if cached, ok := p.vms[vmID]; ok {
		cached.Ports = clonePorts(ports)
	}
	p.vmMu.Unlock()
}

func (p *Provider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (_ domain.PublishedEndpoint, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "PublishPort")
	defer func() { endSpan(retErr) }()

	vmID := ref.Ref

	// Allocate host port.
	hostPort, err := p.portAlloc.Allocate()
	if err != nil {
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port %s: %w", vmID, err)
	}

	// F1: serialize the vminfo RMW against concurrent writers and StopSandbox.
	// Allocate stays outside the lock (portAlloc has its own mutex; no
	// deadlock risk since portAlloc.mu is never held while acquiring fileMu).
	fl, err := p.lockFor(vmID)
	if err != nil {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port %s: %w", vmID, err)
	}
	defer fl.mu.Unlock()

	// Read vminfo to get guest IP.
	infoPath := p.vmInfoPath(vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port read vminfo %s: %w", vmID, err)
	}

	// Validate sandbox is running with valid networking.
	if info.PID == 0 || !processAlive(info.PID) || info.TapDevice == "" {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port %s: sandbox is not running", vmID)
	}

	guestIP := p.subnets.GuestIP(info.SubnetIdx).String()

	// Add iptables rules.
	if err := addDNATFn(hostPort, guestIP, targetPort); err != nil {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port dnat %s: %w", vmID, err)
	}

	// Update vminfo with port mapping.
	if info.Ports == nil {
		info.Ports = make(map[int]int)
	}
	info.Ports[hostPort] = targetPort
	if err := info.Write(infoPath); err != nil {
		removeDNATFn(hostPort, guestIP, targetPort)
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port write vminfo %s: %w", vmID, err)
	}
	p.syncPortsCache(vmID, info.Ports)

	return domain.PublishedEndpoint{
		HostAddress:   "0.0.0.0",
		PublishedPort: hostPort,
	}, nil
}

func (p *Provider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "UnpublishPort")
	defer func() { endSpan(retErr) }()

	vmID := ref.Ref

	// F1: serialize the vminfo RMW against concurrent writers and StopSandbox.
	fl, err := p.lockFor(vmID)
	if err != nil {
		return fmt.Errorf("firecracker unpublish port %s: %w", vmID, err)
	}
	defer fl.mu.Unlock()

	infoPath := p.vmInfoPath(vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		return fmt.Errorf("firecracker unpublish port read vminfo %s: %w", vmID, err)
	}

	targetPort, ok := info.Ports[publishedPort]
	if !ok {
		return nil // Port not found, nothing to do.
	}

	guestIP := p.subnets.GuestIP(info.SubnetIdx).String()

	// Remove iptables rules.
	removeDNATFn(publishedPort, guestIP, targetPort)

	// Update vminfo.
	delete(info.Ports, publishedPort)
	if err := info.Write(infoPath); err != nil {
		return fmt.Errorf("firecracker unpublish port write vminfo %s: %w", vmID, err)
	}
	p.syncPortsCache(vmID, info.Ports)

	p.portAlloc.Release(publishedPort)
	return nil
}
