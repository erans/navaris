//go:build incus

package incus

import (
	"context"
	"fmt"
	"strings"

	incusapi "github.com/lxc/incus/v6/shared/api"
	"github.com/navaris/navaris/internal/domain"
)

// CreateSnapshot creates a snapshot of the given container. When mode is
// ConsistencyLive, a stateful (memory) snapshot is taken so the container can
// be restored to the exact running state.
func (p *IncusProvider) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	snapReq := incusapi.InstanceSnapshotsPost{
		Name:     label,
		Stateful: mode == domain.ConsistencyLive,
	}

	op, err := p.client.CreateInstanceSnapshot(ref.Ref, snapReq)
	if err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus create snapshot %s/%s: %w", ref.Ref, label, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return domain.BackendRef{}, fmt.Errorf("incus create snapshot wait: %w", err)
	}

	// The snapshot ref is "container/snapshot" so callers can address it later.
	snapshotRef := ref.Ref + "/" + label
	return domain.BackendRef{Backend: backendName, Ref: snapshotRef}, nil
}

// RestoreSnapshot restores a container to a previous snapshot state. The
// snapshotRef.Ref is expected to be "container/snapshot".
func (p *IncusProvider) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	parts := strings.SplitN(snapshotRef.Ref, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid snapshot ref %q: expected container/snapshot", snapshotRef.Ref)
	}
	snapshotName := parts[1]

	// Restore by updating the instance with the restore field.
	inst, etag, err := p.client.GetInstance(sandboxRef.Ref)
	if err != nil {
		return fmt.Errorf("incus get instance for restore: %w", err)
	}
	inst.Restore = snapshotName

	op, err := p.client.UpdateInstance(sandboxRef.Ref, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("incus restore snapshot %s: %w", snapshotRef.Ref, err)
	}
	return op.WaitContext(ctx)
}

// DeleteSnapshot removes a snapshot from its parent container. The
// snapshotRef.Ref is expected to be "container/snapshot".
func (p *IncusProvider) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	parts := strings.SplitN(snapshotRef.Ref, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid snapshot ref %q: expected container/snapshot", snapshotRef.Ref)
	}

	op, err := p.client.DeleteInstanceSnapshot(parts[0], parts[1])
	if err != nil {
		return fmt.Errorf("incus delete snapshot %s: %w", snapshotRef.Ref, err)
	}
	return op.WaitContext(ctx)
}
