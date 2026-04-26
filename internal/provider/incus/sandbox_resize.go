//go:build incus

package incus

import (
	"context"

	"github.com/navaris/navaris/internal/domain"
)

// UpdateResources is implemented in task 10.
func (p *IncusProvider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	return domain.ErrNotSupported
}
