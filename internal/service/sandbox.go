package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type CreateSandboxOpts struct {
	CPULimit      *int
	MemoryLimitMB *int
	NetworkMode   domain.NetworkMode
	ExpiresAt     *time.Time
	Metadata      map[string]any
}

type SandboxService struct {
	sandboxes      domain.SandboxStore
	snapshots      domain.SnapshotStore
	ops            domain.OperationStore
	ports          domain.PortBindingStore
	sessions       domain.SessionStore
	provider       domain.Provider
	events         domain.EventBus
	workers        *worker.Dispatcher
	defaultBackend string
}

func NewSandboxService(
	sandboxes domain.SandboxStore,
	snapshots domain.SnapshotStore,
	ops domain.OperationStore,
	ports domain.PortBindingStore,
	sessions domain.SessionStore,
	provider domain.Provider,
	events domain.EventBus,
	workers *worker.Dispatcher,
	defaultBackend string,
) *SandboxService {
	svc := &SandboxService{
		sandboxes:      sandboxes,
		snapshots:      snapshots,
		ops:            ops,
		ports:          ports,
		sessions:       sessions,
		provider:       provider,
		events:         events,
		workers:        workers,
		defaultBackend: defaultBackend,
	}
	svc.registerHandlers()
	return svc
}

func (s *SandboxService) registerHandlers() {
	s.workers.Register("create_sandbox", s.handleCreate)
	s.workers.Register("start_sandbox", s.handleStart)
	s.workers.Register("stop_sandbox", s.handleStop)
	s.workers.Register("destroy_sandbox", s.handleDestroy)
}

func (s *SandboxService) Create(ctx context.Context, projectID, name, imageID string, opts CreateSandboxOpts) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.CreateSandbox")
	defer span.End()

	span.SetAttributes(
		attribute.String("provider.backend", s.defaultBackend),
		attribute.String("image.ref", imageID),
	)

	now := time.Now().UTC()
	networkMode := opts.NetworkMode
	if networkMode == "" {
		networkMode = domain.NetworkIsolated
	}

	sbx := &domain.Sandbox{
		SandboxID:     uuid.NewString(),
		ProjectID:     projectID,
		Name:          name,
		State:         domain.SandboxPending,
		Backend:       s.defaultBackend,
		SourceImageID: imageID,
		NetworkMode:   networkMode,
		CPULimit:      opts.CPULimit,
		MemoryLimitMB: opts.MemoryLimitMB,
		ExpiresAt:     opts.ExpiresAt,
		CreatedAt:     now,
		UpdatedAt:     now,
		Metadata:      opts.Metadata,
	}
	if err := s.sandboxes.Create(ctx, sbx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(attribute.String("sandbox.id", sbx.SandboxID))

	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   sbx.SandboxID,
		SandboxID:    sbx.SandboxID,
		Type:         "create_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
		Metadata:     map[string]any{"image_id": imageID},
	}
	if err := s.ops.Create(ctx, op); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.sandboxes.Delete(ctx, sbx.SandboxID)
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *SandboxService) CreateFromSnapshot(ctx context.Context, projectID, name, snapshotID string, opts CreateSandboxOpts) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.CreateSandboxFromSnapshot")
	defer span.End()

	span.SetAttributes(
		attribute.String("provider.backend", s.defaultBackend),
		attribute.String("snapshot.id", snapshotID),
	)

	now := time.Now().UTC()
	networkMode := opts.NetworkMode
	if networkMode == "" {
		networkMode = domain.NetworkIsolated
	}

	sbx := &domain.Sandbox{
		SandboxID:        uuid.NewString(),
		ProjectID:        projectID,
		Name:             name,
		State:            domain.SandboxPending,
		Backend:          s.defaultBackend,
		ParentSnapshotID: snapshotID,
		NetworkMode:      networkMode,
		CPULimit:         opts.CPULimit,
		MemoryLimitMB:    opts.MemoryLimitMB,
		ExpiresAt:        opts.ExpiresAt,
		CreatedAt:        now,
		UpdatedAt:        now,
		Metadata:         opts.Metadata,
	}
	if err := s.sandboxes.Create(ctx, sbx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(attribute.String("sandbox.id", sbx.SandboxID))

	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   sbx.SandboxID,
		SandboxID:    sbx.SandboxID,
		Type:         "create_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
		Metadata:     map[string]any{"snapshot_id": snapshotID},
	}
	if err := s.ops.Create(ctx, op); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.sandboxes.Delete(ctx, sbx.SandboxID)
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *SandboxService) Get(ctx context.Context, id string) (*domain.Sandbox, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.GetSandbox")
	defer span.End()

	span.SetAttributes(
		attribute.String("provider.backend", s.defaultBackend),
		attribute.String("sandbox.id", id),
	)
	sbx, err := s.sandboxes.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return sbx, nil
}

