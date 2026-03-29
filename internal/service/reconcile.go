package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

// Reconciler corrects state drift between the database and the backend
// provider on daemon startup. It handles three classes of inconsistency:
//
//  1. Sandboxes stuck in transitional states (pending, starting, stopping)
//     that no longer have an active handler driving them forward.
//  2. Operations marked as Running that have no handler processing them
//     (stale), typically because the daemon restarted mid-operation.
//  3. Sandboxes recorded as Running that the backend reports as stopped or
//     unknown, indicating the backend changed state out-of-band.
type Reconciler struct {
	sandboxes domain.SandboxStore
	ops       domain.OperationStore
	provider  domain.Provider
	logger    *slog.Logger
}

// ReconcileResult reports what the reconciler found and fixed.
type ReconcileResult struct {
	TransitionalFixed int
	StaleOpsFailed    int
	DriftFixed        int
	Errors            []error
}

// NewReconciler creates a new Reconciler. The logger may be nil, in which
// case the default slog logger is used.
func NewReconciler(
	sandboxes domain.SandboxStore,
	ops domain.OperationStore,
	provider domain.Provider,
	logger *slog.Logger,
) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		sandboxes: sandboxes,
		ops:       ops,
		provider:  provider,
		logger:    logger,
	}
}

// Run performs a single reconciliation pass. It is intended to be called
// once at daemon startup before the worker dispatcher begins processing
// new operations.
func (r *Reconciler) Run(ctx context.Context) ReconcileResult {
	var result ReconcileResult

	r.reconcileTransitionalSandboxes(ctx, &result)
	r.reconcileStaleOperations(ctx, &result)
	r.reconcileRunningSandboxes(ctx, &result)

	r.logger.Info("reconciliation complete",
		"transitional_fixed", result.TransitionalFixed,
		"stale_ops_failed", result.StaleOpsFailed,
		"drift_fixed", result.DriftFixed,
		"errors", len(result.Errors),
	)
	return result
}

// reconcileTransitionalSandboxes finds sandboxes in non-terminal
// transitional states (pending, starting, stopping) and resolves them by
// querying the backend for actual state. If the backend cannot determine
// the state, the sandbox is marked as failed.
func (r *Reconciler) reconcileTransitionalSandboxes(ctx context.Context, result *ReconcileResult) {
	transitional := []domain.SandboxState{
		domain.SandboxPending,
		domain.SandboxStarting,
		domain.SandboxStopping,
	}

	for _, state := range transitional {
		s := state
		sandboxes, err := r.sandboxes.List(ctx, domain.SandboxFilter{State: &s})
		if err != nil {
			r.logger.Error("reconcile: list sandboxes", "state", s, "error", err)
			result.Errors = append(result.Errors, err)
			continue
		}

		for _, sbx := range sandboxes {
			resolved := r.resolveTransitionalSandbox(ctx, sbx)
			if resolved {
				result.TransitionalFixed++
			}
		}
	}
}

// resolveTransitionalSandbox queries the backend for the actual state of
// a sandbox stuck in a transitional state. Returns true if the sandbox
// was updated.
func (r *Reconciler) resolveTransitionalSandbox(ctx context.Context, sbx *domain.Sandbox) bool {
	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}

	// If there is no backend ref (e.g. never got created), mark as failed.
	if sbx.BackendRef == "" {
		r.logger.Warn("reconcile: sandbox has no backend ref, marking failed",
			"sandbox_id", sbx.SandboxID, "state", sbx.State)
		sbx.State = domain.SandboxFailed
		sbx.UpdatedAt = time.Now().UTC()
		if err := r.sandboxes.Update(ctx, sbx); err != nil {
			r.logger.Error("reconcile: update sandbox", "sandbox_id", sbx.SandboxID, "error", err)
		}
		return true
	}

	actualState, err := r.provider.GetSandboxState(ctx, ref)
	if err != nil {
		r.logger.Warn("reconcile: cannot get sandbox state from backend, marking failed",
			"sandbox_id", sbx.SandboxID, "state", sbx.State, "error", err)
		sbx.State = domain.SandboxFailed
		sbx.UpdatedAt = time.Now().UTC()
		if err := r.sandboxes.Update(ctx, sbx); err != nil {
			r.logger.Error("reconcile: update sandbox", "sandbox_id", sbx.SandboxID, "error", err)
		}
		return true
	}

	// Only update if the actual state differs from the recorded state and
	// the actual state is non-transitional (terminal or stable).
	if actualState != sbx.State && !isTransitional(actualState) {
		r.logger.Info("reconcile: fixing transitional sandbox",
			"sandbox_id", sbx.SandboxID,
			"recorded", sbx.State,
			"actual", actualState)
		sbx.State = actualState
		sbx.UpdatedAt = time.Now().UTC()
		if err := r.sandboxes.Update(ctx, sbx); err != nil {
			r.logger.Error("reconcile: update sandbox", "sandbox_id", sbx.SandboxID, "error", err)
		}
		return true
	}

	// Backend also reports transitional -- mark as failed since no handler
	// is driving it.
	if isTransitional(actualState) {
		r.logger.Warn("reconcile: backend also reports transitional state, marking failed",
			"sandbox_id", sbx.SandboxID, "actual", actualState)
		sbx.State = domain.SandboxFailed
		sbx.UpdatedAt = time.Now().UTC()
		if err := r.sandboxes.Update(ctx, sbx); err != nil {
			r.logger.Error("reconcile: update sandbox", "sandbox_id", sbx.SandboxID, "error", err)
		}
		return true
	}

	return false
}

