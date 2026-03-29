# SQLite Busy Handling Design

## Problem

The navarisd server uses SQLite (via `modernc.org/sqlite`) with WAL mode. Under concurrent load (e.g., multiple sandbox creations), `SQLITE_BUSY` errors surface as opaque HTTP 500 responses.

**Root cause:** `PRAGMA busy_timeout=5000` is set via `db.Exec()` in `sqlite.Open()`, which only applies to the single connection used for that call. Go's `database/sql` manages a connection pool, and new connections don't inherit the pragma. Concurrent requests landing on fresh connections see no busy timeout and fail immediately.

**Secondary issue:** The `mapError` helper doesn't handle `SQLITE_BUSY`, so when it does occur, the raw SQLite error propagates as an unclassified 500 instead of a retryable error.

## Design

### DSN-Based Pragmas

Move all pragma configuration from `db.Exec()` calls to DSN query parameters using `modernc.org/sqlite`'s `_pragma` syntax:

```
file:<path>?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)
```

This ensures every connection opened by the pool inherits the same configuration.

### Read/Write Connection Split

Open two `*sql.DB` instances from the same pragma-enriched DSN:

- **`writeDB`**: `SetMaxOpenConns(1)` — serializes all writes through a single connection, eliminating write-write contention entirely.
- **`readDB`**: Default pool (no explicit limit) — allows concurrent reads, which WAL mode supports without blocking writers.

The `Store` struct becomes:

```go
type Store struct {
    readDB  *sql.DB
    writeDB *sql.DB
}
```

`Close()` closes both. `DB()` returns `readDB` for external consumers (migrations, health checks).

### Sub-Store Wiring

Each sub-store struct gains both fields:

```go
type projectStore struct {
    readDB  *sql.DB
    writeDB *sql.DB
}
```

Method routing is mechanical:
- **Read methods** (Get, List, ListExpired, ListBySandbox, etc.) use `s.readDB` via `QueryContext`/`QueryRowContext`.
- **Write methods** (Create, Update, Delete) use `s.writeDB` via `ExecContext`.

No method mixes reads and writes in a single call. The domain store interfaces (`domain.ProjectStore`, etc.) are unchanged.

### Error Mapping (Defense-in-Depth)

Add a `domain.ErrBusy` sentinel error. Update `mapError` to detect `SQLITE_BUSY` / `database is locked` strings and wrap them as `domain.ErrBusy`.

Map `domain.ErrBusy` to HTTP 503 (Service Unavailable) in the API error handler. This provides a clear, retryable signal to clients if contention ever occurs (e.g., during migrations or if pool config changes).

### Files Changed

| File | Change |
|------|--------|
| `internal/store/sqlite/sqlite.go` | DSN pragma construction, dual pool setup |
| `internal/store/sqlite/project.go` | `readDB`/`writeDB` fields, route methods |
| `internal/store/sqlite/sandbox.go` | Same pattern |
| `internal/store/sqlite/snapshot.go` | Same pattern |
| `internal/store/sqlite/session.go` | Same pattern |
| `internal/store/sqlite/image.go` | Same pattern |
| `internal/store/sqlite/operation.go` | Same pattern |
| `internal/store/sqlite/port.go` | Same pattern |
| `internal/domain/errors.go` | Add `ErrBusy` sentinel |
| `internal/api/errors.go` (or equivalent) | Map `ErrBusy` -> 503 |
| `test/integration/concurrent_test.go` | Remove retry hack, expect clean concurrency |

### Testing

1. **Unit test** (`internal/store/sqlite/`): Concurrent goroutine writes to the store, assert zero `SQLITE_BUSY` errors.
2. **Integration test**: Remove `isRetryable500` retry logic from `TestConcurrentSandboxCreation` — concurrent sandbox creation works without client-side retries.
3. **Existing tests**: Unchanged — domain interfaces are not modified.

## Out of Scope

- Migrating away from SQLite (planned separately)
- Application-level retry middleware
- Read replica or connection routing in the domain layer
