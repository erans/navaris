//go:build firecracker

package firecracker

import (
	"context"
	"fmt"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
)

func (p *Provider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	vmID := ref.Ref

	// Allocate host port.
	hostPort, err := p.portAlloc.Allocate()
	if err != nil {
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port %s: %w", vmID, err)
	}

	// Read vminfo to get guest IP.
	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port read vminfo %s: %w", vmID, err)
	}
	guestIP := p.subnets.GuestIP(info.SubnetIdx).String()

	// Add iptables rules.
	if err := network.AddDNAT(hostPort, guestIP, targetPort); err != nil {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port dnat %s: %w", vmID, err)
	}

	// Update vminfo with port mapping.
	if info.Ports == nil {
		info.Ports = make(map[int]int)
	}
	info.Ports[hostPort] = targetPort
	if err := info.Write(infoPath); err != nil {
		network.RemoveDNAT(hostPort, guestIP, targetPort)
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port write vminfo %s: %w", vmID, err)
	}

	return domain.PublishedEndpoint{
		HostAddress:   "0.0.0.0",
		PublishedPort: hostPort,
	}, nil
}

func (p *Provider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	vmID := ref.Ref

	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
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
	network.RemoveDNAT(publishedPort, guestIP, targetPort)

	// Update vminfo.
	delete(info.Ports, publishedPort)
	if err := info.Write(infoPath); err != nil {
		return fmt.Errorf("firecracker unpublish port write vminfo %s: %w", vmID, err)
	}

	p.portAlloc.Release(publishedPort)
	return nil
}
