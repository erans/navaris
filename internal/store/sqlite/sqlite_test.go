package sqlite_test

import (
	"path/filepath"
	"testing"

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
