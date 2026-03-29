package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/worker"
)

type SnapshotService struct {
	snapshots domain.SnapshotStore
	sandboxes domain.SandboxStore
	ops       domain.OperationStore
	provider  domain.Provider
	events    domain.EventBus
	workers   *worker.Dispatcher
}

func NewSnapshotService(
	snapshots domain.SnapshotStore,
	sandboxes domain.SandboxStore,
	ops domain.OperationStore,
	provider domain.Provider,
	events domain.EventBus,
	workers *worker.Dispatcher,
) *SnapshotService {
	svc := &SnapshotService{
		snapshots: snapshots, sandboxes: sandboxes, ops: ops,
		provider: provider, events: events, workers: workers,
	}
	svc.workers.Register("create_snapshot", svc.handleCreate)
	svc.workers.Register("restore_snapshot", svc.handleRestore)
	svc.workers.Register("delete_snapshot", svc.handleDelete)
	return svc
}

func (s *SnapshotService) Create(ctx context.Context, sandboxID, label string, mode domain.ConsistencyMode) (*domain.Operation, error) {
	sbx, err := s.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if sbx.State == domain.SandboxDestroyed {
		return nil, fmt.Errorf("cannot snapshot destroyed sandbox: %w", domain.ErrInvalidState)
	}
	if mode == "" {
		mode = domain.ConsistencyStopped
	}
	if mode == domain.ConsistencyStopped && sbx.State != domain.SandboxStopped {
		return nil, fmt.Errorf("sandbox must be stopped for stopped-consistency snapshot (state: %s): %w", sbx.State, domain.ErrInvalidState)
	}

	now := time.Now().UTC()
	snap := &domain.Snapshot{
		SnapshotID:      uuid.NewString(),
		SandboxID:       sandboxID,
		Backend:         sbx.Backend,
		Label:           label,
		State:           domain.SnapshotPending,
		ConsistencyMode: mode,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.snapshots.Create(ctx, snap); err != nil {
		return nil, err
	}

	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "snapshot",
		ResourceID:   snap.SnapshotID,
		SandboxID:    sandboxID,
		SnapshotID:   snap.SnapshotID,
		Type:         "create_snapshot",
		State:        domain.OpPending,
		StartedAt:    now,
	}
	if err := s.ops.Create(ctx, op); err != nil {
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *SnapshotService) Get(ctx context.Context, id string) (*domain.Snapshot, error) {
	return s.snapshots.Get(ctx, id)
}

func (s *SnapshotService) ListBySandbox(ctx context.Context, sandboxID string) ([]*domain.Snapshot, error) {
	return s.snapshots.ListBySandbox(ctx, sandboxID)
}

func (s *SnapshotService) Restore(ctx context.Context, sandboxID, snapshotID string) (*domain.Operation, error) {
	now := time.Now().UTC()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "snapshot",
		ResourceID:   snapshotID,
		SandboxID:    sandboxID,
		SnapshotID:   snapshotID,
		Type:         "restore_snapshot",
		State:        domain.OpPending,
		StartedAt:    now,
	}
	if err := s.ops.Create(ctx, op); err != nil {
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *SnapshotService) Delete(ctx context.Context, id string) (*domain.Operation, error) {
	snap, err := s.snapshots.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "snapshot",
		ResourceID:   snap.SnapshotID,
		SandboxID:    snap.SandboxID,
		SnapshotID:   snap.SnapshotID,
		Type:         "delete_snapshot",
		State:        domain.OpPending,
		StartedAt:    now,
	}
	if err := s.ops.Create(ctx, op); err != nil {
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *SnapshotService) handleCreate(ctx context.Context, op *domain.Operation) error {
	snap, err := s.snapshots.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}
	sbx, err := s.sandboxes.Get(ctx, snap.SandboxID)
	if err != nil {
		return err
	}

	snap.State = domain.SnapshotCreating
	snap.UpdatedAt = time.Now().UTC()
	s.snapshots.Update(ctx, snap)

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
	snapRef, err := s.provider.CreateSnapshot(ctx, ref, snap.Label, snap.ConsistencyMode)
	if err != nil {
		snap.State = domain.SnapshotFailed
		snap.UpdatedAt = time.Now().UTC()
		s.snapshots.Update(ctx, snap)
		return err
	}

	snap.BackendRef = snapRef.Ref
	snap.State = domain.SnapshotReady
	snap.UpdatedAt = time.Now().UTC()
	s.snapshots.Update(ctx, snap)
	return nil
}

func (s *SnapshotService) handleRestore(ctx context.Context, op *domain.Operation) error {
	snap, err := s.snapshots.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}
	sbx, err := s.sandboxes.Get(ctx, snap.SandboxID)
	if err != nil {
		return err
	}
	sandboxRef := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
	snapshotRef := domain.BackendRef{Backend: snap.Backend, Ref: snap.BackendRef}
	return s.provider.RestoreSnapshot(ctx, sandboxRef, snapshotRef)
}

func (s *SnapshotService) handleDelete(ctx context.Context, op *domain.Operation) error {
	snap, err := s.snapshots.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}
	snapshotRef := domain.BackendRef{Backend: snap.Backend, Ref: snap.BackendRef}
	if err := s.provider.DeleteSnapshot(ctx, snapshotRef); err != nil {
		return err
	}
	snap.State = domain.SnapshotDeleted
	snap.UpdatedAt = time.Now().UTC()
	return s.snapshots.Update(ctx, snap)
}
