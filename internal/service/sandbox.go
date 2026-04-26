package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type CreateSandboxOpts struct {
	Backend       string
	CPULimit      *int
	MemoryLimitMB *int
	NetworkMode   domain.NetworkMode
	ExpiresAt     *time.Time
	Metadata      map[string]any
}

// resolveBackend picks the backend for a new sandbox.
// Priority: explicit > auto-detect from image ref > default.
func (s *SandboxService) resolveBackend(explicit, imageRef string) string {
	if explicit != "" {
		return explicit
	}
	if strings.Contains(imageRef, "/") {
		return "incus"
	}
	if imageRef != "" {
		return "firecracker"
	}
	return s.defaultBackend
}

type SandboxService struct {
	sandboxes      domain.SandboxStore
	snapshots      domain.SnapshotStore
	ops            domain.OperationStore
	ports          domain.PortBindingStore
	sessions       domain.SessionStore
	sessionSvc     *SessionService
	provider       domain.Provider
	events         domain.EventBus
	workers        *worker.Dispatcher
	defaultBackend string
}

// SetSessionService injects the SessionService after construction.
// This avoids circular init ordering: SandboxService is created before
// SessionService because both depend on stores provided by the same
// initialisation sequence.
func (s *SandboxService) SetSessionService(svc *SessionService) {
	s.sessionSvc = svc
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
	s.workers.Register("fork_sandbox", s.handleFork)
}

