package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
)

type reconcileEnv struct {
	store    *sqlite.Store
	mock     *provider.MockProvider
	reconciler *service.Reconciler
}

func newReconcileEnv(t *testing.T) *reconcileEnv {
	t.Helper()
	s, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	mock := provider.NewMock()
	r := service.NewReconciler(s.SandboxStore(), s.OperationStore(), mock, nil)

	return &reconcileEnv{store: s, mock: mock, reconciler: r}
}

func createProject(t *testing.T, env *reconcileEnv) string {
	t.Helper()
	ctx := context.Background()
	p := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      "test-proj-" + uuid.NewString()[:8],
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := env.store.ProjectStore().Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	return p.ProjectID
}

func insertSandbox(t *testing.T, env *reconcileEnv, projectID string, state domain.SandboxState, backendRef string) *domain.Sandbox {
	t.Helper()
	now := time.Now().UTC()
	sbx := &domain.Sandbox{
		SandboxID:   uuid.NewString(),
		ProjectID:   projectID,
		Name:        "sbx-" + uuid.NewString()[:8],
		State:       state,
		Backend:     "mock",
		BackendRef:  backendRef,
		NetworkMode: domain.NetworkIsolated,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.store.SandboxStore().Create(context.Background(), sbx); err != nil {
		t.Fatal(err)
	}
	return sbx
}

func insertOperation(t *testing.T, env *reconcileEnv, sandboxID string, state domain.OperationState, opType string) *domain.Operation {
	t.Helper()
	now := time.Now().UTC()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   sandboxID,
		SandboxID:    sandboxID,
		Type:         opType,
		State:        state,
		StartedAt:    now,
	}
	if err := env.store.OperationStore().Create(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	return op
}

func TestReconcileTransitionalSandboxes_PendingNoBackendRef(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Sandbox stuck in pending with no backend ref should be marked failed.
	sbx := insertSandbox(t, env, projID, domain.SandboxPending, "")

	result := env.reconciler.Run(context.Background())

	if result.TransitionalFixed != 1 {
		t.Errorf("expected 1 transitional fixed, got %d", result.TransitionalFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxFailed {
		t.Errorf("expected failed, got %s", updated.State)
	}
}

func TestReconcileTransitionalSandboxes_BackendReportsRunning(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Sandbox stuck in starting but backend says running.
	sbx := insertSandbox(t, env, projID, domain.SandboxStarting, "ref-123")

	env.mock.GetSandboxStateFn = func(_ context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
		if ref.Ref == "ref-123" {
			return domain.SandboxRunning, nil
		}
		return domain.SandboxRunning, nil
	}

	result := env.reconciler.Run(context.Background())

	if result.TransitionalFixed != 1 {
		t.Errorf("expected 1 transitional fixed, got %d", result.TransitionalFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxRunning {
		t.Errorf("expected running, got %s", updated.State)
	}
}

func TestReconcileTransitionalSandboxes_BackendReportsStopped(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Sandbox stuck in stopping but backend says stopped.
	sbx := insertSandbox(t, env, projID, domain.SandboxStopping, "ref-456")

	env.mock.GetSandboxStateFn = func(_ context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
		if ref.Ref == "ref-456" {
			return domain.SandboxStopped, nil
		}
		return domain.SandboxRunning, nil
	}

	result := env.reconciler.Run(context.Background())

	if result.TransitionalFixed != 1 {
		t.Errorf("expected 1 transitional fixed, got %d", result.TransitionalFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxStopped {
		t.Errorf("expected stopped, got %s", updated.State)
	}
}

func TestReconcileTransitionalSandboxes_BackendError(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Backend returns an error -- sandbox should be marked failed.
	sbx := insertSandbox(t, env, projID, domain.SandboxStarting, "ref-err")

	env.mock.GetSandboxStateFn = func(_ context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
		if ref.Ref == "ref-err" {
			return "", fmt.Errorf("connection refused")
		}
		return domain.SandboxRunning, nil
	}

	result := env.reconciler.Run(context.Background())

	if result.TransitionalFixed != 1 {
		t.Errorf("expected 1 transitional fixed, got %d", result.TransitionalFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxFailed {
		t.Errorf("expected failed, got %s", updated.State)
	}
}

func TestReconcileTransitionalSandboxes_BackendAlsoTransitional(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Backend also reports transitional -- should be marked failed since
	// there is no handler to drive it forward.
	sbx := insertSandbox(t, env, projID, domain.SandboxStarting, "ref-trans")

	env.mock.GetSandboxStateFn = func(_ context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
		if ref.Ref == "ref-trans" {
			return domain.SandboxStarting, nil
		}
		return domain.SandboxRunning, nil
	}

	result := env.reconciler.Run(context.Background())

	if result.TransitionalFixed != 1 {
		t.Errorf("expected 1 transitional fixed, got %d", result.TransitionalFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxFailed {
		t.Errorf("expected failed, got %s", updated.State)
	}
}

func TestReconcileStaleOperations(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)
	sbx := insertSandbox(t, env, projID, domain.SandboxRunning, "ref-ok")

	// Running operation should be marked failed.
	runningOp := insertOperation(t, env, sbx.SandboxID, domain.OpRunning, "create_sandbox")
	// Pending operation should also be marked failed.
	pendingOp := insertOperation(t, env, sbx.SandboxID, domain.OpPending, "start_sandbox")
	// Succeeded operation should be left alone.
	succeededOp := insertOperation(t, env, sbx.SandboxID, domain.OpSucceeded, "stop_sandbox")

	result := env.reconciler.Run(context.Background())

	if result.StaleOpsFailed != 2 {
		t.Errorf("expected 2 stale ops failed, got %d", result.StaleOpsFailed)
	}

	// Verify running op is now failed.
	updated, err := env.store.OperationStore().Get(context.Background(), runningOp.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.OpFailed {
		t.Errorf("expected running op to be failed, got %s", updated.State)
	}
	if updated.ErrorText == "" {
		t.Error("expected error text on failed operation")
	}
	if updated.FinishedAt == nil {
		t.Error("expected finished_at on failed operation")
	}

	// Verify pending op is now failed.
	updated, err = env.store.OperationStore().Get(context.Background(), pendingOp.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.OpFailed {
		t.Errorf("expected pending op to be failed, got %s", updated.State)
	}

	// Verify succeeded op is unchanged.
	updated, err = env.store.OperationStore().Get(context.Background(), succeededOp.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.OpSucceeded {
		t.Errorf("expected succeeded op to remain succeeded, got %s", updated.State)
	}
}

func TestReconcileRunningSandboxes_ActuallyStopped(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Sandbox recorded as running but backend says stopped.
	sbx := insertSandbox(t, env, projID, domain.SandboxRunning, "ref-drift")

	env.mock.GetSandboxStateFn = func(_ context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
		if ref.Ref == "ref-drift" {
			return domain.SandboxStopped, nil
		}
		return domain.SandboxRunning, nil
	}

	result := env.reconciler.Run(context.Background())

	if result.DriftFixed != 1 {
		t.Errorf("expected 1 drift fixed, got %d", result.DriftFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxStopped {
		t.Errorf("expected stopped, got %s", updated.State)
	}
}

func TestReconcileRunningSandboxes_BackendError(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Sandbox recorded as running but backend returns error.
	sbx := insertSandbox(t, env, projID, domain.SandboxRunning, "ref-gone")

	env.mock.GetSandboxStateFn = func(_ context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
		if ref.Ref == "ref-gone" {
			return "", fmt.Errorf("instance not found")
		}
		return domain.SandboxRunning, nil
	}

	result := env.reconciler.Run(context.Background())

	if result.DriftFixed != 1 {
		t.Errorf("expected 1 drift fixed, got %d", result.DriftFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxFailed {
		t.Errorf("expected failed, got %s", updated.State)
	}
}

func TestReconcileRunningSandboxes_StillRunning(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Sandbox running and backend confirms -- no change expected.
	sbx := insertSandbox(t, env, projID, domain.SandboxRunning, "ref-ok")

	env.mock.GetSandboxStateFn = func(_ context.Context, _ domain.BackendRef) (domain.SandboxState, error) {
		return domain.SandboxRunning, nil
	}

	result := env.reconciler.Run(context.Background())

	if result.DriftFixed != 0 {
		t.Errorf("expected 0 drift fixed, got %d", result.DriftFixed)
	}

	updated, err := env.store.SandboxStore().Get(context.Background(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.SandboxRunning {
		t.Errorf("expected running, got %s", updated.State)
	}
}

func TestReconcileNoIssues(t *testing.T) {
	env := newReconcileEnv(t)

	// Empty database -- nothing to reconcile.
	result := env.reconciler.Run(context.Background())

	if result.TransitionalFixed != 0 || result.StaleOpsFailed != 0 || result.DriftFixed != 0 {
		t.Errorf("expected all zeros, got transitional=%d stale=%d drift=%d",
			result.TransitionalFixed, result.StaleOpsFailed, result.DriftFixed)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %v", result.Errors)
	}
}

func TestReconcileMixedStates(t *testing.T) {
	env := newReconcileEnv(t)
	projID := createProject(t, env)

	// Set up a mix of sandboxes and operations.

	// Transitional sandbox that the backend says is running.
	insertSandbox(t, env, projID, domain.SandboxStarting, "ref-a")
	// Sandbox properly running.
	runningSbx := insertSandbox(t, env, projID, domain.SandboxRunning, "ref-b")
	// Sandbox running but backend says stopped.
	insertSandbox(t, env, projID, domain.SandboxRunning, "ref-c")
	// Stopped sandbox -- should not be touched.
	insertSandbox(t, env, projID, domain.SandboxStopped, "ref-d")

	// Stale running operation.
	insertOperation(t, env, runningSbx.SandboxID, domain.OpRunning, "start_sandbox")

	env.mock.GetSandboxStateFn = func(_ context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
		switch ref.Ref {
		case "ref-a":
			return domain.SandboxRunning, nil
		case "ref-b":
			return domain.SandboxRunning, nil
		case "ref-c":
			return domain.SandboxStopped, nil
		default:
			return domain.SandboxRunning, nil
		}
	}

	result := env.reconciler.Run(context.Background())

	if result.TransitionalFixed != 1 {
		t.Errorf("expected 1 transitional fixed, got %d", result.TransitionalFixed)
	}
	if result.StaleOpsFailed != 1 {
		t.Errorf("expected 1 stale ops failed, got %d", result.StaleOpsFailed)
	}
	if result.DriftFixed != 1 {
		t.Errorf("expected 1 drift fixed, got %d", result.DriftFixed)
	}
}
