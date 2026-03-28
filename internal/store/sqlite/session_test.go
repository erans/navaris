package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

func TestSessionCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	timeout := 30 * time.Minute
	sess := &domain.Session{
		SessionID:   uuid.NewString(),
		SandboxID:   sbx.SandboxID,
		Backing:     domain.SessionBackingDirect,
		Shell:       "/bin/bash",
		State:       domain.SessionActive,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		IdleTimeout: &timeout,
	}
	if err := s.SessionStore().Create(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionStore().Get(t.Context(), sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Shell != "/bin/bash" {
		t.Error("wrong shell")
	}
	if got.IdleTimeout == nil || *got.IdleTimeout != 30*time.Minute {
		t.Error("wrong idle timeout")
	}
}

func TestSessionListBySandbox(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	for i := 0; i < 2; i++ {
		s.SessionStore().Create(t.Context(), &domain.Session{
			SessionID: uuid.NewString(), SandboxID: sbx.SandboxID,
			Backing: domain.SessionBackingDirect, Shell: "/bin/bash",
			State: domain.SessionActive,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		})
	}
	list, _ := s.SessionStore().ListBySandbox(t.Context(), sbx.SandboxID)
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestSessionUpdate(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	sess := &domain.Session{
		SessionID: uuid.NewString(), SandboxID: sbx.SandboxID,
		Backing: domain.SessionBackingDirect, Shell: "/bin/bash",
		State: domain.SessionActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.SessionStore().Create(t.Context(), sess)
	now := time.Now().UTC()
	sess.State = domain.SessionDetached
	sess.LastAttachedAt = &now
	sess.UpdatedAt = now
	if err := s.SessionStore().Update(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	got, _ := s.SessionStore().Get(t.Context(), sess.SessionID)
	if got.State != domain.SessionDetached {
		t.Error("state not updated")
	}
	if got.LastAttachedAt == nil {
		t.Error("last_attached_at not set")
	}
}

func TestSessionNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SessionStore().Get(t.Context(), "nope")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