func (s *SandboxService) List(ctx context.Context, filter domain.SandboxFilter) ([]*domain.Sandbox, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ListSandboxes")
	defer span.End()

	span.SetAttributes(attribute.String("provider.backend", s.defaultBackend))
	list, err := s.sandboxes.List(ctx, filter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return list, nil
}

func (s *SandboxService) Start(ctx context.Context, id string) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.StartSandbox")
	defer span.End()

	span.SetAttributes(
		attribute.String("provider.backend", s.defaultBackend),
		attribute.String("sandbox.id", id),
	)

	sbx, err := s.sandboxes.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	prevState := sbx.State
	if !prevState.CanTransitionTo(domain.SandboxStarting) {
		err := fmt.Errorf("cannot start sandbox in state %s: %w", prevState, domain.ErrInvalidState)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Transition to starting before enqueue to prevent duplicate operations
	sbx.State = domain.SandboxStarting
	sbx.UpdatedAt = time.Now().UTC()
	if err := s.sandboxes.Update(ctx, sbx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	now := time.Now().UTC()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   sbx.SandboxID,
		SandboxID:    sbx.SandboxID,
		Type:         "start_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
	}
	if err := s.ops.Create(ctx, op); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// Rollback sandbox state
		sbx.State = prevState
		sbx.UpdatedAt = time.Now().UTC()
		s.sandboxes.Update(ctx, sbx)
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *SandboxService) Stop(ctx context.Context, id string, force bool) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.StopSandbox")
	defer span.End()

	span.SetAttributes(
		attribute.String("provider.backend", s.defaultBackend),
		attribute.String("sandbox.id", id),
	)

	sbx, err := s.sandboxes.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	prevState := sbx.State
	if !prevState.CanTransitionTo(domain.SandboxStopping) {
		err := fmt.Errorf("cannot stop sandbox in state %s: %w", prevState, domain.ErrInvalidState)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Transition to stopping before enqueue to prevent duplicate operations
	sbx.State = domain.SandboxStopping
	sbx.UpdatedAt = time.Now().UTC()
	if err := s.sandboxes.Update(ctx, sbx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	now := time.Now().UTC()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   sbx.SandboxID,
		SandboxID:    sbx.SandboxID,
		Type:         "stop_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
		Metadata:     map[string]any{"force": force},
	}
	if err := s.ops.Create(ctx, op); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// Rollback sandbox state
		sbx.State = prevState
		sbx.UpdatedAt = time.Now().UTC()
		s.sandboxes.Update(ctx, sbx)
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *SandboxService) Destroy(ctx context.Context, id string) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.DestroySandbox")
	defer span.End()

	span.SetAttributes(
		attribute.String("provider.backend", s.defaultBackend),
		attribute.String("sandbox.id", id),
	)

	sbx, err := s.sandboxes.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if sbx.State == domain.SandboxDestroyed {
		err := fmt.Errorf("sandbox already destroyed: %w", domain.ErrInvalidState)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	now := time.Now().UTC()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   sbx.SandboxID,
		SandboxID:    sbx.SandboxID,
		Type:         "destroy_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
	}
	if err := s.ops.Create(ctx, op); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

// Operation handlers

func (s *SandboxService) handleCreate(ctx context.Context, op *domain.Operation) error {
	sbx, err := s.sandboxes.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}

	// Transition to starting
	sbx.State = domain.SandboxStarting
	sbx.UpdatedAt = time.Now().UTC()
	s.sandboxes.Update(ctx, sbx)
	s.publishStateChange(ctx, sbx)

	createReq := domain.CreateSandboxRequest{
		Name:          sbx.Name,
		ImageRef:      sbx.SourceImageID,
		CPULimit:      sbx.CPULimit,
		MemoryLimitMB: sbx.MemoryLimitMB,
		NetworkMode:   sbx.NetworkMode,
	}

	var ref domain.BackendRef
	if sbx.ParentSnapshotID != "" {
		// Snapshot-based creation: look up snapshot backend ref
		snap, err := s.snapshots.Get(ctx, sbx.ParentSnapshotID)
		if err != nil {
			sbx.State = domain.SandboxFailed
			sbx.UpdatedAt = time.Now().UTC()
			s.sandboxes.Update(ctx, sbx)
			s.publishStateChange(ctx, sbx)
			return fmt.Errorf("lookup parent snapshot: %w", err)
		}
		snapshotRef := domain.BackendRef{Backend: snap.Backend, Ref: snap.BackendRef}
		ref, err = s.provider.CreateSandboxFromSnapshot(ctx, snapshotRef, createReq)
		if err != nil {
			sbx.State = domain.SandboxFailed
			sbx.UpdatedAt = time.Now().UTC()
			s.sandboxes.Update(ctx, sbx)
			s.publishStateChange(ctx, sbx)
			return err
		}
	} else {
		// Image-based creation
		ref, err = s.provider.CreateSandbox(ctx, createReq)
		if err != nil {
			sbx.State = domain.SandboxFailed
			sbx.UpdatedAt = time.Now().UTC()
			s.sandboxes.Update(ctx, sbx)
			s.publishStateChange(ctx, sbx)
			return err
		}
	}

	sbx.BackendRef = ref.Ref
	sbx.Backend = ref.Backend

	// Start the sandbox
	if err := s.provider.StartSandbox(ctx, ref); err != nil {
		sbx.State = domain.SandboxFailed
		sbx.UpdatedAt = time.Now().UTC()
		s.sandboxes.Update(ctx, sbx)
		s.publishStateChange(ctx, sbx)
		return err
	}

	sbx.State = domain.SandboxRunning
	sbx.UpdatedAt = time.Now().UTC()
	s.sandboxes.Update(ctx, sbx)
	s.publishStateChange(ctx, sbx)
	return nil
}

