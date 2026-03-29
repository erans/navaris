# SQLite Busy Handling Design

## Problem

The navarisd server uses SQLite (via `modernc.org/sqlite`) with WAL mode. Under concurrent load (e.g., multiple sandbox creations), `SQLITE_BUSY` errors surface as opaque HTTP 500 responses.

**Root cause:** `PRAGMA busy_timeout=5000` is set via `db.Exec()` in `sqlite.Open()`, which only applies to the single connection used for that call. Go's `database/sql` manages a connection pool, and new connections don't inherit the pragma. Concurrent requests landing on fresh connections see no busy timeout and fail immediately.

**Secondary issue:** The `mapError` helper doesn't handle `SQLITE_BUSY` or `SQLITE_LOCKED`, and is not called consistently across all store methods (notably missing from `Delete` methods). Raw SQLite errors propagate as unclassified 500 responses.

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
- **`readDB`**: `SetMaxOpenConns(4)` — allows concurrent reads, which WAL mode supports without blocking writers. Bounded to avoid excessive file descriptors and memory.

The `Store` struct becomes:

```go
type Store struct {
    readDB  *sql.DB
    writeDB *sql.DB
}
```

`Close()` closes both. `DB()` returns `readDB` for external consumers (health checks). Migrations run against `writeDB` since they perform DDL and DML.

### Sub-Store Wiring

Each sub-store struct gains both fields:

```go
type projectStore struct {
    readDB  *sql.DB
    writeDB *sql.DB
}
```

Method routing:
- **Read methods** (Get, List, ListExpired, ListBySandbox, etc.) use `s.readDB` via `QueryContext`/`QueryRowContext`.
- **Write methods** (Create, Update, Delete) use `s.writeDB` via `ExecContext`.
- **Write-path reads** — methods that read data used to inform a subsequent write must use `writeDB` to avoid TOCTOU races. Specifically, `NextAvailablePort()` queries for the lowest unused port and its result feeds a `Create()` call; routing it through `readDB` would allow two concurrent callers to see the same available port before either writes. Route `NextAvailablePort` through `writeDB`. The UNIQUE constraint on `published_port` provides a safety net, but using `writeDB` eliminates the race.

The domain store interfaces (`domain.ProjectStore`, etc.) are unchanged.

### Error Mapping (Defense-in-Depth)

Add a `domain.ErrBusy` sentinel error. Update `mapError` to detect SQLite contention using typed error inspection (`errors.As` with `*sqlite.Error`, checking error codes 5 `SQLITE_BUSY` and 6 `SQLITE_LOCKED`) rather than string matching, which is fragile across driver versions.

As part of this change, ensure all store methods (including `Delete` methods that currently bypass `mapError`) route errors through `mapError` consistently.

Map `domain.ErrBusy` to HTTP 503 (Service Unavailable) with a `Retry-After: 1` header in the API error handler (`internal/api/response.go`). This provides a clear, retryable signal to clients if contention ever occurs.

### Files Changed

| File | Change |
|------|--------|
| `internal/store/sqlite/sqlite.go` | DSN pragma construction, dual pool setup, migrate via writeDB |
| `internal/store/sqlite/project.go` | `readDB`/`writeDB` fields, route methods, `mapError` on all paths |
| `internal/store/sqlite/sandbox.go` | Same pattern |
| `internal/store/sqlite/snapshot.go` | Same pattern |
| `internal/store/sqlite/session.go` | Same pattern |
| `internal/store/sqlite/image.go` | Same pattern |
| `internal/store/sqlite/operation.go` | Same pattern |
| `internal/store/sqlite/port.go` | Same pattern, `NextAvailablePort` via writeDB |
| `internal/domain/errors.go` | Add `ErrBusy` sentinel |
| `internal/api/response.go` | Map `ErrBusy` -> 503 with `Retry-After: 1` |
| `test/integration/concurrent_test.go` | Remove retry hack, expect clean concurrency |

### Testing

1. **Unit test** (`internal/store/sqlite/`): Concurrent goroutine writes to the store, assert zero `SQLITE_BUSY` errors. Use a temp file path (not `:memory:`) since dual `sql.Open` on `:memory:` creates two separate in-memory databases that don't share data.
2. **Integration test**: Remove `isRetryable500` retry logic from `TestConcurrentSandboxCreation` — concurrent sandbox creation works without client-side retries.
3. **Existing tests**: Any unit tests using `sqlite.Open(":memory:")` must switch to a temp file path for the same reason. Wrap in `t.TempDir()` for automatic cleanup.
4. **Existing tests**: No domain interface changes, so all service-level tests continue to work.

## Out of Scope

- Migrating away from SQLite (planned separately)
- Application-level retry middleware
- Read replica or connection routing in the domain layer
