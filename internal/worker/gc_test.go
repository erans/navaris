package worker_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

func newTestStoreForGC(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGCSweepExpiredSandboxes(t *testing.T) {
	s := newTestStoreForGC(t)
	mock := provider.NewMock()

	// Create project + expired sandbox
	proj := &domain.Project{
		ProjectID: uuid.NewString(), Name: "proj",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.ProjectStore().Create(t.Context(), proj)

	past := time.Now().UTC().Add(-time.Hour)
	sbx := &domain.Sandbox{
		SandboxID: uuid.NewString(), ProjectID: proj.ProjectID, Name: "expired",
		State: domain.SandboxRunning, Backend: "mock", BackendRef: "ref",
		NetworkMode: domain.NetworkIsolated, ExpiresAt: &past,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.SandboxStore().Create(t.Context(), sbx)

	gc := worker.NewGC(s.SandboxStore(), s.SnapshotStore(), s.OperationStore(), mock, worker.GCConfig{})
	gc.Sweep(t.Context())

	got, _ := s.SandboxStore().Get(t.Context(), sbx.SandboxID)
	if got.State != domain.SandboxDestroyed {
		t.Errorf("expected destroyed, got %s", got.State)
	}
}

func TestGCSweepOrphanedSnapshots(t *testing.T) {
	s := newTestStoreForGC(t)
	mock := provider.NewMock()

	proj := &domain.Project{
		ProjectID: uuid.NewString(), Name: "proj",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.ProjectStore().Create(t.Context(), proj)

	sbx := &domain.Sandbox{
		SandboxID: uuid.NewString(), ProjectID: proj.ProjectID, Name: "sbx",
		State: domain.SandboxDestroyed, Backend: "mock",
		NetworkMode: domain.NetworkIsolated,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.SandboxStore().Create(t.Context(), sbx)

	snap := &domain.Snapshot{
		SnapshotID: uuid.NewString(), SandboxID: sbx.SandboxID,
		Backend: "mock", Label: "orphan", State: domain.SnapshotReady,
		ConsistencyMode: domain.ConsistencyStopped,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.SnapshotStore().Create(t.Context(), snap)

	var deleteCalled bool
	mock.DeleteSnapshotFn = func(_ context.Context, _ domain.BackendRef) error {
		deleteCalled = true
		return nil
	}

	gc := worker.NewGC(s.SandboxStore(), s.SnapshotStore(), s.OperationStore(), mock, worker.GCConfig{})
	gc.Sweep(t.Context())

	if !deleteCalled {
		t.Error("expected DeleteSnapshot to be called")
	}
}