func (s *SandboxService) handleStart(ctx context.Context, op *domain.Operation) error {
	sbx, err := s.sandboxes.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}

	sbx.State = domain.SandboxStarting
	sbx.UpdatedAt = time.Now().UTC()
	s.sandboxes.Update(ctx, sbx)
	s.publishStateChange(ctx, sbx)

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
	if err := s.provider.StartSandbox(ctx, ref); err != nil {
		sbx.State = domain.SandboxFailed
		sbx.UpdatedAt = time.Now().UTC()
		s.sandboxes.Update(ctx, sbx)
		s.publishStateChange(ctx, sbx)
		return err
	}

	sbx.State = domain.SandboxRunning
	sbx.UpdatedAt = time.Now().UTC()
	s.sandboxes.Update(ctx, sbx)
	s.publishStateChange(ctx, sbx)
	return nil
}

func (s *SandboxService) handleStop(ctx context.Context, op *domain.Operation) error {
	sbx, err := s.sandboxes.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}

	sbx.State = domain.SandboxStopping
	sbx.UpdatedAt = time.Now().UTC()
	s.sandboxes.Update(ctx, sbx)
	s.publishStateChange(ctx, sbx)

	force := false
	if op.Metadata != nil {
		if v, ok := op.Metadata["force"].(bool); ok {
			force = v
		}
	}

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
	if err := s.provider.StopSandbox(ctx, ref, force); err != nil {
		sbx.State = domain.SandboxFailed
		sbx.UpdatedAt = time.Now().UTC()
		s.sandboxes.Update(ctx, sbx)
		s.publishStateChange(ctx, sbx)
		return err
	}

	sbx.State = domain.SandboxStopped
	sbx.UpdatedAt = time.Now().UTC()
	s.sandboxes.Update(ctx, sbx)
	s.publishStateChange(ctx, sbx)
	return nil
}

func (s *SandboxService) handleDestroy(ctx context.Context, op *domain.Operation) error {
	sbx, err := s.sandboxes.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}

	// Best-effort cleanup: sessions, port bindings
	sessions, _ := s.sessions.ListBySandbox(ctx, sbx.SandboxID)
	for _, sess := range sessions {
		s.sessions.Delete(ctx, sess.SessionID)
	}
	ports, _ := s.ports.ListBySandbox(ctx, sbx.SandboxID)
	for _, pb := range ports {
		s.provider.UnpublishPort(ctx, ref, pb.PublishedPort)
		s.ports.Delete(ctx, sbx.SandboxID, pb.TargetPort)
	}

	if err := s.provider.DestroySandbox(ctx, ref); err != nil {
		return err
	}

	sbx.State = domain.SandboxDestroyed
	sbx.UpdatedAt = time.Now().UTC()
	s.sandboxes.Update(ctx, sbx)
	s.publishStateChange(ctx, sbx)
	return nil
}

func (s *SandboxService) publishStateChange(ctx context.Context, sbx *domain.Sandbox) {
	s.events.Publish(ctx, domain.Event{
		Type:      domain.EventSandboxStateChanged,
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"sandbox_id": sbx.SandboxID,
			"project_id": sbx.ProjectID,
			"state":      string(sbx.State),
		},
	})
}
