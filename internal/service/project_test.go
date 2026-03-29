package service_test

import (
	"errors"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
)

func newProjectService(t *testing.T) *service.ProjectService {
	t.Helper()
	s, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return service.NewProjectService(s.ProjectStore())
}

func TestProjectServiceCreate(t *testing.T) {
	svc := newProjectService(t)
	p, err := svc.Create(t.Context(), "my-project", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "my-project" {
		t.Errorf("wrong name")
	}
	if p.ProjectID == "" {
		t.Error("expected ID to be set")
	}
}

func TestProjectServiceDuplicateName(t *testing.T) {
	svc := newProjectService(t)
	svc.Create(t.Context(), "dup", nil)
	_, err := svc.Create(t.Context(), "dup", nil)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestProjectServiceGetAndList(t *testing.T) {
	svc := newProjectService(t)
	p, _ := svc.Create(t.Context(), "proj1", nil)
	svc.Create(t.Context(), "proj2", nil)

	got, err := svc.Get(t.Context(), p.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "proj1" {
		t.Error("wrong project")
	}

	list, err := svc.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}
