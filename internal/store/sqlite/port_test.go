package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

func TestPortBindingCreateAndList(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	pb := &domain.PortBinding{
		SandboxID: sbx.SandboxID, TargetPort: 80, PublishedPort: 40000,
		HostAddress: "0.0.0.0", CreatedAt: time.Now().UTC(),
	}
	if err := s.PortBindingStore().Create(t.Context(), pb); err != nil {
		t.Fatal(err)
	}
	list, err := s.PortBindingStore().ListBySandbox(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 binding, got %d", len(list))
	}
}

func TestPortBindingGetByPublishedPort(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	s.PortBindingStore().Create(t.Context(), &domain.PortBinding{
		SandboxID: sbx.SandboxID, TargetPort: 80, PublishedPort: 40000,
		HostAddress: "0.0.0.0", CreatedAt: time.Now().UTC(),
	})
	got, err := s.PortBindingStore().GetByPublishedPort(t.Context(), 40000)
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetPort != 80 {
		t.Errorf("wrong target port: %d", got.TargetPort)
	}
}

func TestPortBindingNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.PortBindingStore().GetByPublishedPort(t.Context(), 99999)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPortNextAvailable(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx-"+uuid.NewString()[:8])

	// First allocation returns range start
	port1, err := s.PortBindingStore().NextAvailablePort(t.Context(), 40000, 40010)
	if err != nil {
		t.Fatal(err)
	}
	if port1 != 40000 {
		t.Errorf("expected 40000, got %d", port1)
	}

	s.PortBindingStore().Create(t.Context(), &domain.PortBinding{
		SandboxID: sbx.SandboxID, TargetPort: 80, PublishedPort: port1,
		HostAddress: "0.0.0.0", CreatedAt: time.Now().UTC(),
	})

	// Second allocation returns next
	port2, err := s.PortBindingStore().NextAvailablePort(t.Context(), 40000, 40010)
	if err != nil {
		t.Fatal(err)
	}
	if port2 != 40001 {
		t.Errorf("expected 40001, got %d", port2)
	}
}

func TestPortDelete(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx1")
	s.PortBindingStore().Create(t.Context(), &domain.PortBinding{
		SandboxID: sbx.SandboxID, TargetPort: 80, PublishedPort: 40000,
		HostAddress: "0.0.0.0", CreatedAt: time.Now().UTC(),
	})
	if err := s.PortBindingStore().Delete(t.Context(), sbx.SandboxID, 80); err != nil {
		t.Fatal(err)
	}
	list, _ := s.PortBindingStore().ListBySandbox(t.Context(), sbx.SandboxID)
	if len(list) != 0 {
		t.Error("expected empty after delete")
	}
}
