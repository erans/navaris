package sqlite_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/store/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpen(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.DB().QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	expected := []string{"base_images", "operations", "port_bindings", "projects", "sandboxes", "schema_migrations", "sessions", "snapshots"}
	if len(tables) != len(expected) {
		t.Fatalf("expected %d tables, got %d: %v", len(expected), len(tables), tables)
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := newTestStore(t)
	ps := s.ProjectStore()

	const n = 20
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			p := &domain.Project{
				ProjectID: uuid.NewString(),
				Name:      fmt.Sprintf("concurrent-%d", idx),
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}
			errs <- ps.Create(t.Context(), p)
		}(i)
	}

	var busy int
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			if errors.Is(err, domain.ErrBusy) {
				busy++
			} else if !errors.Is(err, domain.ErrConflict) {
				t.Errorf("unexpected error: %v", err)
			}
		}
	}

	if busy > 0 {
		t.Errorf("got %d ErrBusy errors — single-writer pool should prevent this", busy)
	}

	list, err := ps.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != n {
		t.Errorf("expected %d projects, got %d", n, len(list))
	}
}

func TestMapErrorConflict(t *testing.T) {
	s := newTestStore(t)
	ps := s.ProjectStore()

	p := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      "duplicate",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := ps.Create(t.Context(), p); err != nil {
		t.Fatal(err)
	}

	dup := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      "duplicate",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	err := ps.Create(t.Context(), dup)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}
}

func TestMapErrorNotFound(t *testing.T) {
	s := newTestStore(t)
	ps := s.ProjectStore()

	_, err := ps.Get(t.Context(), "nonexistent")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestMapErrorDeleteNotFound(t *testing.T) {
	s := newTestStore(t)
	ps := s.ProjectStore()

	err := ps.Delete(t.Context(), "nonexistent")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound from delete, got: %v", err)
	}
}
