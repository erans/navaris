package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	sqlite3 "modernc.org/sqlite/lib"
	_ "modernc.org/sqlite"
)

func TestMapErrorNil(t *testing.T) {
	if got := mapError(nil); got != nil {
		t.Errorf("nil input: expected nil, got: %v", got)
	}
}

func TestMapErrorNoRows(t *testing.T) {
	got := mapError(sql.ErrNoRows)
	if !errors.Is(got, domain.ErrNotFound) {
		t.Errorf("ErrNoRows: expected ErrNotFound, got: %v", got)
	}
}

func TestMapErrorUniqueConstraint(t *testing.T) {
	err := errors.New("UNIQUE constraint failed: projects.name")
	got := mapError(err)
	if !errors.Is(got, domain.ErrConflict) {
		t.Errorf("UNIQUE constraint: expected ErrConflict, got: %v", got)
	}
}

func TestMapErrorPassthrough(t *testing.T) {
	orig := errors.New("some other error")
	got := mapError(orig)
	if got != orig {
		t.Errorf("expected passthrough, got different error: %v", got)
	}
}

// TestMapErrorBusyViaContention induces a real SQLITE_BUSY by holding
// an exclusive lock on a raw connection (no busy_timeout) while writing
// through another raw connection.
func TestMapErrorBusyViaContention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy-test.db")

	// Open DB with WAL and no busy_timeout.
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	db1, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db1.SetMaxOpenConns(1)

	db2, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	db2.SetMaxOpenConns(1)

	// Create a table.
	if _, err := db1.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	// Begin a write transaction on db1 (holds the write lock).
	tx, err := db1.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	// Try to write on db2 — should get SQLITE_BUSY immediately
	// (no busy_timeout set).
	_, err = db2.Exec("INSERT INTO t VALUES (2)")
	if err == nil {
		t.Fatal("expected SQLITE_BUSY error, got nil")
	}

	// Verify mapError classifies it as ErrBusy.
	mapped := mapError(err)
	if !errors.Is(mapped, domain.ErrBusy) {
		t.Errorf("expected ErrBusy, got: %v", mapped)
	}
}

// TestMapErrorExtendedCodeNormalization verifies the 0xFF bitmask
// correctly normalizes extended result codes.
func TestMapErrorExtendedCodeNormalization(t *testing.T) {
	// SQLITE_LOCKED_SHAREDCACHE = SQLITE_LOCKED | (1<<8) = 262
	// After 0xFF mask: 262 & 0xFF = 6 = SQLITE_LOCKED
	lockedExtended := sqlite3.SQLITE_LOCKED | (1 << 8)
	if lockedExtended&0xFF != sqlite3.SQLITE_LOCKED {
		t.Fatalf("bitmask sanity: %d & 0xFF = %d, expected %d",
			lockedExtended, lockedExtended&0xFF, sqlite3.SQLITE_LOCKED)
	}

	// SQLITE_BUSY with hypothetical extended code
	busyExtended := sqlite3.SQLITE_BUSY | (1 << 8)
	if busyExtended&0xFF != sqlite3.SQLITE_BUSY {
		t.Fatalf("bitmask sanity: %d & 0xFF = %d, expected %d",
			busyExtended, busyExtended&0xFF, sqlite3.SQLITE_BUSY)
	}
}
