//go:build firecracker

package firecracker

import (
	"context"
	"errors"

	"github.com/navaris/navaris/internal/domain"
)

// ErrNotImplemented is returned by Phase 2 stub methods.
var ErrNotImplemented = errors.New("firecracker provider: operation not implemented (phase 2)")

func (p *Provider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	return domain.PublishedEndpoint{}, ErrNotImplemented
}

func (p *Provider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	return ErrNotImplemented
}
