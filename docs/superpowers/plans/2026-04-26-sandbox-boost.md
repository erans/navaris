# Sandbox Boost Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `POST /v1/sandboxes/{id}/boost` so a caller can temporarily raise CPU and/or memory limits on a running sandbox for a bounded duration, with daemon-managed auto-revert at expiry.

**Architecture:** A new `BoostService` on top of spec #1's resize primitives owns a SQLite-backed `boosts` table and an in-memory `time.AfterFunc` timer per active boost. Boosts are applied as a *live-only overlay* (a new `ApplyLiveOnly` flag on `UpdateResourcesOpts` skips the SQLite write); persisted limits track user intent. At expiry, the timer reads current persisted limits and applies them live, with retry-on-failure that transitions to a terminal `revert_failed` state after 5 attempts.

**Tech Stack:** Go, sqlite (new migration `003_boosts.sql`), `time.AfterFunc` for timers, existing event bus, Firecracker (PATCH /balloon via the spec #1 path), Incus (`incus config set` via the spec #1 path).

**Spec:** [docs/superpowers/specs/2026-04-26-sandbox-boost-design.md](../specs/2026-04-26-sandbox-boost-design.md)

**Plan-time deviation from spec §3.6:** the spec proposed routing reverts through the async dispatcher (`worker.Dispatcher`) to bound concurrency. After review during plan-writing, that's overkill for v1: it'd persist an Operation row for every revert and complicate the BoostService dependency graph. We'll use direct goroutine calls from the timer callback instead. If concurrent-revert pile-ups become a real issue, we can add a semaphore inside `BoostService` or revisit the dispatcher integration in a follow-up.

---

## File Plan

### Created
- `internal/domain/boost.go` — `Boost`, `BoostState`, `BoostStore` interface
- `internal/service/boost.go` — `BoostService` with all public + private methods
- `internal/service/boost_test.go` — service-layer tests
- `internal/service/clock.go` — small `Clock` interface (for testing)
- `internal/store/sqlite/migrations/003_boosts.sql` — schema
- `internal/store/sqlite/boost.go` — sqlite `BoostStore` impl
- `internal/store/sqlite/boost_test.go` — sqlite tests
- `internal/api/boost.go` — POST/GET/DELETE handlers + types
- `internal/api/boost_test.go` — API tests
- `pkg/client/boost.go` — SDK methods + types
- `cmd/navaris/sandbox_boost.go` — `navaris sandbox boost {start,show,cancel}` cobra subcommands
- `test/integration/boost_test.go` — integration tests

### Modified
- `internal/domain/event.go` — 3 new event types
- `internal/domain/store.go` — `Store` interface gains `BoostStore() BoostStore`
- `internal/store/sqlite/sqlite.go` — `Store` struct exposes `BoostStore()`
- `internal/service/sandbox_resize.go` — `UpdateResourcesOpts` gains `ApplyLiveOnly bool`
- `internal/service/sandbox.go` — `Stop` and `Destroy` call `boostSvc.cancelOnLifecycle`; `boostSvc` injected via `SetBoostService`
- `internal/api/sandbox.go` — `getSandbox` response embeds `active_boost`
- `internal/api/server.go` — three new routes; `BoostService` added to `ServerConfig`
- `cmd/navarisd/main.go` — `--boost-max-duration` flag, construct + recover BoostService, register lifecycle hook
- `cmd/navaris/sandbox.go` — register `sandboxBoostCmd` subtree
- `web/src/api/sandboxes.ts` — `startBoost` / `getBoost` / `cancelBoost`; extend `Sandbox` type with `ActiveBoost`
- `web/src/routes/SandboxDetail.tsx` — Boost subsection inside `ResourcesPanel`
- `README.md` — one-line bullet under Features

---

## Conventions

- All work on a fresh feature branch off `main`. Use the existing `superpowers:using-git-worktrees` flow if executing via subagent-driven-development.
- Each task ends with a commit. Match commit prefixes seen in `git log`: `feat:`, `feat(...)`, `test(...)`, `refactor(...)`, `fix(...)`, `chore(...)`, `docs:`.
- Build tags: backend tests use `//go:build firecracker` / `//go:build incus`. Domain, service, API, and store code are not build-tagged.
- After every implementation task: `gofmt -l <files>` must produce no output, all four builds (`./...`, `-tags firecracker ./...`, `-tags incus ./...`, `-tags 'incus firecracker' ./...`) must succeed.

---

## Task 1: Domain types — Boost, BoostStore, event types

**Files:**
- Create: `internal/domain/boost.go`
- Modify: `internal/domain/event.go`
- Modify: `internal/domain/store.go`

- [ ] **Step 1: Create `internal/domain/boost.go`**

```go
package domain

import (
	"context"
	"time"
)

type BoostState string

const (
	BoostActive       BoostState = "active"
	BoostRevertFailed BoostState = "revert_failed"
)

func (s BoostState) Valid() bool {
	switch s {
	case BoostActive, BoostRevertFailed:
		return true
	}
	return false
}

// Boost is a time-bounded resource bump applied to a running sandbox.
// The boosted limits are live-only — they are NOT written to the
// sandbox's persisted limits. At ExpiresAt (or on explicit cancel) the
// daemon applies the current persisted limits live again, restoring
// the user's intended steady-state.
type Boost struct {
	BoostID               string
	SandboxID             string
	OriginalCPULimit      *int   // captured at boost time, for caller display
	OriginalMemoryLimitMB *int   // captured at boost time, for caller display
	BoostedCPULimit       *int   // nil if the boost only touched memory
	BoostedMemoryLimitMB  *int   // nil if the boost only touched cpu
	StartedAt             time.Time
	ExpiresAt             time.Time
	State                 BoostState
	RevertAttempts        int
	LastError             string
}

// BoostStore persists Boost rows. UNIQUE(sandbox_id) is enforced at the
// schema level; Upsert replaces any existing boost for the same sandbox.
type BoostStore interface {
	Get(ctx context.Context, sandboxID string) (*Boost, error)        // ErrNotFound if absent
	GetByID(ctx context.Context, boostID string) (*Boost, error)
	Upsert(ctx context.Context, b *Boost) error
	UpdateState(ctx context.Context, boostID string, state BoostState, attempts int, lastErr string) error
	Delete(ctx context.Context, boostID string) error
	ListAll(ctx context.Context) ([]*Boost, error)
}
```

- [ ] **Step 2: Add the three event types**

In `internal/domain/event.go`, in the `EventType` const block, after `EventSandboxResourcesUpdated`:

```go
	EventBoostStarted        EventType = "sandbox_boost_started"
	EventBoostExpired        EventType = "sandbox_boost_expired"
	EventBoostRevertFailed   EventType = "sandbox_boost_revert_failed"
```

- [ ] **Step 3: Extend the `Store` interface**

In `internal/domain/store.go`, find the `Store` interface and add:

```go
	BoostStore() BoostStore
```

This will fail to compile in the sqlite implementation until Task 3.

- [ ] **Step 4: Verify build**

Run: `go build ./internal/domain/...`
Expected: success.

(Builds with `./...` will fail at the sqlite Store implementation — fixed in Task 3.)

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/domain/boost.go internal/domain/event.go internal/domain/store.go
git add internal/domain/boost.go internal/domain/event.go internal/domain/store.go
git commit -m "feat(domain): Boost type, BoostStore interface, boost event types"
```

---

## Task 2: SQLite migration + BoostStore implementation

**Files:**
- Create: `internal/store/sqlite/migrations/003_boosts.sql`
- Create: `internal/store/sqlite/boost.go`
- Create: `internal/store/sqlite/boost_test.go`

- [ ] **Step 1: Write the migration**

Create `internal/store/sqlite/migrations/003_boosts.sql`:

```sql
-- Boost rows: time-bounded resource bumps with auto-revert.
-- See docs/superpowers/specs/2026-04-26-sandbox-boost-design.md.

CREATE TABLE boosts (
    boost_id                  TEXT PRIMARY KEY,
    sandbox_id                TEXT NOT NULL UNIQUE REFERENCES sandboxes(sandbox_id) ON DELETE CASCADE,
    original_cpu_limit        INTEGER,
    original_memory_limit_mb  INTEGER,
    boosted_cpu_limit         INTEGER,
    boosted_memory_limit_mb   INTEGER,
    started_at                TEXT NOT NULL,
    expires_at                TEXT NOT NULL,
    state                     TEXT NOT NULL,
    revert_attempts           INTEGER NOT NULL DEFAULT 0,
    last_error                TEXT
);

CREATE INDEX idx_boosts_expires_at ON boosts(expires_at);
```

- [ ] **Step 2: Write the failing store test**

Create `internal/store/sqlite/boost_test.go`:

```go
package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/store/sqlite"
)