// reconcileStaleOperations finds operations in Running state and marks
// them as failed, since after a restart no handler is processing them.
func (r *Reconciler) reconcileStaleOperations(ctx context.Context, result *ReconcileResult) {
	ops, err := r.ops.ListByState(ctx, domain.OpRunning)
	if err != nil {
		r.logger.Error("reconcile: list running operations", "error", err)
		result.Errors = append(result.Errors, err)
		return
	}

	now := time.Now().UTC()
	for _, op := range ops {
		r.logger.Info("reconcile: marking stale operation as failed",
			"operation_id", op.OperationID,
			"type", op.Type,
			"resource_id", op.ResourceID)
		op.State = domain.OpFailed
		op.FinishedAt = &now
		op.ErrorText = "operation was in-flight during daemon restart"
		if err := r.ops.Update(ctx, op); err != nil {
			r.logger.Error("reconcile: update operation", "operation_id", op.OperationID, "error", err)
			result.Errors = append(result.Errors, err)
			continue
		}
		result.StaleOpsFailed++
	}

	// Also handle pending operations that will never be picked up.
	pendingOps, err := r.ops.ListByState(ctx, domain.OpPending)
	if err != nil {
		r.logger.Error("reconcile: list pending operations", "error", err)
		result.Errors = append(result.Errors, err)
		return
	}

	for _, op := range pendingOps {
		r.logger.Info("reconcile: marking orphaned pending operation as failed",
			"operation_id", op.OperationID,
			"type", op.Type,
			"resource_id", op.ResourceID)
		op.State = domain.OpFailed
		op.FinishedAt = &now
		op.ErrorText = "operation was pending during daemon restart"
		if err := r.ops.Update(ctx, op); err != nil {
			r.logger.Error("reconcile: update operation", "operation_id", op.OperationID, "error", err)
			result.Errors = append(result.Errors, err)
			continue
		}
		result.StaleOpsFailed++
	}
}

// reconcileRunningSandboxes verifies that sandboxes in Running state are
// actually running on the backend. If the backend reports a different
// state, the database is updated to match.
func (r *Reconciler) reconcileRunningSandboxes(ctx context.Context, result *ReconcileResult) {
	running := domain.SandboxRunning
	sandboxes, err := r.sandboxes.List(ctx, domain.SandboxFilter{State: &running})
	if err != nil {
		r.logger.Error("reconcile: list running sandboxes", "error", err)
		result.Errors = append(result.Errors, err)
		return
	}

	for _, sbx := range sandboxes {
		ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
		actualState, err := r.provider.GetSandboxState(ctx, ref)
		if err != nil {
			r.logger.Warn("reconcile: cannot verify running sandbox, marking failed",
				"sandbox_id", sbx.SandboxID, "error", err)
			sbx.State = domain.SandboxFailed
			sbx.UpdatedAt = time.Now().UTC()
			if err := r.sandboxes.Update(ctx, sbx); err != nil {
				r.logger.Error("reconcile: update sandbox", "sandbox_id", sbx.SandboxID, "error", err)
			}
			result.DriftFixed++
			continue
		}

		if actualState != domain.SandboxRunning {
			r.logger.Info("reconcile: running sandbox drifted",
				"sandbox_id", sbx.SandboxID,
				"actual", actualState)
			sbx.State = actualState
			sbx.UpdatedAt = time.Now().UTC()
			if err := r.sandboxes.Update(ctx, sbx); err != nil {
				r.logger.Error("reconcile: update sandbox", "sandbox_id", sbx.SandboxID, "error", err)
			}
			result.DriftFixed++
		}
	}
}

func isTransitional(s domain.SandboxState) bool {
	return s == domain.SandboxPending || s == domain.SandboxStarting || s == domain.SandboxStopping
}
