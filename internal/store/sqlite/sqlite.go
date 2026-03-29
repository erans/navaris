package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/navaris/navaris/internal/store"
	_ "modernc.org/sqlite"
)

var _ store.Store = (*Store)(nil)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

// Open creates a new Store with separate read and write connection pools.
// Pragmas are encoded in the DSN so every pooled connection inherits them.
// The write pool is limited to a single connection to serialize writes and
// eliminate SQLITE_BUSY errors. The read pool allows concurrent readers.
func Open(dsn string) (*Store, error) {
	pragmaDSN := buildDSN(dsn)

	writeDB, err := sql.Open("sqlite", pragmaDSN)
	if err != nil {
		return nil, fmt.Errorf("open sqlite (write): %w", err)
	}
	writeDB.SetMaxOpenConns(1)

	readDB, err := sql.Open("sqlite", pragmaDSN)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open sqlite (read): %w", err)
	}
	readDB.SetMaxOpenConns(4)

	s := &Store{readDB: readDB, writeDB: writeDB}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// buildDSN appends _pragma query parameters to the given DSN path.
func buildDSN(dsn string) string {
	pragmas := []string{
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(ON)",
		"_pragma=busy_timeout(5000)",
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + strings.Join(pragmas, "&")
}

func (s *Store) DB() *sql.DB { return s.readDB }

func (s *Store) Close() error {
	rErr := s.readDB.Close()
	wErr := s.writeDB.Close()
	if rErr != nil {
		return rErr
	}
	return wErr
}

func (s *Store) migrate() error {
	_, err := s.writeDB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return err
	}

	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var count int
		s.writeDB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", entry.Name()).Scan(&count)
		if count > 0 {
			continue
		}
		content, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.writeDB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", entry.Name()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
