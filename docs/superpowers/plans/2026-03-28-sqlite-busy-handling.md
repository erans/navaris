# SQLite Busy Handling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate SQLITE_BUSY errors under concurrent load by moving pragmas to the DSN and splitting read/write connection pools.

**Architecture:** Two `*sql.DB` pools open the same DSN with `_pragma` query parameters. `writeDB` has `MaxOpenConns(1)` to serialize writes; `readDB` allows concurrent reads via WAL mode. Each sub-store receives both pools and routes methods accordingly. A new `domain.ErrBusy` sentinel maps SQLITE_BUSY/LOCKED to HTTP 503.

**Tech Stack:** Go, `modernc.org/sqlite` v1.48.0, `database/sql`

---

### Task 1: Add `domain.ErrBusy` sentinel and API 503 mapping

**Files:**
- Modify: `internal/domain/errors.go:5-11`
- Modify: `internal/api/response.go:40-69`

- [ ] **Step 1: Add ErrBusy to domain errors**

In `internal/domain/errors.go`, add `ErrBusy` to the var block:

```go
var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrInvalidState     = errors.New("invalid state transition")
	ErrCapacityExceeded = errors.New("capacity exceeded")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrBusy             = errors.New("database busy")
)
```

- [ ] **Step 2: Map ErrBusy to 503 in the API layer**

In `internal/api/response.go`, add a case for `ErrBusy` in `mapErrorCode()` (before the default 500 return):

```go
if errors.Is(err, domain.ErrBusy) {
	return http.StatusServiceUnavailable
}
```

Update `respondError()` to add `Retry-After` header for 503:

```go
func respondError(w http.ResponseWriter, err error) {
	code := mapErrorCode(err)
	resp := errorResponse{}
	resp.Error.Code = code
	switch {
	case code == http.StatusServiceUnavailable:
		resp.Error.Message = "service temporarily unavailable"
	case code >= 500:
		resp.Error.Message = "internal server error"
		slog.Error("api error", "status", code, "error", err.Error())
	default:
		resp.Error.Message = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	if code == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 3: Run existing tests**

Run: `go test ./internal/api/... ./internal/domain/...`
Expected: PASS (no behavioral change yet)

- [ ] **Step 4: Commit**

```bash
git add internal/domain/errors.go internal/api/response.go
git commit -m "feat: add domain.ErrBusy sentinel and HTTP 503 mapping"
```

---

### Task 2: Update `mapError` with typed SQLite error detection

**Files:**
- Modify: `internal/store/sqlite/project.go:1-13,137-149`

- [ ] **Step 1: Add modernc.org/sqlite import and rewrite mapError**

In `internal/store/sqlite/project.go`, add the `modernc.org/sqlite` import (aliased to avoid conflict with package name) and the `modernc.org/sqlite/lib` import for constants. Then rewrite `mapError`:

```go
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/navaris/navaris/internal/domain"
	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)
```

Replace the `mapError` function:

```go
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return fmt.Errorf("%w: %s", domain.ErrBusy, sqliteErr.Error())
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %s", domain.ErrConflict, msg)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w", domain.ErrNotFound)
	}
	return err
}
```

- [ ] **Step 2: Route Delete and List methods through mapError**

Ensure all store methods return errors through `mapError` consistently — not just write methods. Currently, all Delete methods and all List/ListBy*/ListExpired/ListStale/ListByState/ListOrphaned methods return raw errors.

**Delete methods** — change `return err` to `return mapError(err)` in the error path:


**`internal/store/sqlite/project.go:81-87`:**
```go
func (ps *projectStore) Delete(ctx context.Context, id string) error {
	res, err := ps.db.ExecContext(ctx, `DELETE FROM projects WHERE project_id = ?`, id)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}
```

**`internal/store/sqlite/sandbox.go:106-112`:**
```go
func (ss *sandboxStore) Delete(ctx context.Context, id string) error {
	res, err := ss.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE sandbox_id = ?`, id)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}
```

**`internal/store/sqlite/snapshot.go:93-99`:**
```go
func (ss *snapshotStore) Delete(ctx context.Context, id string) error {
	res, err := ss.db.ExecContext(ctx, `DELETE FROM snapshots WHERE snapshot_id = ?`, id)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}
```

**`internal/store/sqlite/session.go:92-98`:**
```go
func (ss *sessionStore) Delete(ctx context.Context, id string) error {
	res, err := ss.db.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = ?`, id)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}
```

**`internal/store/sqlite/image.go:99-105`:**
```go
func (is *imageStore) Delete(ctx context.Context, id string) error {
	res, err := is.db.ExecContext(ctx, `DELETE FROM base_images WHERE image_id = ?`, id)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}
