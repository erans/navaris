package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

func TestSnapshotCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	snap := &domain.Snapshot{
		SnapshotID:      uuid.NewString(),
		SandboxID:       sbx.SandboxID,
		Backend:         "incus",
		Label:           "snap1",
		State:           domain.SnapshotReady,
		ConsistencyMode: domain.ConsistencyStopped,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	if err := s.SnapshotStore().Create(t.Context(), snap); err != nil {
		t.Fatal(err)
	}
	got, err := s.SnapshotStore().Get(t.Context(), snap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "snap1" {
		t.Error("wrong label")
	}
}

func TestSnapshotListBySandbox(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	for i := 0; i < 3; i++ {
		s.SnapshotStore().Create(t.Context(), &domain.Snapshot{
			SnapshotID: uuid.NewString(), SandboxID: sbx.SandboxID,
			Backend: "incus", Label: "snap", State: domain.SnapshotReady,
			ConsistencyMode: domain.ConsistencyStopped,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		})
	}
	list, _ := s.SnapshotStore().ListBySandbox(t.Context(), sbx.SandboxID)
	if len(list) != 3 {
		t.Errorf("expected 3, got %d", len(list))
	}
}

func TestSnapshotNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SnapshotStore().Get(t.Context(), "nope")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSnapshotListOrphaned(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	snap := &domain.Snapshot{
		SnapshotID: uuid.NewString(), SandboxID: sbx.SandboxID,
		Backend: "incus", Label: "orphan", State: domain.SnapshotReady,
		ConsistencyMode: domain.ConsistencyStopped,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.SnapshotStore().Create(t.Context(), snap)

	// Mark sandbox as destroyed
	sbx.State = domain.SandboxDestroyed
	sbx.UpdatedAt = time.Now().UTC()
	s.SandboxStore().Update(t.Context(), sbx)

	orphaned, err := s.SnapshotStore().ListOrphaned(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(orphaned) != 1 {
		t.Errorf("expected 1 orphaned, got %d", len(orphaned))
	}
}
