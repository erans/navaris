//go:build firecracker

package firecracker

import (
	"context"

	"github.com/navaris/navaris/internal/domain"
)

// UpdateResources is implemented in task 9.
func (p *Provider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	return domain.ErrNotSupported
}
