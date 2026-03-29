//go:build firecracker

package firecracker

import (
	"context"
	"errors"

	"github.com/navaris/navaris/internal/domain"
)

// ErrNotImplemented is returned by Phase 2 stub methods.
var ErrNotImplemented = errors.New("firecracker provider: operation not implemented (phase 2)")

func (p *Provider) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	return domain.BackendRef{}, ErrNotImplemented
}

func (p *Provider) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	return ErrNotImplemented
}

func (p *Provider) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	return ErrNotImplemented
}

func (p *Provider) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	return domain.BackendRef{}, ErrNotImplemented
}

func (p *Provider) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	return ErrNotImplemented
}

func (p *Provider) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
	return domain.ImageInfo{}, ErrNotImplemented
}

func (p *Provider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	return domain.PublishedEndpoint{}, ErrNotImplemented
}

func (p *Provider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	return ErrNotImplemented
}