func (s *SandboxService) Create(ctx context.Context, projectID, name, imageID string, opts CreateSandboxOpts) (*domain.Operation, error) {
	backend := s.resolveBackend(opts.Backend, imageID)
	if err := validateLimits(opts, backend); err != nil {
		return nil, err
	}
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
		Backend:       backend,
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
	backend := s.resolveBackend(opts.Backend, "")
	if err := validateLimits(opts, backend); err != nil {
		return nil, err
	}
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
		Backend:          backend,
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

// Fork creates count children from a running parent sandbox. Each child
// is created in pending state with metadata["fork_parent_id"] set to the
// parent's sandbox ID; a single fork_sandbox operation is enqueued whose
// worker calls provider.ForkSandbox and binds the children to their
// returned BackendRefs.
//
// Validation: count >= 1; parent must exist. Provider-side caps (currently
// 64 children per fork on Firecracker) surface as worker-time errors,
// not pre-flight rejection.
func (s *SandboxService) Fork(ctx context.Context, parentID string, count int) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ForkSandbox")
	defer span.End()
	span.SetAttributes(
		attribute.String("sandbox.id", parentID),
		attribute.Int("fork.count", count),
	)

	if count < 1 {
		return nil, fmt.Errorf("fork: count must be >= 1: %w", domain.ErrInvalidState)
	}

	parent, err := s.sandboxes.Get(ctx, parentID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if parent.State != domain.SandboxRunning {
		err := fmt.Errorf("fork: parent sandbox %q is %s, want running: %w",
			parent.SandboxID, parent.State, domain.ErrInvalidState)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	now := time.Now().UTC()
	childIDs := make([]string, 0, count)
	for i := 0; i < count; i++ {
		child := &domain.Sandbox{
			SandboxID:     uuid.NewString(),
			ProjectID:     parent.ProjectID,
			Name:          fmt.Sprintf("%s-fork-%d", parent.Name, i),
			State:         domain.SandboxPending,
			Backend:       parent.Backend,
			NetworkMode:   parent.NetworkMode,
			CPULimit:      parent.CPULimit,
			MemoryLimitMB: parent.MemoryLimitMB,
			CreatedAt:     now,
			UpdatedAt:     now,
			Metadata: map[string]any{
				"fork_parent_id": parent.SandboxID,
			},
		}
		if err := s.sandboxes.Create(ctx, child); err != nil {
			// Roll back already-created children before returning.
			for _, id := range childIDs {
				_ = s.sandboxes.Delete(ctx, id)
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("fork: create child %d: %w", i, err)
		}
		childIDs = append(childIDs, child.SandboxID)
	}

	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   parent.SandboxID,
		SandboxID:    parent.SandboxID,
		Type:         "fork_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
		Metadata: map[string]any{
			"parent_id": parent.SandboxID,
			"children":  childIDs,
			"count":     count,
		},
	}
	if err := s.ops.Create(ctx, op); err != nil {
		for _, id := range childIDs {
			_ = s.sandboxes.Delete(ctx, id)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
		Backend:       sbx.Backend,
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

// handleFork dispatches the provider-level fork: read the parent sandbox,
// call provider.ForkSandbox (which the firecracker provider implements;
// incus returns ErrNotSupported), then bind each child's BackendRef and
// transition state. On provider failure, all enqueued children go to failed.
func (s *SandboxService) handleFork(ctx context.Context, op *domain.Operation) error {
	parentID, ok := op.Metadata["parent_id"].(string)
	if !ok {
		return fmt.Errorf("fork op missing parent_id")
	}

	// children may be []string (in-memory, direct from Fork()) or []any
	// (JSON round-trip via the operation store — json.Unmarshal decodes
	// arrays into []any when the target is map[string]any).
	var childIDs []string
	switch v := op.Metadata["children"].(type) {
	case []string:
		childIDs = v
	case []any:
		childIDs = make([]string, 0, len(v))
		for _, raw := range v {
			id, ok := raw.(string)
			if !ok {
				return fmt.Errorf("fork op children entry is not a string")
			}
			childIDs = append(childIDs, id)
		}
	default:
		return fmt.Errorf("fork op missing children")
	}

	parent, err := s.sandboxes.Get(ctx, parentID)
	if err != nil {
		s.markChildrenFailed(ctx, childIDs)
		return err
	}
	parentRef := domain.BackendRef{Backend: parent.Backend, Ref: parent.BackendRef}

	refs, err := s.provider.ForkSandbox(ctx, parentRef, len(childIDs))
	if err != nil {
		s.markChildrenFailed(ctx, childIDs)
		return err
	}

	// Provider may return fewer refs than requested if some children failed
	// after others succeeded. Bind the successful ones; mark the rest failed.
	for i, id := range childIDs {
		sbx, getErr := s.sandboxes.Get(ctx, id)
		if getErr != nil {
			continue
		}
		now := time.Now().UTC()
		if i < len(refs) {
			sbx.BackendRef = refs[i].Ref
			sbx.Backend = refs[i].Backend
			sbx.State = domain.SandboxStarting
		} else {
			sbx.State = domain.SandboxFailed
		}
		sbx.UpdatedAt = now
		_ = s.sandboxes.Update(ctx, sbx)
		s.publishStateChange(ctx, sbx)
	}
	return nil
}

// markChildrenFailed best-effort transitions every listed child to failed.
// Used when fork dispatch errors before any per-child binding happens.
func (s *SandboxService) markChildrenFailed(ctx context.Context, childIDs []string) {
	for _, id := range childIDs {
		sbx, err := s.sandboxes.Get(ctx, id)
		if err != nil {
			continue
		}
		sbx.State = domain.SandboxFailed
		sbx.UpdatedAt = time.Now().UTC()
		_ = s.sandboxes.Update(ctx, sbx)
		s.publishStateChange(ctx, sbx)
	}
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

	// Mark all active/detached sessions as exited and clear tmux cache.
	if s.sessionSvc != nil {
		if err := s.sessionSvc.ExitAllForSandbox(ctx, sbx.SandboxID); err != nil {
			_, span := otel.Tracer("navaris.service").Start(ctx, "service.handleStop.exitSessions")
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to exit sessions on sandbox stop")
			span.End()
		}
		s.sessionSvc.ClearTmuxCache(sbx.SandboxID)
	}

	return nil
}

func (s *SandboxService) handleDestroy(ctx context.Context, op *domain.Operation) error {
	sbx, err := s.sandboxes.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}

	// Best-effort cleanup: sessions, port bindings
	if s.sessionSvc != nil {
		s.sessionSvc.ExitAllForSandbox(ctx, sbx.SandboxID)
		s.sessionSvc.ClearTmuxCache(sbx.SandboxID)
	}
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