```

**`internal/store/sqlite/port.go:49-57`:**
```go
func (ps *portBindingStore) Delete(ctx context.Context, sandboxID string, targetPort int) error {
	res, err := ps.db.ExecContext(ctx,
		`DELETE FROM port_bindings WHERE sandbox_id = ? AND target_port = ?`,
		sandboxID, targetPort)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}
```

**Read methods** — wrap error returns with `mapError` in every List method. The pattern is the same across all files. Each List method has up to 3 error returns — wrap all of them. Example for `projectStore.List`:

```go
func (ps *projectStore) List(ctx context.Context) ([]*domain.Project, error) {
	rows, err := ps.db.QueryContext(ctx, `SELECT ...`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var projects []*domain.Project
	for rows.Next() {
		p, err := scanProjectRow(rows)
		if err != nil {
			return nil, mapError(err)
		}
		projects = append(projects, p)
	}
	return projects, mapError(rows.Err())
}
```

Apply the same `mapError` wrapping to all List methods:

| File | Method | Error returns to wrap |
|------|--------|-----------------------|
| `project.go` | `List` | QueryContext err, scan err, rows.Err() |
| `sandbox.go` | `List` | QueryContext err, scan err, rows.Err() |
| `sandbox.go` | `ListExpired` | QueryContext err, scan err, rows.Err() |
| `snapshot.go` | `ListBySandbox` | QueryContext err, scan err, rows.Err() |
| `snapshot.go` | `ListOrphaned` | QueryContext err, scan err, rows.Err() |
| `session.go` | `ListBySandbox` | QueryContext err, scan err, rows.Err() |
| `image.go` | `List` | QueryContext err, scan err, rows.Err() |
| `operation.go` | `List` | QueryContext err, scan err, rows.Err() |
| `operation.go` | `ListStale` | QueryContext err, scan err, rows.Err() |
| `operation.go` | `ListByState` | QueryContext err, scan err, rows.Err() |
| `port.go` | `ListBySandbox` | QueryContext err, scan err, rows.Err() |

**Get methods** — each `scanXxx(row *sql.Row)` helper has a fallthrough `return nil, err` after the `ErrNoRows` check. `QueryRowContext` defers errors to `Scan()`, so a `SQLITE_BUSY` from query execution surfaces there unwrapped. Change the fallthrough path in each scan helper:

| File | Function | Change |
|------|----------|--------|
| `project.go` | `scanProject` | `return nil, err` → `return nil, mapError(err)` |
| `sandbox.go` | `scanSandbox` | `return nil, err` → `return nil, mapError(err)` |
| `snapshot.go` | `scanSnapshot` | `return nil, err` → `return nil, mapError(err)` |
| `session.go` | `scanSession` | `return nil, err` → `return nil, mapError(err)` |
| `image.go` | `scanImage` | `return nil, err` → `return nil, mapError(err)` |
| `operation.go` | `scanOperation` | `return nil, err` → `return nil, mapError(err)` |

Also wrap the raw error returns in `GetByPublishedPort` (port.go line 70: `return nil, err`) and `NextAvailablePort` (port.go line 93: `return 0, err`) with `mapError`.

- [ ] **Step 3: Run `go mod tidy`**

The new `modernc.org/sqlite/lib` import is a sub-package of `modernc.org/sqlite` (already a direct dependency), so no new module is needed:

Run: `go mod tidy`
Expected: No errors, no changes to go.mod/go.sum.

- [ ] **Step 4: Run store unit tests**

Run: `go test ./internal/store/sqlite/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/project.go internal/store/sqlite/sandbox.go internal/store/sqlite/snapshot.go internal/store/sqlite/session.go internal/store/sqlite/image.go internal/store/sqlite/operation.go internal/store/sqlite/port.go go.mod go.sum
git commit -m "feat: add typed SQLITE_BUSY detection and consistent mapError"
```

---

### Task 3: Implement dual read/write connection pools and wire sub-stores

**Files:**
- Modify: `internal/store/sqlite/sqlite.go`
- Modify: `internal/store/sqlite/project.go:15-21`
- Modify: `internal/store/sqlite/sandbox.go` (struct + constructor)
- Modify: `internal/store/sqlite/snapshot.go` (struct + constructor)
- Modify: `internal/store/sqlite/session.go` (struct + constructor)
- Modify: `internal/store/sqlite/image.go` (struct + constructor)
- Modify: `internal/store/sqlite/operation.go` (struct + constructor)
- Modify: `internal/store/sqlite/port.go` (struct + constructor)

- [ ] **Step 1: Rewrite Store struct and Open function**

Replace the entire `Open` function and `Store` struct in `internal/store/sqlite/sqlite.go`:

```go
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
	// Migrations use the write connection (DDL + DML).
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
```

- [ ] **Step 2: Update project store (template for all others)**

In `internal/store/sqlite/project.go`, change the struct and constructor:

```go
type projectStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) ProjectStore() domain.ProjectStore {
	return &projectStore{readDB: s.readDB, writeDB: s.writeDB}
}
```

Then update every method to use the correct pool:
- `Create`: change `ps.db.ExecContext` → `ps.writeDB.ExecContext`
- `Get`: change `ps.db.QueryRowContext` → `ps.readDB.QueryRowContext`
- `GetByName`: change `ps.db.QueryRowContext` → `ps.readDB.QueryRowContext`
- `List`: change `ps.db.QueryContext` → `ps.readDB.QueryContext`
- `Update`: change `ps.db.ExecContext` → `ps.writeDB.ExecContext`
- `Delete`: change `ps.db.ExecContext` → `ps.writeDB.ExecContext`

- [ ] **Step 3: Update sandbox store**

In `internal/store/sqlite/sandbox.go`:

```go
type sandboxStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) SandboxStore() domain.SandboxStore {
	return &sandboxStore{readDB: s.readDB, writeDB: s.writeDB}
}
```

Route methods:
- `Create` → `ss.writeDB.ExecContext`
- `Get` → `ss.readDB.QueryRowContext`
- `List` → `ss.readDB.QueryContext`
- `Update` → `ss.writeDB.ExecContext`
- `Delete` → `ss.writeDB.ExecContext`
- `ListExpired` → `ss.readDB.QueryContext`

- [ ] **Step 4: Update snapshot store**

In `internal/store/sqlite/snapshot.go`:

```go
type snapshotStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) SnapshotStore() domain.SnapshotStore {
	return &snapshotStore{readDB: s.readDB, writeDB: s.writeDB}
}
```

Route methods:
- `Create` → `ss.writeDB.ExecContext`
- `Get` → `ss.readDB.QueryRowContext`
- `ListBySandbox` → `ss.readDB.QueryContext`
- `Update` → `ss.writeDB.ExecContext`
- `Delete` → `ss.writeDB.ExecContext`
- `ListOrphaned` → `ss.readDB.QueryContext`

- [ ] **Step 5: Update session store**

In `internal/store/sqlite/session.go`:

```go
type sessionStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) SessionStore() domain.SessionStore {
	return &sessionStore{readDB: s.readDB, writeDB: s.writeDB}
}
```

Route methods:
- `Create` → `ss.writeDB.ExecContext`
- `Get` → `ss.readDB.QueryRowContext`
- `ListBySandbox` → `ss.readDB.QueryContext`
- `Update` → `ss.writeDB.ExecContext`
- `Delete` → `ss.writeDB.ExecContext`

- [ ] **Step 6: Update image store**

In `internal/store/sqlite/image.go`:

```go
type imageStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) ImageStore() domain.ImageStore {
	return &imageStore{readDB: s.readDB, writeDB: s.writeDB}
}
```

Route methods:
- `Create` → `is.writeDB.ExecContext`
- `Get` → `is.readDB.QueryRowContext`
- `List` → `is.readDB.QueryContext`
- `Update` → `is.writeDB.ExecContext`
- `Delete` → `is.writeDB.ExecContext`

- [ ] **Step 7: Update operation store**

In `internal/store/sqlite/operation.go`:

```go
type operationStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) OperationStore() domain.OperationStore {
	return &operationStore{readDB: s.readDB, writeDB: s.writeDB}
}
```

Route methods:
- `Create` → `os.writeDB.ExecContext`
- `Get` → `os.readDB.QueryRowContext`
- `List` → `os.readDB.QueryContext`
- `Update` → `os.writeDB.ExecContext`
- `ListStale` → `os.readDB.QueryContext`
- `ListByState` → `os.readDB.QueryContext`

- [ ] **Step 8: Update port binding store**

In `internal/store/sqlite/port.go`:

```go
type portBindingStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) PortBindingStore() domain.PortBindingStore {
	return &portBindingStore{readDB: s.readDB, writeDB: s.writeDB}
}
```

Route methods:
- `Create` → `ps.writeDB.ExecContext`
- `ListBySandbox` → `ps.readDB.QueryContext`
- `Delete` → `ps.writeDB.ExecContext`
- `GetByPublishedPort` → `ps.readDB.QueryRowContext`
- `NextAvailablePort` → **`ps.writeDB.QueryRowContext`** (write-path read — prevents TOCTOU race with Create)

Explicit `NextAvailablePort` after wiring:

```go
func (ps *portBindingStore) NextAvailablePort(ctx context.Context, rangeStart, rangeEnd int) (int, error) {
	// Uses writeDB to prevent TOCTOU race: two concurrent callers could
	// see the same available port via readDB before either writes.
	row := ps.writeDB.QueryRowContext(ctx, `
		WITH RECURSIVE candidates(port) AS (
			SELECT ?
			UNION ALL
			SELECT port + 1 FROM candidates WHERE port < ?
		)
		SELECT port FROM candidates
		WHERE port NOT IN (SELECT published_port FROM port_bindings)
		LIMIT 1`, rangeStart, rangeEnd)
	var port int
	err := row.Scan(&port)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("port range exhausted: %w", domain.ErrCapacityExceeded)
		}
		return 0, mapError(err)
	}
	return port, nil
}
```

- [ ] **Step 9: Verify compilation**

Run: `go build ./...`
Expected: PASS — all sub-stores now reference `readDB`/`writeDB`

- [ ] **Step 10: Commit**

```bash
git add internal/store/sqlite/sqlite.go internal/store/sqlite/project.go internal/store/sqlite/sandbox.go internal/store/sqlite/snapshot.go internal/store/sqlite/session.go internal/store/sqlite/image.go internal/store/sqlite/operation.go internal/store/sqlite/port.go
git commit -m "feat: implement dual read/write connection pools and wire sub-stores"
```

---

### Task 4: Fix unit test helper for dual-pool `:memory:` incompatibility

**Files:**
- Modify: `internal/store/sqlite/sqlite_test.go:1-17`

- [ ] **Step 1: Update `newTestStore` to use a temp file**

Opening two `*sql.DB` against `:memory:` creates two separate in-memory databases. Switch to a temp file:

```go
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
```

- [ ] **Step 2: Run all store unit tests**

Run: `go test ./internal/store/sqlite/... -v`
Expected: PASS — all existing tests work with temp-file DB

- [ ] **Step 3: Commit**

```bash
git add internal/store/sqlite/sqlite_test.go
git commit -m "fix: use temp file in store tests for dual-pool compat"
```

---

### Task 5: Add concurrent write unit test

**Files:**
- Modify: `internal/store/sqlite/sqlite_test.go`

- [ ] **Step 1: Write the concurrent write test**

Add to `internal/store/sqlite/sqlite_test.go`:

```go
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
```

Add the required imports at the top of the file:

```go
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
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/store/sqlite/... -run TestConcurrentWrites -v`
Expected: PASS — zero ErrBusy errors, all 20 projects created

- [ ] **Step 3: Commit**

```bash
git add internal/store/sqlite/sqlite_test.go
git commit -m "test: add concurrent write test validating single-writer pool"
```

---

### Task 6: Remove retry hack from integration test

**Files:**
- Modify: `test/integration/concurrent_test.go`

- [ ] **Step 1: Simplify `TestConcurrentSandboxCreation`**

Remove the retry logic and `isRetryable500` helper. Concurrent creation should work on the first attempt now:

```go
//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// TestConcurrentSandboxCreation verifies that multiple sandboxes can be
// created concurrently without SQLITE_BUSY errors.
func TestConcurrentSandboxCreation(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	const n = 3
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		sandboxIDs []string
		errors     []error
	)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
				ProjectID: proj.ProjectID,
				Name:      fmt.Sprintf("concurrent-sbx-%d", idx),
				ImageID:   baseImage(),
			}, waitOpts())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, fmt.Errorf("sandbox %d: %w", idx, err))
				return
			}
			if op.State != client.OpSucceeded {
				errors = append(errors, fmt.Errorf("sandbox %d: state=%s error=%s", idx, op.State, op.ErrorText))
				return
			}
			sandboxIDs = append(sandboxIDs, op.ResourceID)
		}(i)
	}
	wg.Wait()

	t.Cleanup(func() {
		for _, id := range sandboxIDs {
			_, _ = c.DestroySandboxAndWait(context.Background(), id, waitOpts())
		}
	})

	if len(errors) > 0 {
		for _, err := range errors {
			t.Errorf("concurrent error: %v", err)
		}
		t.FailNow()
	}

	if len(sandboxIDs) != n {
		t.Fatalf("expected %d sandboxes, got %d", n, len(sandboxIDs))
	}

	for _, id := range sandboxIDs {
		sbx, err := c.GetSandbox(ctx, id)
		if err != nil {
			t.Fatalf("get sandbox %s: %v", id, err)
		}
		if sbx.State != "running" {
			t.Fatalf("sandbox %s state: %s", id, sbx.State)
		}
	}

	t.Logf("all %d sandboxes created concurrently and running", n)
}
```

- [ ] **Step 2: Run integration tests**

Run: `make integration-test`
Expected: All tests PASS — concurrent test works without retries

- [ ] **Step 3: Commit**

```bash
git add test/integration/concurrent_test.go
git commit -m "test: remove SQLITE_BUSY retry hack from concurrent test"
```
