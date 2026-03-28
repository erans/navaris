package sqlite_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

func TestProjectCreate(t *testing.T) {
	s := newTestStore(t)
	p := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      "test-project",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.ProjectStore().Create(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProjectStore().Get(t.Context(), p.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != p.Name {
		t.Errorf("got name %q, want %q", got.Name, p.Name)
	}
}

func TestProjectGetByName(t *testing.T) {
	s := newTestStore(t)
	p := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      "by-name",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	s.ProjectStore().Create(t.Context(), p)
	got, err := s.ProjectStore().GetByName(t.Context(), "by-name")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != p.ProjectID {
		t.Error("wrong project returned")
	}
}

func TestProjectNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ProjectStore().Get(t.Context(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProjectList(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		s.ProjectStore().Create(t.Context(), &domain.Project{
			ProjectID: uuid.NewString(),
			Name:      "proj-" + uuid.NewString()[:8],
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		})
	}
	list, err := s.ProjectStore().List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Errorf("got %d projects, want 3", len(list))
	}
}

func TestProjectDuplicateName(t *testing.T) {
	s := newTestStore(t)
	p1 := &domain.Project{ProjectID: uuid.NewString(), Name: "dup", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	p2 := &domain.Project{ProjectID: uuid.NewString(), Name: "dup", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	s.ProjectStore().Create(t.Context(), p1)
	err := s.ProjectStore().Create(t.Context(), p2)
	if err == nil {
		t.Fatal("expected conflict error on duplicate name")
	}
}
