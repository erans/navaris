package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/store/sqlite"
)

func createTestOp(t *testing.T, s *sqlite.Store, state domain.OperationState, finishedAt time.Time) *domain.Operation {
	t.Helper()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   uuid.NewString(),
		Type:         "create_sandbox",
		State:        state,
		StartedAt:    finishedAt.Add(-time.Hour),
	}
	if state.Terminal() {
		op.FinishedAt = &finishedAt
	}
	if err := s.OperationStore().Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op
}

func TestOperationCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   uuid.NewString(),
		Type:         "create_sandbox",
		State:        domain.OpPending,
		StartedAt:    time.Now().UTC(),
	}
	if err := s.OperationStore().Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	got, err := s.OperationStore().Get(t.Context(), op.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "create_sandbox" {
		t.Error("wrong type")
	}
}

func TestOperationListByState(t *testing.T) {
	s := newTestStore(t)
	createTestOp(t, s, domain.OpPending, time.Now().UTC())
	createTestOp(t, s, domain.OpPending, time.Now().UTC())
	createTestOp(t, s, domain.OpSucceeded, time.Now().UTC())

	pending, _ := s.OperationStore().ListByState(t.Context(), domain.OpPending)
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(pending))
	}
}

func TestOperationListStale(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)
	createTestOp(t, s, domain.OpSucceeded, old)
	createTestOp(t, s, domain.OpFailed, old)
	createTestOp(t, s, domain.OpRunning, old)
	createTestOp(t, s, domain.OpSucceeded, recent)
	stale, _ := s.OperationStore().ListStale(t.Context(), time.Now().UTC().Add(-24*time.Hour))
	if len(stale) != 2 {
		t.Errorf("expected 2 stale ops, got %d", len(stale))
	}
}

func TestOperationUpdate(t *testing.T) {
	s := newTestStore(t)
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   uuid.NewString(),
		Type:         "create_sandbox",
		State:        domain.OpRunning,
		StartedAt:    time.Now().UTC(),
	}
	s.OperationStore().Create(t.Context(), op)
	now := time.Now().UTC()
	op.State = domain.OpFailed
	op.FinishedAt = &now
	op.ErrorText = "something broke"
	if err := s.OperationStore().Update(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	got, _ := s.OperationStore().Get(t.Context(), op.OperationID)
	if got.State != domain.OpFailed {
		t.Error("state not updated")
	}
	if got.ErrorText != "something broke" {
		t.Error("error_text not set")
	}
}

func TestOperationNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.OperationStore().Get(t.Context(), "nope")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