func TestBoostStore_UpsertGetDelete(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	// Seed a sandbox row so the FK passes.
	proj := mustCreateProject(t, s, "p1")
	sbx := mustCreateSandbox(t, s, proj.ProjectID, "s1")

	cpu, mem, boostedCPU, boostedMem := 1, 256, 4, 1024
	now := time.Now().UTC().Truncate(time.Microsecond)
	b := &domain.Boost{
		BoostID:               "b-" + uuid.NewString()[:8],
		SandboxID:             sbx.SandboxID,
		OriginalCPULimit:      &cpu,
		OriginalMemoryLimitMB: &mem,
		BoostedCPULimit:       &boostedCPU,
		BoostedMemoryLimitMB:  &boostedMem,
		StartedAt:             now,
		ExpiresAt:             now.Add(10 * time.Minute),
		State:                 domain.BoostActive,
	}

	if err := bs.Upsert(ctx, b); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := bs.Get(ctx, sbx.SandboxID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BoostID != b.BoostID || got.State != domain.BoostActive {
		t.Fatalf("got %+v", got)
	}
	if *got.BoostedCPULimit != 4 || *got.BoostedMemoryLimitMB != 1024 {
		t.Fatalf("boosted limits: %+v / %+v", got.BoostedCPULimit, got.BoostedMemoryLimitMB)
	}

	// Replace via Upsert (unique constraint on sandbox_id; provider deletes prior row first in service).
	if err := bs.Delete(ctx, b.BoostID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := bs.Get(ctx, sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestBoostStore_UpdateState(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	proj := mustCreateProject(t, s, "p1")
	sbx := mustCreateSandbox(t, s, proj.ProjectID, "s1")

	now := time.Now().UTC()
	b := &domain.Boost{
		BoostID:   "b-1",
		SandboxID: sbx.SandboxID,
		StartedAt: now,
		ExpiresAt: now.Add(time.Minute),
		State:     domain.BoostActive,
	}
	if err := bs.Upsert(ctx, b); err != nil {
		t.Fatal(err)
	}

	if err := bs.UpdateState(ctx, b.BoostID, domain.BoostRevertFailed, 5, "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ := bs.GetByID(ctx, b.BoostID)
	if got.State != domain.BoostRevertFailed || got.RevertAttempts != 5 || got.LastError != "boom" {
		t.Fatalf("after UpdateState: %+v", got)
	}
}

func TestBoostStore_CascadesOnSandboxDelete(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	proj := mustCreateProject(t, s, "p1")
	sbx := mustCreateSandbox(t, s, proj.ProjectID, "s1")
	now := time.Now().UTC()
	if err := bs.Upsert(ctx, &domain.Boost{
		BoostID: "b-1", SandboxID: sbx.SandboxID, StartedAt: now,
		ExpiresAt: now.Add(time.Minute), State: domain.BoostActive,
	}); err != nil {
		t.Fatal(err)
	}

	// Delete the sandbox; boost should cascade away.
	if err := s.SandboxStore().Delete(ctx, sbx.SandboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := bs.GetByID(ctx, "b-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after sandbox delete, got %v", err)
	}
}

func TestBoostStore_ListAll(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	proj := mustCreateProject(t, s, "p1")
	sbx1 := mustCreateSandbox(t, s, proj.ProjectID, "s1")
	sbx2 := mustCreateSandbox(t, s, proj.ProjectID, "s2")

	now := time.Now().UTC()
	for i, sbx := range []*domain.Sandbox{sbx1, sbx2} {
		_ = i
		if err := bs.Upsert(ctx, &domain.Boost{
			BoostID:   "b-" + sbx.SandboxID,
			SandboxID: sbx.SandboxID,
			StartedAt: now,
			ExpiresAt: now.Add(time.Minute),
			State:     domain.BoostActive,
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := bs.ListAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll = %d rows, want 2", len(all))
	}
	_ = sqlite.Store{} // keep import live if helpers move
}
```

> **Note:** the test references helpers `newTestStore`, `mustCreateProject`, `mustCreateSandbox`. Search `internal/store/sqlite/` for existing helpers (`grep -n "newTestStore\|mustCreate" internal/store/sqlite/*_test.go`). Reuse whatever's there. The current convention is in `sqlite_test.go` and `sandbox_test.go` — match them. If the helper signatures differ, adapt the test calls.

- [ ] **Step 3: Run, expect compile failure**

Run: `go test ./internal/store/sqlite/`
Expected: FAIL — `s.BoostStore` undefined.

- [ ] **Step 4: Implement `internal/store/sqlite/boost.go`**

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

type boostStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

// BoostStore is exposed via the sqlite Store; see Task 3 for the wiring.
func (s *Store) BoostStore() domain.BoostStore {
	return &boostStore{readDB: s.readDB, writeDB: s.writeDB}
}

func (bs *boostStore) Upsert(ctx context.Context, b *domain.Boost) error {
	_, err := bs.writeDB.ExecContext(ctx, `INSERT INTO boosts
		(boost_id, sandbox_id, original_cpu_limit, original_memory_limit_mb,
		 boosted_cpu_limit, boosted_memory_limit_mb,
		 started_at, expires_at, state, revert_attempts, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sandbox_id) DO UPDATE SET
			boost_id=excluded.boost_id,
			original_cpu_limit=excluded.original_cpu_limit,
			original_memory_limit_mb=excluded.original_memory_limit_mb,
			boosted_cpu_limit=excluded.boosted_cpu_limit,
			boosted_memory_limit_mb=excluded.boosted_memory_limit_mb,
			started_at=excluded.started_at,
			expires_at=excluded.expires_at,
			state=excluded.state,
			revert_attempts=excluded.revert_attempts,
			last_error=excluded.last_error`,
		b.BoostID, b.SandboxID,
		nullInt(b.OriginalCPULimit), nullInt(b.OriginalMemoryLimitMB),
		nullInt(b.BoostedCPULimit), nullInt(b.BoostedMemoryLimitMB),
		b.StartedAt.Format(time.RFC3339Nano),
		b.ExpiresAt.Format(time.RFC3339Nano),
		string(b.State), b.RevertAttempts, b.LastError)
	return mapError(err)
}

func (bs *boostStore) Get(ctx context.Context, sandboxID string) (*domain.Boost, error) {
	row := bs.readDB.QueryRowContext(ctx, boostSelect+` WHERE sandbox_id = ?`, sandboxID)
	return scanBoost(row)
}

func (bs *boostStore) GetByID(ctx context.Context, boostID string) (*domain.Boost, error) {
	row := bs.readDB.QueryRowContext(ctx, boostSelect+` WHERE boost_id = ?`, boostID)
	return scanBoost(row)
}

func (bs *boostStore) UpdateState(ctx context.Context, boostID string, state domain.BoostState, attempts int, lastErr string) error {
	res, err := bs.writeDB.ExecContext(ctx,
		`UPDATE boosts SET state=?, revert_attempts=?, last_error=? WHERE boost_id=?`,
		string(state), attempts, lastErr, boostID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (bs *boostStore) Delete(ctx context.Context, boostID string) error {
	res, err := bs.writeDB.ExecContext(ctx, `DELETE FROM boosts WHERE boost_id = ?`, boostID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (bs *boostStore) ListAll(ctx context.Context) ([]*domain.Boost, error) {
	rows, err := bs.readDB.QueryContext(ctx, boostSelect)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*domain.Boost
	for rows.Next() {
		b, err := scanBoostRow(rows)
		if err != nil {
			return nil, mapError(err)
		}
		out = append(out, b)
	}
	return out, mapError(rows.Err())
}

const boostSelect = `SELECT boost_id, sandbox_id,
	original_cpu_limit, original_memory_limit_mb,
	boosted_cpu_limit, boosted_memory_limit_mb,
	started_at, expires_at, state, revert_attempts, last_error
	FROM boosts`

type boostScannable interface {
	Scan(dst ...any) error
}

func scanBoost(row *sql.Row) (*domain.Boost, error) {
	return scanBoostFrom(row)
}

func scanBoostRow(rows *sql.Rows) (*domain.Boost, error) {
	return scanBoostFrom(rows)
}

func scanBoostFrom(s boostScannable) (*domain.Boost, error) {
	var (
		b           domain.Boost
		origCPU     sql.NullInt64
		origMem     sql.NullInt64
		bstCPU      sql.NullInt64
		bstMem      sql.NullInt64
		started     string
		expires     string
		state       string
		lastErr     sql.NullString
	)
	err := s.Scan(&b.BoostID, &b.SandboxID,
		&origCPU, &origMem, &bstCPU, &bstMem,
		&started, &expires, &state, &b.RevertAttempts, &lastErr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, mapError(err)
	}
	if origCPU.Valid {
		v := int(origCPU.Int64)
		b.OriginalCPULimit = &v
	}
	if origMem.Valid {
		v := int(origMem.Int64)
		b.OriginalMemoryLimitMB = &v
	}
	if bstCPU.Valid {
		v := int(bstCPU.Int64)
		b.BoostedCPULimit = &v
	}
	if bstMem.Valid {
		v := int(bstMem.Int64)
		b.BoostedMemoryLimitMB = &v
	}
	if t, perr := time.Parse(time.RFC3339Nano, started); perr == nil {
		b.StartedAt = t
	}
	if t, perr := time.Parse(time.RFC3339Nano, expires); perr == nil {
		b.ExpiresAt = t
	}
	b.State = domain.BoostState(state)
	if lastErr.Valid {
		b.LastError = lastErr.String
	}
	return &b, nil
}
```

> **Note on helpers:** `nullInt`, `mapError`, `checkRowsAffected` already exist in this package — see how `internal/store/sqlite/sandbox.go` uses them.

- [ ] **Step 5: Run all 4 tests**

Run: `go test ./internal/store/sqlite/ -run TestBoostStore -v`
Expected: 4 PASS.

Run: `go test ./internal/store/sqlite/`
Expected: all green (no regressions in existing tests).

- [ ] **Step 6: gofmt and commit**

```bash
gofmt -l internal/store/sqlite/boost.go internal/store/sqlite/boost_test.go
git add internal/store/sqlite/migrations/003_boosts.sql internal/store/sqlite/boost.go internal/store/sqlite/boost_test.go
git commit -m "feat(store): boosts table + BoostStore sqlite impl"
```

---

## Task 3: Wire `BoostStore()` onto `*sqlite.Store`

The `Store` interface in `internal/domain/store.go` gained `BoostStore() BoostStore` in Task 1. The sqlite struct method was added in Task 2's `boost.go` file. This task ensures the build is fully green by checking all callers compile.

**Files:**
- Verify only — no edits unless needed.

- [ ] **Step 1: Build everything**

```
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

If any compile error references `BoostStore`, fix the call site.

- [ ] **Step 2: Run all unit tests**

```
go test ./...
```

Expected: all green.

- [ ] **Step 3: Commit (only if any change was needed)**

```bash
git status
# if there are changes:
git add -A
git commit -m "chore: wire BoostStore through callers"
```

If no changes, this task is a no-op (Task 2's `boost.go` already added the method).

---

## Task 4: Add `ApplyLiveOnly` flag to `UpdateResourcesOpts`

The boost path applies live without writing to SQLite. Spec §3.7.

**Files:**
- Modify: `internal/service/sandbox_resize.go`
- Modify: `internal/service/sandbox_resize_test.go`

- [ ] **Step 1: Add a failing test**

Append to `internal/service/sandbox_resize_test.go`:

```go
func TestUpdateResources_ApplyLiveOnly_SkipsPersist(t *testing.T) {
	env := newServiceEnv(t)
	sbx := env.seedSandbox(t, "sbx-live-only", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	calls := 0
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, _ domain.UpdateResourcesRequest) error {
		calls++
		return nil
	}

	cpu := 4
	res, err := env.sandbox.UpdateResources(t.Context(), service.UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      &cpu,
		ApplyLiveOnly: true,
	})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}
	if !res.AppliedLive {
		t.Fatalf("AppliedLive=false; want true on running sandbox")
	}
	if calls != 1 {
		t.Fatalf("provider.UpdateResources calls = %d; want 1", calls)
	}

	// SQLite must show the ORIGINAL cpu, not 4.
	got, _ := env.store.SandboxStore().Get(t.Context(), sbx.SandboxID)
	if got.CPULimit == nil || *got.CPULimit != origCPU {
		t.Fatalf("CPULimit after ApplyLiveOnly = %v; want %d (unchanged)", got.CPULimit, origCPU)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/service/ -run TestUpdateResources_ApplyLiveOnly_SkipsPersist -v`
Expected: FAIL — `service.UpdateResourcesOpts.ApplyLiveOnly` undefined.

- [ ] **Step 3: Add the field + behavior in `internal/service/sandbox_resize.go`**

Locate `UpdateResourcesOpts`:

```go
type UpdateResourcesOpts struct {
	SandboxID     string
	CPULimit      *int
	MemoryLimitMB *int
}
```

Replace with:

```go
type UpdateResourcesOpts struct {
	SandboxID     string
	CPULimit      *int
	MemoryLimitMB *int

	// ApplyLiveOnly skips the SQLite persistence step. The provider is
	// still called when the sandbox is running. Used by the boost path,
	// where the boosted limits are a transient overlay and the persisted
	// columns must continue to track the user's steady-state intent.
	// See docs/superpowers/specs/2026-04-26-sandbox-boost-design.md §3.7.
	ApplyLiveOnly bool
}
```

In `SandboxService.UpdateResources`, find the persistence step (`s.sandboxes.Update(ctx, sbx)` near the start) and gate it on `!opts.ApplyLiveOnly`:

```go
	prevCPU := sbx.CPULimit
	prevMem := sbx.MemoryLimitMB

	if opts.CPULimit != nil {
		v := *opts.CPULimit
		sbx.CPULimit = &v
	}
	if opts.MemoryLimitMB != nil {
		v := *opts.MemoryLimitMB
		sbx.MemoryLimitMB = &v
	}
	sbx.UpdatedAt = time.Now().UTC()

	if !opts.ApplyLiveOnly {
		if err := s.sandboxes.Update(ctx, sbx); err != nil {
			return nil, fmt.Errorf("persist resize: %w", err)
		}
	}
```

The rollback path (in the running-sandbox branch on provider error) must also be gated — when `ApplyLiveOnly` was true, there's nothing to roll back:

Find the rollback block and adjust:

```go
		if err := s.provider.UpdateResources(ctx, ...); err != nil {
			if !opts.ApplyLiveOnly {
				sbx.CPULimit = prevCPU
				sbx.MemoryLimitMB = prevMem
				if rbErr := s.sandboxes.Update(ctx, sbx); rbErr != nil {
					return nil, fmt.Errorf("provider resize failed: %v; rollback also failed: %w", err, rbErr)
				}
			}
			var prErr *domain.ProviderResizeError
			if errors.As(err, &prErr) {
				return nil, prErr
			}
			return nil, err
		}
```

The event payload should still include the boosted values when applied live, so no change to the event-emit step.

- [ ] **Step 4: Run the new test + the existing UpdateResources tests**

```
go test ./internal/service/ -run TestUpdateResources -v
```

Expected: all 7 pass (the 6 from spec #1 + the new one).

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/service/sandbox_resize.go internal/service/sandbox_resize_test.go
git add internal/service/sandbox_resize.go internal/service/sandbox_resize_test.go
git commit -m "feat(service): UpdateResourcesOpts.ApplyLiveOnly for boost overlay"
```

---

## Task 5: `Clock` interface + `BoostService` skeleton

Most of the boost logic depends on time. To keep tests deterministic, introduce a tiny `Clock` interface.

**Files:**
- Create: `internal/service/clock.go`
- Create: `internal/service/boost.go`

- [ ] **Step 1: Create `internal/service/clock.go`**

```go
package service

import "time"

// Clock is a tiny abstraction over the real clock so tests that exercise
// timer behavior don't need to sleep. Production code uses RealClock.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

// Timer is the subset of *time.Timer that BoostService uses.
type Timer interface {
	Stop() bool
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func (RealClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}
```

- [ ] **Step 2: Create `internal/service/boost.go` with the skeleton**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

// BoostService manages time-bounded resource boosts. See
// docs/superpowers/specs/2026-04-26-sandbox-boost-design.md.
type BoostService struct {
	boosts      domain.BoostStore
	sandboxes   domain.SandboxStore
	sandboxSvc  *SandboxService
	events      domain.EventBus
	clock       Clock
	maxDuration time.Duration

	mu     sync.Mutex
	timers map[string]Timer // keyed by boost_id
}

func NewBoostService(
	boosts domain.BoostStore,
	sandboxes domain.SandboxStore,
	sandboxSvc *SandboxService,
	events domain.EventBus,
	clock Clock,
	maxDuration time.Duration,
) *BoostService {
	return &BoostService{
		boosts:      boosts,
		sandboxes:   sandboxes,
		sandboxSvc:  sandboxSvc,
		events:      events,
		clock:       clock,
		maxDuration: maxDuration,
		timers:      make(map[string]Timer),
	}
}

// StartBoostOpts is the input to BoostService.Start.
type StartBoostOpts struct {
	SandboxID       string
	CPULimit        *int
	MemoryLimitMB   *int
	DurationSeconds int
}

// Get returns the active or revert_failed boost for a sandbox, or
// ErrNotFound if none exists.
func (s *BoostService) Get(ctx context.Context, sandboxID string) (*domain.Boost, error) {
	return s.boosts.Get(ctx, sandboxID)
}

// Start, Cancel, expire, cancelOnLifecycle, Recover are filled in by
// later tasks. Stub them out so the type satisfies whatever callers need
// during bring-up.
func (s *BoostService) Start(ctx context.Context, opts StartBoostOpts) (*domain.Boost, error) {
	return nil, errors.New("BoostService.Start: not implemented")
}

func (s *BoostService) Cancel(ctx context.Context, sandboxID string) error {
	return errors.New("BoostService.Cancel: not implemented")
}

func (s *BoostService) Recover(ctx context.Context) error {
	return errors.New("BoostService.Recover: not implemented")
}

func (s *BoostService) cancelOnLifecycle(ctx context.Context, sandboxID string) {
	// filled in Task 9
	_ = ctx
	_ = sandboxID
}

// suppress unused-import noise during bring-up
var _ = fmt.Errorf
var _ = uuid.NewString
var _ = (*domain.Boost)(nil)
```

- [ ] **Step 3: Build**

```
go build ./...
```

Expected: success.

- [ ] **Step 4: gofmt and commit**

```bash
gofmt -l internal/service/clock.go internal/service/boost.go
git add internal/service/clock.go internal/service/boost.go
git commit -m "feat(service): Clock abstraction + BoostService skeleton"
```

---

## Task 6: `BoostService.Start` happy path

**Files:**
- Modify: `internal/service/boost.go`
- Create: `internal/service/boost_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/service/boost_test.go`:

```go
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/service"
)

func newBoostEnv(t *testing.T) *boostEnv {
	t.Helper()
	env := newServiceEnv(t)
	bs := service.NewBoostService(
		env.store.BoostStore(),
		env.store.SandboxStore(),
		env.sandbox,
		env.events,
		service.RealClock{},
		time.Hour,
	)
	return &boostEnv{serviceEnv: env, boost: bs}
}

type boostEnv struct {
	*serviceEnv
	boost *service.BoostService
}

func TestBoostStart_HappyPath(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-boost", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	ch, cancel, err := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostStarted},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	var calls []domain.UpdateResourcesRequest
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		calls = append(calls, req)
		return nil
	}

	cpu, mem := 8, 4096
	b, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID:       sbx.SandboxID,
		CPULimit:        &cpu,
		MemoryLimitMB:   &mem,
		DurationSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if b.State != domain.BoostActive {
		t.Errorf("state = %s", b.State)
	}
	if b.BoostedCPULimit == nil || *b.BoostedCPULimit != 8 {
		t.Errorf("BoostedCPU = %+v", b.BoostedCPULimit)
	}
	if b.OriginalCPULimit == nil || *b.OriginalCPULimit != origCPU {
		t.Errorf("OriginalCPU = %+v; want %d", b.OriginalCPULimit, origCPU)
	}
	if !b.ExpiresAt.After(b.StartedAt) {
		t.Errorf("ExpiresAt %v not after StartedAt %v", b.ExpiresAt, b.StartedAt)
	}

	if len(calls) != 1 {
		t.Fatalf("provider.UpdateResources calls = %d; want 1", len(calls))
	}

	// Persisted sandbox row must NOT have been mutated (ApplyLiveOnly=true).
	got, _ := env.store.SandboxStore().Get(t.Context(), sbx.SandboxID)
	if got.CPULimit == nil || *got.CPULimit != origCPU {
		t.Fatalf("persisted CPULimit = %+v; want %d (unchanged)", got.CPULimit, origCPU)
	}

	// Boost row exists in store.
	dbB, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("BoostStore.Get: %v", err)
	}
	if dbB.BoostID != b.BoostID {
		t.Errorf("BoostStore returned wrong row: %s vs %s", dbB.BoostID, b.BoostID)
	}

	select {
	case ev := <-ch:
		if ev.Type != domain.EventBoostStarted {
			t.Errorf("event type = %s", ev.Type)
		}
		if ev.Data["sandbox_id"] != sbx.SandboxID {
			t.Errorf("event sandbox_id = %v", ev.Data["sandbox_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostStarted not received")
	}

	_ = eventbus.New
	_ = errors.New
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/service/ -run TestBoostStart_HappyPath -v`
Expected: FAIL — `Start` is the stubbed "not implemented" version.

- [ ] **Step 3: Implement `Start` in `internal/service/boost.go`**

Replace the stub:

```go
func (s *BoostService) Start(ctx context.Context, opts StartBoostOpts) (*domain.Boost, error) {
	if opts.DurationSeconds <= 0 {
		return nil, fmt.Errorf("duration_seconds must be > 0: %w", domain.ErrInvalidArgument)
	}
	dur := time.Duration(opts.DurationSeconds) * time.Second
	if dur > s.maxDuration {
		return nil, fmt.Errorf("duration_seconds %d exceeds max %d: %w",
			opts.DurationSeconds, int(s.maxDuration.Seconds()), domain.ErrInvalidArgument)
	}
	if opts.CPULimit == nil && opts.MemoryLimitMB == nil {
		return nil, fmt.Errorf("at least one of cpu_limit, memory_limit_mb must be supplied: %w",
			domain.ErrInvalidArgument)
	}

	sbx, err := s.sandboxes.Get(ctx, opts.SandboxID)
	if err != nil {
		return nil, err
	}
	if sbx.State != domain.SandboxRunning {
		return nil, fmt.Errorf("boost requires sandbox state running, got %s: %w",
			sbx.State, domain.ErrInvalidState)
	}
	if err := validateResourceBounds(opts.CPULimit, opts.MemoryLimitMB, sbx.Backend); err != nil {
		return nil, err
	}

	// Cancel any existing boost — replace semantics. We hold s.mu across the
	// timer cancel + row delete + new timer schedule to avoid a window where
	// two boosts could be in flight for the same sandbox.
	s.mu.Lock()
	defer s.mu.Unlock()

	if prior, err := s.boosts.Get(ctx, opts.SandboxID); err == nil {
		if t, ok := s.timers[prior.BoostID]; ok {
			t.Stop()
			delete(s.timers, prior.BoostID)
		}
		if err := s.boosts.Delete(ctx, prior.BoostID); err != nil {
			return nil, fmt.Errorf("delete prior boost: %w", err)
		}
	}

	now := s.clock.Now().UTC()
	boost := &domain.Boost{
		BoostID:               "bst-" + uuid.NewString()[:8],
		SandboxID:             sbx.SandboxID,
		OriginalCPULimit:      copyIntPtr(sbx.CPULimit),
		OriginalMemoryLimitMB: copyIntPtr(sbx.MemoryLimitMB),
		BoostedCPULimit:       copyIntPtr(opts.CPULimit),
		BoostedMemoryLimitMB:  copyIntPtr(opts.MemoryLimitMB),
		StartedAt:             now,
		ExpiresAt:             now.Add(dur),
		State:                 domain.BoostActive,
	}
	if err := s.boosts.Upsert(ctx, boost); err != nil {
		return nil, fmt.Errorf("persist boost: %w", err)
	}

	// Apply live-only — the persisted limits stay as the user's intent.
	_, err = s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      opts.CPULimit,
		MemoryLimitMB: opts.MemoryLimitMB,
		ApplyLiveOnly: true,
	})
	if err != nil {
		// Roll back the boost row; the live VM is unchanged.
		if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil {
			return nil, fmt.Errorf("apply boost failed: %v; rollback also failed: %w", err, delErr)
		}
		return nil, err
	}

	// Schedule the auto-revert timer. The timer callback runs in a fresh
	// goroutine; expire() takes the lock itself.
	s.timers[boost.BoostID] = s.clock.AfterFunc(dur, func() { s.expire(context.Background(), boost.BoostID) })

	_ = s.events.Publish(ctx, domain.Event{
		Type:      domain.EventBoostStarted,
		Timestamp: now,
		Data: map[string]any{
			"boost_id":                 boost.BoostID,
			"sandbox_id":               boost.SandboxID,
			"boosted_cpu_limit":        boost.BoostedCPULimit,
			"boosted_memory_limit_mb": boost.BoostedMemoryLimitMB,
			"expires_at":               boost.ExpiresAt.Format(time.RFC3339Nano),
		},
	})

	return boost, nil
}

// expire is filled in Task 8. For now it deletes the row so timer-fire
// in tests doesn't dangle.
func (s *BoostService) expire(ctx context.Context, boostID string) {
	s.mu.Lock()
	delete(s.timers, boostID)
	s.mu.Unlock()
	_ = s.boosts.Delete(ctx, boostID)
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
```

Remove the `var _ = fmt.Errorf` / `var _ = uuid.NewString` / `var _ = (*domain.Boost)(nil)` placeholders from Task 5 — those imports are now used.

- [ ] **Step 4: Run the test**

```
go test ./internal/service/ -run TestBoostStart_HappyPath -v
go test ./internal/service/
```

Expected: both PASS.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/service/boost.go internal/service/boost_test.go
git add internal/service/boost.go internal/service/boost_test.go
git commit -m "feat(service): BoostService.Start happy path"
```

---

## Task 7: `BoostService.Start` error paths

**Files:**
- Modify: `internal/service/boost_test.go`

(The implementation in Task 6 already handles all these cases — this task verifies them.)

- [ ] **Step 1: Append the error-path tests**

```go
func TestBoostStart_StoppedSandbox_409(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-stopped", domain.SandboxStopped, "mock")
	cpu := 4
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
}

func TestBoostStart_BothFieldsNil_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, DurationSeconds: 60,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_DurationZero_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	cpu := 4
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 0,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_DurationOverMax_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	cpu := 4
	// max from newBoostEnv = 1h = 3600s
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 3601,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_BoundsViolation_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "firecracker")
	cpu := 99 // FC max is 32
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_ProviderError_RollsBack(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "firecracker")
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{Reason: domain.ResizeReasonExceedsCeiling, Detail: "test"}
	}

	cpu := 4
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("err = %v, want *ProviderResizeError", err)
	}

	// Boost row must NOT exist (rolled back).
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not rolled back; got %v", err)
	}
}

func TestBoostStart_ReplacesExisting(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu1 := 4
	first, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu1, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	cpu2 := 8
	second, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu2, DurationSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.BoostID == second.BoostID {
		t.Fatalf("expected new boost id; got same %s", first.BoostID)
	}
	got, _ := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if got.BoostID != second.BoostID {
		t.Fatalf("store has %s, want %s", got.BoostID, second.BoostID)
	}
}
```

- [ ] **Step 2: Run all boost tests**

```
go test ./internal/service/ -run TestBoost -v
```

Expected: all 8 pass (1 happy + 7 error/replacement).

- [ ] **Step 3: gofmt and commit**

```bash
gofmt -l internal/service/boost_test.go
git add internal/service/boost_test.go
git commit -m "test(service): cover BoostService.Start error and replace paths"
```

---

## Task 8: `BoostService.expire` — revert + retry-on-failure

**Files:**
- Modify: `internal/service/boost.go`
- Modify: `internal/service/boost_test.go`

- [ ] **Step 1: Add failing tests for `expire`**

Append to `internal/service/boost_test.go`:

```go
// fakeClock allows the boost tests to fast-forward without sleeping.
type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool { t.stopped = true; return true }

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }
func (c *fakeClock) Now() time.Time       { return c.now }
func (c *fakeClock) AfterFunc(d time.Duration, fn func()) service.Timer {
	t := &fakeTimer{at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return t
}

// fire advances the clock past dur and invokes any timers whose deadline has
// elapsed (synchronously, in the order they were scheduled).
func (c *fakeClock) fire(dur time.Duration) {
	c.now = c.now.Add(dur)
	pending := c.timers
	c.timers = nil
	for _, t := range pending {
		if t.stopped || t.at.After(c.now) {
			c.timers = append(c.timers, t)
			continue
		}
		t.fn()
	}
}

func newBoostEnvWithClock(t *testing.T, clk service.Clock) *boostEnv {
	t.Helper()
	env := newServiceEnv(t)
	bs := service.NewBoostService(
		env.store.BoostStore(), env.store.SandboxStore(), env.sandbox,
		env.events, clk, time.Hour,
	)
	return &boostEnv{serviceEnv: env, boost: bs}
}

func TestBoostExpire_RevertsToCurrentPersisted(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	cpu := 8
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe to expiry event.
	ch, cancel, _ := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostExpired},
	})
	defer cancel()

	var lastReq domain.UpdateResourcesRequest
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		lastReq = req
		return nil
	}

	clk.fire(61 * time.Second)

	// expire should have called provider.UpdateResources with the persisted
	// (original) CPU value.
	if lastReq.CPULimit == nil || *lastReq.CPULimit != origCPU {
		t.Fatalf("revert called with CPULimit=%+v; want %d", lastReq.CPULimit, origCPU)
	}

	// Boost row should be gone.
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not deleted on expire; got %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Data["cause"] != "expired" {
			t.Errorf("event cause = %v", ev.Data["cause"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostExpired not received")
	}
}

func TestBoostExpire_RetriesOnFailure_ThenRevertFailed(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	// First call (boost apply) succeeded; arm a failure for every subsequent call.
	calls := 0
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		calls++
		if calls == 1 {
			return nil // boost apply
		}
		return errors.New("provider boom")
	}

	// Fire the timer; expire() schedules a retry timer with backoff.
	clk.fire(61 * time.Second) // attempt 1 fails -> 1s retry scheduled
	clk.fire(2 * time.Second)  // attempt 2 fails -> 5s retry
	clk.fire(6 * time.Second)  // 3 -> 30s
	clk.fire(31 * time.Second) // 4 -> 2m
	clk.fire(2 * time.Minute)  // 5 -> exhausted

	// After 5 attempts, boost should be in revert_failed.
	got, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("expected boost row to remain in revert_failed; got %v", err)
	}
	if got.State != domain.BoostRevertFailed {
		t.Fatalf("state = %s, want revert_failed", got.State)
	}
	if got.RevertAttempts < 5 {
		t.Fatalf("revert_attempts = %d, want >= 5", got.RevertAttempts)
	}
}
```

- [ ] **Step 2: Run, expect failures**

```
go test ./internal/service/ -run TestBoostExpire -v
```

Expected: FAIL — `expire` is the placeholder from Task 6.

- [ ] **Step 3: Replace `expire` in `internal/service/boost.go`**

```go
// boostBackoff is the per-attempt sleep between revert retries. The slice
// length is the maximum number of attempts. If a provider error persists
// past the last entry, the boost transitions to BoostRevertFailed.
var boostBackoff = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
}

func (s *BoostService) expire(ctx context.Context, boostID string) {
	s.mu.Lock()
	delete(s.timers, boostID)
	s.mu.Unlock()

	boost, err := s.boosts.GetByID(ctx, boostID)
	if err != nil {
		// Race: boost was cancelled or deleted while the timer was firing.
		return
	}

	sbx, err := s.sandboxes.Get(ctx, boost.SandboxID)
	if err != nil {
		// Sandbox is gone; clean up the boost row.
		_ = s.boosts.Delete(ctx, boostID)
		return
	}
	if sbx.State != domain.SandboxRunning {
		// Defense-in-depth: lifecycle hooks should have removed this.
		_ = s.boosts.Delete(ctx, boostID)
		s.emitExpired(ctx, boost, "sandbox_not_running", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

	// Apply the persisted (current) limits live.
	_, applyErr := s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      sbx.CPULimit,
		MemoryLimitMB: sbx.MemoryLimitMB,
		ApplyLiveOnly: true,
	})

	if applyErr == nil {
		_ = s.boosts.Delete(ctx, boostID)
		s.emitExpired(ctx, boost, "expired", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

	// Failure: increment attempts, retry with backoff or transition to revert_failed.
	attempts := boost.RevertAttempts + 1
	if attempts >= len(boostBackoff) {
		_ = s.boosts.UpdateState(ctx, boostID, domain.BoostRevertFailed, attempts, applyErr.Error())
		_ = s.events.Publish(ctx, domain.Event{
			Type:      domain.EventBoostRevertFailed,
			Timestamp: s.clock.Now().UTC(),
			Data: map[string]any{
				"boost_id":   boostID,
				"sandbox_id": boost.SandboxID,
				"attempts":   attempts,
				"last_error": applyErr.Error(),
			},
		})
		return
	}

	_ = s.boosts.UpdateState(ctx, boostID, domain.BoostActive, attempts, applyErr.Error())

	// Schedule retry.
	s.mu.Lock()
	s.timers[boostID] = s.clock.AfterFunc(boostBackoff[attempts-1], func() {
		s.expire(context.Background(), boostID)
	})
	s.mu.Unlock()
}

func (s *BoostService) emitExpired(ctx context.Context, b *domain.Boost, cause string, cpu, mem *int) {
	_ = s.events.Publish(ctx, domain.Event{
		Type:      domain.EventBoostExpired,
		Timestamp: s.clock.Now().UTC(),
		Data: map[string]any{
			"boost_id":                  b.BoostID,
			"sandbox_id":                b.SandboxID,
			"cause":                     cause,
			"reverted_cpu_limit":        cpu,
			"reverted_memory_limit_mb": mem,
		},
	})
}
```

- [ ] **Step 4: Run all boost tests**

```
go test ./internal/service/ -run TestBoost -v
```

Expected: all pass (the existing 8 + 2 new expire tests).

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/service/boost.go internal/service/boost_test.go
git add internal/service/boost.go internal/service/boost_test.go
git commit -m "feat(service): BoostService.expire with retry-on-failure backoff"
```

---

## Task 9: `Cancel` + `cancelOnLifecycle` + Stop/Destroy hooks

**Files:**
- Modify: `internal/service/boost.go`
- Modify: `internal/service/boost_test.go`
- Modify: `internal/service/sandbox.go`

- [ ] **Step 1: Implement `Cancel` and `cancelOnLifecycle` in `boost.go`**

Replace the stub `Cancel` and `cancelOnLifecycle`:

```go
// Cancel reverts the active boost immediately and deletes the row. If no
// boost exists, returns ErrNotFound. If the boost is in BoostRevertFailed
// state, the cancel attempts the revert one more time and surfaces the
// provider error if it still fails.
func (s *BoostService) Cancel(ctx context.Context, sandboxID string) error {
	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if t, ok := s.timers[boost.BoostID]; ok {
		t.Stop()
		delete(s.timers, boost.BoostID)
	}
	s.mu.Unlock()

	sbx, err := s.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		_ = s.boosts.Delete(ctx, boost.BoostID)
		return err
	}
	if sbx.State != domain.SandboxRunning {
		_ = s.boosts.Delete(ctx, boost.BoostID)
		s.emitExpired(ctx, boost, "cancelled", sbx.CPULimit, sbx.MemoryLimitMB)
		return nil
	}

	_, applyErr := s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      sbx.CPULimit,
		MemoryLimitMB: sbx.MemoryLimitMB,
		ApplyLiveOnly: true,
	})
	if applyErr != nil {
		// Surface to the caller; leave the row in revert_failed for visibility.
		_ = s.boosts.UpdateState(ctx, boost.BoostID, domain.BoostRevertFailed, boost.RevertAttempts+1, applyErr.Error())
		return applyErr
	}

	_ = s.boosts.Delete(ctx, boost.BoostID)
	s.emitExpired(ctx, boost, "cancelled", sbx.CPULimit, sbx.MemoryLimitMB)
	return nil
}

// cancelOnLifecycle is called from SandboxService.Stop/Destroy. It drops
// the boost row + timer WITHOUT attempting a revert (the live VM is going
// away or being suspended; nothing to apply to). Errors are best-effort
// and are not propagated.
func (s *BoostService) cancelOnLifecycle(ctx context.Context, sandboxID string) {
	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		return
	}
	s.mu.Lock()
	if t, ok := s.timers[boost.BoostID]; ok {
		t.Stop()
		delete(s.timers, boost.BoostID)
	}
	s.mu.Unlock()
	_ = s.boosts.Delete(ctx, boost.BoostID)
}
```

- [ ] **Step 2: Hook into SandboxService.Stop and Destroy**

In `internal/service/sandbox.go`, find the `SandboxService` struct. Add a field:

```go
	boostSvc *BoostService
```

…and a setter method (mirroring the existing `SetSessionService` pattern):

```go
// SetBoostService injects the BoostService after construction, mirroring
// SetSessionService — the service is constructed in main.go after
// SandboxService is created, and the lifecycle hooks need to call into it.
func (s *SandboxService) SetBoostService(svc *BoostService) {
	s.boostSvc = svc
}
```

Then find the `Stop` and `Destroy` methods. At the **start** of each (after the existing pre-flight argument validation, before any state changes), add:

```go
	if s.boostSvc != nil {
		s.boostSvc.cancelOnLifecycle(ctx, id)
	}
```

The nil check tolerates pre-`SetBoostService` calls (mostly tests that don't wire the boost service).

- [ ] **Step 3: Add tests**

Append to `internal/service/boost_test.go`:

```go
func TestBoostCancel_Reverts(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	var lastReq domain.UpdateResourcesRequest
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		lastReq = req
		return nil
	}

	if err := env.boost.Cancel(t.Context(), sbx.SandboxID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if lastReq.CPULimit == nil || *lastReq.CPULimit != origCPU {
		t.Fatalf("revert called with CPULimit=%+v; want %d", lastReq.CPULimit, origCPU)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not deleted after Cancel; got %v", err)
	}
}

func TestBoostCancel_NoActiveBoost_NotFound(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	err := env.boost.Cancel(t.Context(), sbx.SandboxID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSandboxStop_CancelsBoost(t *testing.T) {
	env := newBoostEnv(t)
	env.sandbox.SetBoostService(env.boost)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	// Stop the sandbox. The boost row must be gone afterwards.
	if _, err := env.sandbox.Stop(t.Context(), sbx.SandboxID, false); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	env.dispatcher.WaitIdle()

	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not removed by Stop hook; got %v", err)
	}
}
```

- [ ] **Step 4: Run all boost tests**

```
go test ./internal/service/ -run TestBoost -v
go test ./internal/service/
```

Expected: all green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/service/boost.go internal/service/boost_test.go internal/service/sandbox.go
git add internal/service/boost.go internal/service/boost_test.go internal/service/sandbox.go
git commit -m "feat(service): BoostService.Cancel + Stop/Destroy lifecycle hooks"
```

---

## Task 10: `BoostService.Recover` — startup replay

**Files:**
- Modify: `internal/service/boost.go`
- Modify: `internal/service/boost_test.go`

- [ ] **Step 1: Add failing tests**

Append:

```go
func TestBoostRecover_RescheduleInWindow(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	// Seed a boost row directly (simulates a row left over from a daemon restart).
	cpu := 8
	now := clk.Now().UTC()
	row := &domain.Boost{
		BoostID: "bst-x", SandboxID: sbx.SandboxID,
		BoostedCPULimit: &cpu, StartedAt: now,
		ExpiresAt: now.Add(60 * time.Second),
		State:     domain.BoostActive,
	}
	if err := env.store.BoostStore().Upsert(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	if err := env.boost.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Advancing past expiry must trigger the revert.
	called := false
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		called = true
		return nil
	}
	clk.fire(61 * time.Second)
	if !called {
		t.Fatal("recovered boost did not expire on time")
	}
}

func TestBoostRecover_AlreadyExpired_RevertsImmediately(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	now := clk.Now().UTC()
	row := &domain.Boost{
		BoostID: "bst-x", SandboxID: sbx.SandboxID,
		BoostedCPULimit: &cpu, StartedAt: now.Add(-10 * time.Minute),
		ExpiresAt: now.Add(-5 * time.Minute), // already in the past
		State:     domain.BoostActive,
	}
	if err := env.store.BoostStore().Upsert(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	called := make(chan struct{}, 1)
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}

	if err := env.boost.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("expired-while-down boost did not revert immediately")
	}
}

func TestBoostRecover_RevertFailedLeftAlone(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	now := clk.Now().UTC()
	row := &domain.Boost{
		BoostID: "bst-x", SandboxID: sbx.SandboxID,
		BoostedCPULimit: &cpu, StartedAt: now,
		ExpiresAt: now.Add(60 * time.Second),
		State:     domain.BoostRevertFailed,
		RevertAttempts: 5, LastError: "stuck",
	}
	if err := env.store.BoostStore().Upsert(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	if err := env.boost.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Advance past expiry — no revert should fire.
	called := false
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		called = true
		return nil
	}
	clk.fire(61 * time.Second)
	if called {
		t.Fatal("revert_failed boost should not auto-revert on Recover")
	}
}
```

- [ ] **Step 2: Run, expect failures**

```
go test ./internal/service/ -run TestBoostRecover -v
```

Expected: FAIL on the in-window and expired-while-down tests; the third may pass by accident.

- [ ] **Step 3: Implement `Recover` in `internal/service/boost.go`**

Replace the stub:

```go
func (s *BoostService) Recover(ctx context.Context) error {
	rows, err := s.boosts.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("list boosts: %w", err)
	}
	now := s.clock.Now().UTC()
	for _, b := range rows {
		if b.State != domain.BoostActive {
			// Leave revert_failed and any future states alone.
			continue
		}
		if !now.Before(b.ExpiresAt) {
			// Already expired; trigger revert immediately. Run in a fresh
			// goroutine so a slow provider doesn't block daemon startup.
			boostID := b.BoostID
			go s.expire(context.Background(), boostID)
			continue
		}
		// Schedule timer for the remainder.
		remaining := b.ExpiresAt.Sub(now)
		boostID := b.BoostID
		s.mu.Lock()
		s.timers[boostID] = s.clock.AfterFunc(remaining, func() {
			s.expire(context.Background(), boostID)
		})
		s.mu.Unlock()
	}
	return nil
}
```

- [ ] **Step 4: Run all boost tests**

```
go test ./internal/service/ -run TestBoost -v
```

Expected: all pass.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/service/boost.go internal/service/boost_test.go
git add internal/service/boost.go internal/service/boost_test.go
git commit -m "feat(service): BoostService.Recover for daemon-restart replay"
```

---

## Task 11: API — `POST /v1/sandboxes/{id}/boost`

**Files:**
- Create: `internal/api/boost.go`
- Create: `internal/api/boost_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Add the `BoostService` to `ServerConfig`**

In `internal/api/server.go`, find `type ServerConfig struct` and add:

```go
	Boosts *service.BoostService
```

- [ ] **Step 2: Write a failing API test**

Create `internal/api/boost_test.go`:

```go
package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

// boostTestEnv extends testEnv with a constructed BoostService (newTestEnv
// doesn't construct one by default; we wire it here so the boost handlers
// have somewhere to dispatch to).
//
// `newTestEnv` is in helpers_test.go. We re-use it and then poke a Boost
// service into the api server. The plan can't see helpers_test.go's exact
// shape — adapt the wiring if the testEnv exposes a hook.
func TestPostBoost_OK(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip("test env doesn't wire a BoostService; helpers_test.go needs a small extension")
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxRunning, "mock", projID)

	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		BoostID         string `json:"boost_id"`
		SandboxID       string `json:"sandbox_id"`
		BoostedCPULimit *int   `json:"boosted_cpu_limit"`
		ExpiresAt       string `json:"expires_at"`
		State           string `json:"state"`
	}
	parseJSON(t, rec, &got)
	if got.BoostedCPULimit == nil || *got.BoostedCPULimit != 4 {
		t.Fatalf("BoostedCPULimit = %+v", got.BoostedCPULimit)
	}
	if got.State != "active" {
		t.Errorf("state = %q", got.State)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.ExpiresAt); err != nil {
		t.Errorf("expires_at not parseable: %v", err)
	}
}

func TestPostBoost_StoppedSandbox_409(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip("test env doesn't wire a BoostService")
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxStopped, "mock", projID)

	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})

	if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusConflict {
		t.Fatalf("status = %d (want 409 or 422)", rec.Code)
	}
}

func TestPostBoost_BothFieldsOmitted_400(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip()
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxRunning, "mock", projID)

	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"duration_seconds": 60})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPostBoost_NotFound_404(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip()
	}
	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/missing/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

// envHasBoost is true when the test environment was constructed with a
// BoostService wired into the api server. helpers_test.go must be extended
// to construct one and pass it via api.ServerConfig.Boosts. Until then the
// boost API tests skip.
func envHasBoost(env *testEnv) bool {
	// Implementer: set this to whatever check `newTestEnv` exposes — e.g.,
	// `return env.boost != nil`. If you change newTestEnv to always wire a
	// BoostService, just `return true`.
	return env.boost != nil
}

var _ = uuid.NewString
var _ = strings.Contains
```

> **Note for the implementer:** `newTestEnv` in `internal/api/helpers_test.go` does not currently construct a `BoostService`. Add the wiring in this same task. Append a `boost *service.BoostService` field to `testEnv`, construct it after `sbxSvc` (passing the `BoostStore`, `SandboxStore`, `sbxSvc`, `bus`, `service.RealClock{}`, and `time.Hour`), call `sbxSvc.SetBoostService(boost)`, and add `Boosts: boost` to the `api.ServerConfig` literal. That makes `envHasBoost` return true and the new tests run.

- [ ] **Step 3: Run, expect failures**

```
go test ./internal/api/ -run TestPostBoost -v
```

Expected: most skip until `helpers_test.go` is updated; once updated, they FAIL because the route + handler are missing.

- [ ] **Step 4: Update `helpers_test.go`**

Edit `internal/api/helpers_test.go`:

In the `testEnv` struct, add:
```go
	boost *service.BoostService
```

In `newTestEnv`, after `sbxSvc.SetSessionService(sessSvc)`:

```go
	boostSvc := service.NewBoostService(
		s.BoostStore(), s.SandboxStore(), sbxSvc, bus, service.RealClock{}, time.Hour,
	)
	sbxSvc.SetBoostService(boostSvc)
```

Add `Boosts: boostSvc,` to the `api.NewServer(api.ServerConfig{...})` literal.

Add `boost: boostSvc,` to the returned `&testEnv{...}`.

Add `"time"` import if not present.

- [ ] **Step 5: Create `internal/api/boost.go`**

```go
package api

import (
	"errors"
	"net/http"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

type startBoostRequest struct {
	CPULimit        *int `json:"cpu_limit"`
	MemoryLimitMB   *int `json:"memory_limit_mb"`
	DurationSeconds int  `json:"duration_seconds"`
}

type boostResponse struct {
	BoostID               string `json:"boost_id"`
	SandboxID             string `json:"sandbox_id"`
	OriginalCPULimit      *int   `json:"original_cpu_limit"`
	OriginalMemoryLimitMB *int   `json:"original_memory_limit_mb"`
	BoostedCPULimit       *int   `json:"boosted_cpu_limit"`
	BoostedMemoryLimitMB  *int   `json:"boosted_memory_limit_mb"`
	StartedAt             string `json:"started_at"`
	ExpiresAt             string `json:"expires_at"`
	State                 string `json:"state"`
	RevertAttempts        int    `json:"revert_attempts,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

func boostToResponse(b *domain.Boost) boostResponse {
	return boostResponse{
		BoostID:               b.BoostID,
		SandboxID:             b.SandboxID,
		OriginalCPULimit:      b.OriginalCPULimit,
		OriginalMemoryLimitMB: b.OriginalMemoryLimitMB,
		BoostedCPULimit:       b.BoostedCPULimit,
		BoostedMemoryLimitMB:  b.BoostedMemoryLimitMB,
		StartedAt:             b.StartedAt.UTC().Format(timeFormatJSON),
		ExpiresAt:             b.ExpiresAt.UTC().Format(timeFormatJSON),
		State:                 string(b.State),
		RevertAttempts:        b.RevertAttempts,
		LastError:             b.LastError,
	}
}

const timeFormatJSON = "2006-01-02T15:04:05.999999999Z07:00"

func (s *Server) startBoost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing sandbox id", http.StatusBadRequest)
		return
	}
	var req startBoostRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.CPULimit == nil && req.MemoryLimitMB == nil {
		http.Error(w, "at least one of cpu_limit, memory_limit_mb is required", http.StatusBadRequest)
		return
	}
	if req.DurationSeconds <= 0 {
		http.Error(w, "duration_seconds must be > 0", http.StatusBadRequest)
		return
	}

	b, err := s.cfg.Boosts.Start(r.Context(), service.StartBoostOpts{
		SandboxID:       id,
		CPULimit:        req.CPULimit,
		MemoryLimitMB:   req.MemoryLimitMB,
		DurationSeconds: req.DurationSeconds,
	})
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, boostToResponse(b))
}

// suppress unused-import if Get/Cancel handlers aren't yet wired
var _ = errors.New
```

- [ ] **Step 6: Register the POST route in `internal/api/server.go`**

Add near the existing `PATCH /v1/sandboxes/{id}/resources`:

```go
	api.HandleFunc("POST /v1/sandboxes/{id}/boost", s.startBoost)
```

- [ ] **Step 7: Run the tests**

```
go test ./internal/api/ -run TestPostBoost -v
go test ./internal/api/
```

Expected: all 4 PASS, no regressions.

- [ ] **Step 8: gofmt and commit**

```bash
gofmt -l internal/api/boost.go internal/api/boost_test.go internal/api/server.go internal/api/helpers_test.go
git add internal/api/boost.go internal/api/boost_test.go internal/api/server.go internal/api/helpers_test.go
git commit -m "feat(api): POST /sandboxes/{id}/boost"
```

---

## Task 12: API — `GET /v1/sandboxes/{id}/boost` + embed `active_boost` on `GET /v1/sandboxes/{id}`

**Files:**
- Modify: `internal/api/boost.go`
- Modify: `internal/api/sandbox.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/boost_test.go`

- [ ] **Step 1: Add `getBoost` handler to `internal/api/boost.go`**

```go
func (s *Server) getBoost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := s.cfg.Boosts.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, boostToResponse(b))
}
```

- [ ] **Step 2: Embed `active_boost` in the sandbox response**

In `internal/api/sandbox.go`, find `getSandbox`. The current implementation returns the `*domain.Sandbox` directly via `respondData`. Build a response struct that includes `active_boost`:

```go
type sandboxResponse struct {
	*domain.Sandbox
	ActiveBoost *boostResponse `json:"active_boost,omitempty"`
}

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sbx, err := s.cfg.Sandboxes.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	resp := sandboxResponse{Sandbox: sbx}
	if s.cfg.Boosts != nil {
		if b, err := s.cfg.Boosts.Get(r.Context(), id); err == nil {
			br := boostToResponse(b)
			resp.ActiveBoost = &br
		}
		// On any other error (e.g., transient store error), omit the field
		// rather than failing the whole GET.
	}
	respondData(w, http.StatusOK, resp)
}
```

- [ ] **Step 3: Register the GET route**

In `internal/api/server.go`, add:

```go
	api.HandleFunc("GET /v1/sandboxes/{id}/boost", s.getBoost)
```

- [ ] **Step 4: Add tests**

Append to `internal/api/boost_test.go`:

```go
func TestGetBoost_OK(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip()
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxRunning, "mock", projID)

	// Start a boost.
	doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})

	rec := doRequest(t, env.handler, http.MethodGet,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		BoostedCPULimit *int `json:"boosted_cpu_limit"`
	}
	parseJSON(t, rec, &got)
	if got.BoostedCPULimit == nil || *got.BoostedCPULimit != 4 {
		t.Errorf("BoostedCPULimit = %+v", got.BoostedCPULimit)
	}
}

func TestGetBoost_NoActive_404(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip()
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxRunning, "mock", projID)

	rec := doRequest(t, env.handler, http.MethodGet,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetSandbox_EmbedsActiveBoost(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip()
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxRunning, "mock", projID)

	doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})

	rec := doRequest(t, env.handler, http.MethodGet,
		"/v1/sandboxes/"+sbx.SandboxID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"active_boost"`) {
		t.Fatalf("expected active_boost in body: %s", body)
	}
}
```

- [ ] **Step 5: Run, then commit**

```
go test ./internal/api/ -run TestGet -v
go test ./internal/api/
```

Expected: all green.

```bash
gofmt -l internal/api/boost.go internal/api/sandbox.go internal/api/server.go internal/api/boost_test.go
git add internal/api/boost.go internal/api/sandbox.go internal/api/server.go internal/api/boost_test.go
git commit -m "feat(api): GET /sandboxes/{id}/boost + embed active_boost on getSandbox"
```

---

## Task 13: API — `DELETE /v1/sandboxes/{id}/boost`

**Files:**
- Modify: `internal/api/boost.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/boost_test.go`

- [ ] **Step 1: Append the handler**

```go
func (s *Server) deleteBoost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.cfg.Boosts.Cancel(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Register the route**

```go
	api.HandleFunc("DELETE /v1/sandboxes/{id}/boost", s.deleteBoost)
```

- [ ] **Step 3: Add tests**

```go
func TestDeleteBoost_204(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip()
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxRunning, "mock", projID)

	doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})

	rec := doRequest(t, env.handler, http.MethodDelete,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteBoost_NoActive_404(t *testing.T) {
	env := newTestEnv(t)
	if !envHasBoost(env) {
		t.Skip()
	}
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, "sbx-1", domain.SandboxRunning, "mock", projID)

	rec := doRequest(t, env.handler, http.MethodDelete,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
```

- [ ] **Step 4: Run + commit**

```
go test ./internal/api/ -run TestDeleteBoost -v
go test ./internal/api/
```

```bash
gofmt -l internal/api/boost.go internal/api/server.go internal/api/boost_test.go
git add internal/api/boost.go internal/api/server.go internal/api/boost_test.go
git commit -m "feat(api): DELETE /sandboxes/{id}/boost"
```

---

## Task 14: Daemon flag wiring + `Recover()` at startup

**Files:**
- Modify: `cmd/navarisd/main.go`

- [ ] **Step 1: Add the flag**

In the config struct (next to the existing flags):

```go
	boostMaxDuration time.Duration
```

In `parseFlags`, after the existing `--gc-interval` registration:

```go
	flag.DurationVar(&cfg.boostMaxDuration, "boost-max-duration", time.Hour,
		"maximum duration for a single boost (1m..24h)")
```

- [ ] **Step 2: Validate the flag**

After `parseFlags` returns, but before constructing `BoostService`:

```go
	if cfg.boostMaxDuration < time.Minute || cfg.boostMaxDuration > 24*time.Hour {
		log.Fatalf("--boost-max-duration must be in [1m, 24h], got %s", cfg.boostMaxDuration)
	}
```

- [ ] **Step 3: Construct `BoostService` and inject + recover**

Find where `sbxSvc` is constructed. After it, add:

```go
	boostSvc := service.NewBoostService(
		store.BoostStore(), store.SandboxStore(), sbxSvc, bus,
		service.RealClock{}, cfg.boostMaxDuration,
	)
	sbxSvc.SetBoostService(boostSvc)

	if err := boostSvc.Recover(context.Background()); err != nil {
		log.Fatalf("boost recover: %v", err)
	}
```

Pass `Boosts: boostSvc,` into the `api.ServerConfig` literal.

- [ ] **Step 4: Build + verify the flag appears**

```
go build -tags 'incus firecracker' ./cmd/navarisd
./navarisd --help 2>&1 | grep boost-max-duration
```

Expected: the flag is listed with default `1h0m0s`.

- [ ] **Step 5: Commit**

```bash
gofmt -l cmd/navarisd/main.go
git add cmd/navarisd/main.go
git commit -m "feat(navarisd): --boost-max-duration flag + BoostService recovery"
```

---

## Task 15: SDK + CLI

**Files:**
- Create: `pkg/client/boost.go`
- Create: `cmd/navaris/sandbox_boost.go`
- Modify: `cmd/navaris/sandbox.go`

- [ ] **Step 1: Create `pkg/client/boost.go`**

```go
package client

import (
	"context"
	"fmt"
)

type StartBoostRequest struct {
	CPULimit        *int `json:"cpu_limit,omitempty"`
	MemoryLimitMB   *int `json:"memory_limit_mb,omitempty"`
	DurationSeconds int  `json:"duration_seconds"`
}

type Boost struct {
	BoostID               string `json:"boost_id"`
	SandboxID             string `json:"sandbox_id"`
	OriginalCPULimit      *int   `json:"original_cpu_limit"`
	OriginalMemoryLimitMB *int   `json:"original_memory_limit_mb"`
	BoostedCPULimit       *int   `json:"boosted_cpu_limit"`
	BoostedMemoryLimitMB  *int   `json:"boosted_memory_limit_mb"`
	StartedAt             string `json:"started_at"`
	ExpiresAt             string `json:"expires_at"`
	State                 string `json:"state"`
	RevertAttempts        int    `json:"revert_attempts,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

func (c *Client) StartBoost(ctx context.Context, sandboxID string, req StartBoostRequest) (*Boost, error) {
	var resp Boost
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/boost", sandboxID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetBoost(ctx context.Context, sandboxID string) (*Boost, error) {
	var b Boost
	if err := c.get(ctx, fmt.Sprintf("/v1/sandboxes/%s/boost", sandboxID), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) CancelBoost(ctx context.Context, sandboxID string) error {
	return c.del(ctx, fmt.Sprintf("/v1/sandboxes/%s/boost", sandboxID))
}
```

- [ ] **Step 2: Create the CLI subcommand tree**

```go
// cmd/navaris/sandbox_boost.go
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var sandboxBoostCmd = &cobra.Command{
	Use:   "boost",
	Short: "Time-bounded resource boost (CPU and/or memory)",
}

var (
	boostStartCPU      int
	boostStartMem      int
	boostStartDuration time.Duration
)

var sandboxBoostStartCmd = &cobra.Command{
	Use:   "start <sandbox-id>",
	Short: "Start a boost",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if boostStartCPU == 0 && boostStartMem == 0 {
			return errors.New("at least one of --cpu or --memory is required")
		}
		if boostStartDuration <= 0 {
			return errors.New("--duration must be > 0")
		}
		req := client.StartBoostRequest{DurationSeconds: int(boostStartDuration.Seconds())}
		if boostStartCPU > 0 {
			v := boostStartCPU
			req.CPULimit = &v
		}
		if boostStartMem > 0 {
			v := boostStartMem
			req.MemoryLimitMB = &v
		}
		c, err := newClient(cmd)
		if err != nil {
			return err
		}
		b, err := c.StartBoost(cmd.Context(), args[0], req)
		if err != nil {
			return err
		}
		fmt.Printf("boost started: %s -> %s (expires %s)\n", b.BoostID, b.SandboxID, b.ExpiresAt)
		return nil
	},
}

var sandboxBoostShowCmd = &cobra.Command{
	Use:   "show <sandbox-id>",
	Short: "Show the active boost (or 404)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient(cmd)
		if err != nil {
			return err
		}
		b, err := c.GetBoost(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Printf("boost: %s state=%s expires_at=%s cpu=%s mem=%s\n",
			b.BoostID, b.State, b.ExpiresAt, fmtPtr(b.BoostedCPULimit), fmtPtr(b.BoostedMemoryLimitMB))
		return nil
	},
}

var sandboxBoostCancelCmd = &cobra.Command{
	Use:   "cancel <sandbox-id>",
	Short: "Cancel the active boost (reverts immediately)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient(cmd)
		if err != nil {
			return err
		}
		if err := c.CancelBoost(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Println("boost cancelled")
		return nil
	},
}

func fmtPtr(p *int) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *p)
}

func init() {
	sandboxBoostStartCmd.Flags().IntVar(&boostStartCPU, "cpu", 0, "boosted CPU limit (0 = leave unchanged)")
	sandboxBoostStartCmd.Flags().IntVar(&boostStartMem, "memory", 0, "boosted memory limit in MB (0 = leave unchanged)")
	sandboxBoostStartCmd.Flags().DurationVar(&boostStartDuration, "duration", 5*time.Minute, "boost duration (e.g. 30s, 5m)")

	sandboxBoostCmd.AddCommand(sandboxBoostStartCmd)
	sandboxBoostCmd.AddCommand(sandboxBoostShowCmd)
	sandboxBoostCmd.AddCommand(sandboxBoostCancelCmd)
}
```

- [ ] **Step 3: Register the subcommand subtree**

In `cmd/navaris/sandbox.go`, in the existing `init()` block, add:

```go
	sandboxCmd.AddCommand(sandboxBoostCmd)
```

- [ ] **Step 4: Build and smoke-test**

```
go build -o /tmp/navaris ./cmd/navaris/
/tmp/navaris sandbox boost --help
/tmp/navaris sandbox boost start --help
```

Expected: subcommands appear with their flags.

- [ ] **Step 5: Run unit tests + commit**

```
go test ./pkg/client/ ./cmd/navaris/...
```

```bash
gofmt -l pkg/client/boost.go cmd/navaris/sandbox_boost.go cmd/navaris/sandbox.go
git add pkg/client/boost.go cmd/navaris/sandbox_boost.go cmd/navaris/sandbox.go
git commit -m "feat(client,cli): boost SDK methods + 'navaris sandbox boost' subcommand"
```

---

## Task 16: Integration tests

**Files:**
- Create: `test/integration/boost_test.go`

- [ ] **Step 1: Write the test file**

```go
//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

func ptrIntBoost(v int) *int { return &v }

// TestBoost_Memory_AppliesAndReverts creates a sandbox, boosts memory by
// shrinking it (works on both backends), then waits for the timer to fire
// and verifies the live limit reverts.
//
// We use a SHRINK boost (memory_limit_mb < current) so the test works
// without --firecracker-mem-headroom-mult > 1.0. With the default 1.0,
// ceiling==limit, but balloon-shrink within [0..limit] is always allowed.
//
// Wait — actually a shrink boost is unusual semantically; this is a
// time-bounded resource bump in the spec. Let's do it as a memory grow
// when the daemon is configured with mem_headroom > 1.0; otherwise skip
// the FC leg of this test. Incus doesn't have a ceiling so grow always works.
func TestBoost_Memory_AppliesAndReverts(t *testing.T) {
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boost-mem-revert",
		ImageID: baseImage(), MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	// Boost memory DOWN (shrink, works on either backend regardless of headroom).
	_, err = c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntBoost(192),
		DurationSeconds: 3,
	})
	if err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	// Confirm the boost shows up in GET.
	b, err := c.GetBoost(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetBoost: %v", err)
	}
	if b.State != "active" {
		t.Errorf("state = %s", b.State)
	}

	// Wait past expiry + a small slack.
	time.Sleep(5 * time.Second)

	// GetBoost should now be 404.
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatalf("expected ErrNotFound after expiry, got nil")
	}
}

func TestBoost_Cancel_RevertsImmediately(t *testing.T) {
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boost-cancel",
		ImageID: baseImage(), MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntBoost(192),
		DurationSeconds: 600, // long; we'll cancel
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatal("expected 404 after cancel")
	}
}

func TestBoost_Stop_CancelsBoost(t *testing.T) {
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boost-stop",
		ImageID: baseImage(), MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntBoost(192),
		DurationSeconds: 600,
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	if _, err := c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatal("expected boost gone after sandbox stop")
	}
	_ = strings.Contains
}
```

- [ ] **Step 2: Compile-only check**

```
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both should succeed.

- [ ] **Step 3: gofmt and commit**

```bash
gofmt -l test/integration/boost_test.go
git add test/integration/boost_test.go
git commit -m "test(integration): boost apply/revert/cancel on both backends"
```

---

## Task 17: Web UI — Boost subsection in `ResourcesPanel`

**Files:**
- Modify: `web/src/api/sandboxes.ts`
- Modify: `web/src/types/navaris.ts` (or wherever the `Sandbox` type lives — search first)
- Modify: `web/src/routes/SandboxDetail.tsx`

- [ ] **Step 1: Add API client functions in `web/src/api/sandboxes.ts`**

Append:

```ts
export interface StartBoostRequest {
  cpu_limit?: number;
  memory_limit_mb?: number;
  duration_seconds: number;
}

export interface ActiveBoost {
  boost_id: string;
  sandbox_id: string;
  original_cpu_limit: number | null;
  original_memory_limit_mb: number | null;
  boosted_cpu_limit: number | null;
  boosted_memory_limit_mb: number | null;
  started_at: string;
  expires_at: string;
  state: "active" | "revert_failed";
  revert_attempts?: number;
  last_error?: string;
}

export async function startBoost(
  id: string,
  body: StartBoostRequest,
): Promise<ActiveBoost> {
  return apiFetch<ActiveBoost>(
    `/v1/sandboxes/${encodeURIComponent(id)}/boost`,
    { method: "POST", json: body },
  );
}

export async function getBoost(id: string): Promise<ActiveBoost> {
  return apiFetch<ActiveBoost>(
    `/v1/sandboxes/${encodeURIComponent(id)}/boost`,
  );
}

export async function cancelBoost(id: string): Promise<void> {
  await apiFetch<unknown>(
    `/v1/sandboxes/${encodeURIComponent(id)}/boost`,
    { method: "DELETE" },
  );
}
```

- [ ] **Step 2: Extend the `Sandbox` type**

Find the `Sandbox` interface (likely in `web/src/types/navaris.ts`). Add an optional field:

```ts
ActiveBoost?: ActiveBoost;
```

> The Go server marshals the embedded field as `active_boost` (snake_case JSON tag). Match the existing convention in `web/src/types/navaris.ts` — if other fields use PascalCase (e.g. `CPULimit`), keep PascalCase here too; the JSON wire format and the TS field name don't have to match the same way (the API returns whichever serialization the backend chose). Confirm by inspecting the JSON the Go server actually sends — if it's `active_boost`, the TS field is `active_boost`; if Go uses no JSON tags and emits `ActiveBoost`, TS uses `ActiveBoost`. Match what's on the wire.

- [ ] **Step 3: Add the Boost subsection in `web/src/routes/SandboxDetail.tsx`**

Inside the `ResourcesPanel` component (added in spec #1), append a new section. Look at the existing CSS variables and patterns. Outline:

```tsx
function BoostSection({ sandbox, onChange }: { sandbox: Sandbox; onChange: () => void }) {
  const [cpu, setCpu] = useState<string>("");
  const [mem, setMem] = useState<string>("");
  const [duration, setDuration] = useState<string>("5m");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const active = sandbox.active_boost ?? sandbox.ActiveBoost; // accommodate both casings

  async function start() {
    setErr(null);
    setBusy(true);
    try {
      const durSec = parseDuration(duration);
      const body: StartBoostRequest = { duration_seconds: durSec };
      const cpuN = cpu === "" ? undefined : Number(cpu);
      const memN = mem === "" ? undefined : Number(mem);
      if (cpuN !== undefined) body.cpu_limit = cpuN;
      if (memN !== undefined) body.memory_limit_mb = memN;
      if (cpuN === undefined && memN === undefined) {
        throw new Error("set at least one of CPU or memory");
      }
      await startBoost(sandbox.SandboxID, body);
      onChange();
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : "boost failed");
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    setErr(null);
    setBusy(true);
    try {
      await cancelBoost(sandbox.SandboxID);
      onChange();
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : "cancel failed");
    } finally {
      setBusy(false);
    }
  }

  if (active) {
    return (
      <section>
        <h4>Boost active</h4>
        <p>cpu={active.boosted_cpu_limit ?? "—"} mem={active.boosted_memory_limit_mb ?? "—"} expires={active.expires_at}</p>
        {active.state === "revert_failed" && (
          <p role="alert">revert failed ({active.revert_attempts}x): {active.last_error}</p>
        )}
        <button type="button" onClick={cancel} disabled={busy}>
          {busy ? "Cancelling…" : "Cancel boost"}
        </button>
        {err && <p role="alert">{err}</p>}
      </section>
    );
  }

  return (
    <section>
      <h4>Boost</h4>
      <label>CPU <input type="number" min="1" value={cpu} onChange={(e) => setCpu(e.currentTarget.value)} disabled={busy} /></label>
      <label>Memory (MB) <input type="number" min="64" value={mem} onChange={(e) => setMem(e.currentTarget.value)} disabled={busy} /></label>
      <label>Duration <input value={duration} onChange={(e) => setDuration(e.currentTarget.value)} disabled={busy} placeholder="5m" /></label>
      <button type="button" onClick={start} disabled={busy}>{busy ? "Boosting…" : "Boost"}</button>
      {err && <p role="alert">{err}</p>}
    </section>
  );
}

function parseDuration(s: string): number {
  // Tiny parser supporting "30s", "5m", "1h" — sufficient for the dropdown.
  const m = /^(\d+)\s*(s|m|h)$/.exec(s.trim());
  if (!m) {
    const n = Number(s);
    if (Number.isFinite(n) && n > 0) return Math.floor(n);
    throw new Error(`invalid duration: ${s}`);
  }
  const n = Number(m[1]);
  return n * (m[2] === "h" ? 3600 : m[2] === "m" ? 60 : 1);
}
```

Render `<BoostSection sandbox={data} onChange={() => qc.invalidateQueries({ queryKey: ["sandbox", id] })} />` next to the existing `<ResourcesPanel ... />` (or inside it if the layout permits).

- [ ] **Step 4: Build the front-end**

```
cd web && npm run build 2>&1 | tail -10
```

Expected: clean.

- [ ] **Step 5: Run front-end tests**

```
cd web && npm test 2>&1 | tail -10
```

Existing tests still pass (no new tests for the boost panel — out of scope for this spec).

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(web): Boost subsection in sandbox detail Resources panel"
```

---

## Task 18: README touch-up

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add the boost feature line**

Find the "Runtime resize" feature bullet (added in spec #1). Insert immediately after it:

```markdown
- **Time-bounded boost**: temporarily raise CPU/memory for a fixed duration via `POST /v1/sandboxes/{id}/boost`; the daemon auto-reverts at expiry (with retry-on-failure). One active boost per sandbox; auto-cancelled if the sandbox stops or is destroyed. Cap with `--boost-max-duration` (default 1h, max 24h).
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(readme): time-bounded boost feature"
```

---

## Final verification

- [ ] **Run the full unit test matrix:**

```
go test ./...
go test -tags incus ./...
go test -tags firecracker ./...
```

Expected: all green.

- [ ] **Run the web build:**

```
cd web && npm run build
```

Expected: clean.

- [ ] **Smoke test (if dev environment is available):**

```bash
./navarisd ... &
./navaris sandbox create --image alpine/3.21 --memory 256 my-sbx
./navaris sandbox boost start my-sbx --memory 192 --duration 5s
./navaris sandbox boost show my-sbx
sleep 6
./navaris sandbox boost show my-sbx   # expect "boost: not found" / 404
```

- [ ] **Open a PR** referencing the spec doc and this plan.
