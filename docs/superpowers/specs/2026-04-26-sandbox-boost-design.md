# Sandbox Boost Design (Time-bounded Resource Bump with Auto-revert)

**Status:** Draft
**Date:** 2026-04-26
**Scope:** Add `POST /v1/sandboxes/{id}/boost` so a caller can temporarily raise CPU and/or memory limits on a running sandbox for a bounded duration, with automatic revert when the timer fires. This is **spec #2 of three** (spec #1 shipped runtime resize via `PATCH /v1/sandboxes/{id}/resources`; spec #3 will add an in-sandbox channel that lets guest code request a boost back through the daemon). All boost behavior in this spec is initiated externally via the daemon's REST API.

## 1. Goals

Spec #1 made resize a sync, persistent operation. Common workloads need a different shape: "give this sandbox 4× CPU for 5 minutes while it runs a one-off task, then drop back to normal." Asking the user to PATCH up, run a timer, PATCH back is error-prone — if their script crashes, the sandbox stays expensive forever.

This spec adds a single primitive: a time-bounded boost that the daemon owns. The daemon takes responsibility for reverting at expiry, with retries if the revert fails, and surfaces lifecycle events so observers can react. Boosts replace any prior boost on the same sandbox (one boost active per sandbox at a time). Boosts auto-cancel when the sandbox is stopped or destroyed.

## 2. Non-Goals

- **Stacking / nesting boosts.** One active boost per sandbox; new POST replaces the old one.
- **Per-sandbox quotas / rate limits.** Out of scope for v1; daemon-level bounds are duration-only (`--boost-max-duration`).
- **Magnitude multiplier caps.** Magnitude is bounded only by the existing backend bounds (FC ceiling, Incus generic bounds). Operators who need stricter caps should layer authorization above the API.
- **Auto-extend on activity.** No "while-active heartbeat extends the boost" semantic. Each POST sets an absolute expiry.
- **Boost on stopped sandboxes.** Boost requires `running`; we wouldn't have a live VM to apply to. POST returns 409 if the sandbox isn't running.
- **In-sandbox boost requests.** Spec #3 adds the guest-side channel.
- **CPU live-boost on Firecracker.** Per spec #1, live CPU resize on FC is unsupported in this build (SDK lacks `PatchMachineConfiguration`). A POST that includes only CPU on a running FC sandbox fails with the same 409 (`cpu_resize_unsupported_by_backend`) the resize endpoint produces. Memory-only boosts work fully on FC; mixed CPU+memory POSTs on FC fail at the CPU step and the boost is not started.

## 3. Architecture

### 3.1 Daemon flag

```
--boost-max-duration duration   default 1h
```

Validated at startup as `1m <= boostMaxDuration <= 24h`. The cap exists to prevent accidental "boost forever" bugs (a missing decimal in `duration_seconds` shouldn't reserve a sandbox for a day).

### 3.2 SQLite schema (migration `003_boosts.sql`)

```sql
CREATE TABLE boosts (
    boost_id                  TEXT PRIMARY KEY,
    sandbox_id                TEXT NOT NULL UNIQUE,
    original_cpu_limit        INTEGER,
    original_memory_limit_mb  INTEGER,
    boosted_cpu_limit         INTEGER,
    boosted_memory_limit_mb   INTEGER,
    started_at                TEXT NOT NULL,
    expires_at                TEXT NOT NULL,
    state                     TEXT NOT NULL,         -- 'active' | 'revert_failed'
    revert_attempts           INTEGER NOT NULL DEFAULT 0,
    last_error                TEXT,
    FOREIGN KEY (sandbox_id) REFERENCES sandboxes(sandbox_id) ON DELETE CASCADE
);

CREATE INDEX idx_boosts_expires_at ON boosts(expires_at);
```

`UNIQUE (sandbox_id)` enforces "one active boost per sandbox" at the DB layer. `ON DELETE CASCADE` ensures destroying a sandbox removes its boost row automatically. Times are stored as RFC3339Nano UTC strings, matching the convention used by `sandboxes`, `snapshots`, etc.

`original_*` is recorded purely for caller display in `GET` responses — the actual revert reads the **current** persisted sandbox limits at expiry, not the captured originals (see §3.5).

### 3.3 Domain types

`internal/domain/boost.go`:

```go
type BoostState string

const (
    BoostActive        BoostState = "active"
    BoostRevertFailed  BoostState = "revert_failed"
)

type Boost struct {
    BoostID               string
    SandboxID             string
    OriginalCPULimit      *int
    OriginalMemoryLimitMB *int
    BoostedCPULimit       *int
    BoostedMemoryLimitMB  *int
    StartedAt             time.Time
    ExpiresAt             time.Time
    State                 BoostState
    RevertAttempts        int
    LastError             string
}

type BoostStore interface {
    Get(ctx context.Context, sandboxID string) (*Boost, error)        // ErrNotFound if none
    GetByID(ctx context.Context, boostID string) (*Boost, error)
    Upsert(ctx context.Context, b *Boost) error
    UpdateState(ctx context.Context, boostID string, state BoostState, attempts int, lastErr string) error
    Delete(ctx context.Context, boostID string) error
    ListAll(ctx context.Context) ([]*Boost, error)
}
```

### 3.4 BoostService

```go
type BoostService struct {
    boosts       domain.BoostStore
    sandboxes    domain.SandboxStore
    sandboxSvc   *SandboxService             // calls UpdateResources for apply + revert
    workers      *worker.Dispatcher          // reverts go through async dispatcher
    events       domain.EventBus
    maxDuration  time.Duration

    mu     sync.Mutex
    timers map[string]*time.Timer            // keyed by boost_id
}
```

Public methods:

- `Start(ctx, opts) (*domain.Boost, error)` — POST /boost.
- `Get(ctx, sandboxID) (*domain.Boost, error)` — GET /boost.
- `Cancel(ctx, sandboxID) error` — DELETE /boost.
- `Recover(ctx) error` — called once at daemon startup (replays in-flight boosts).

Internal:
- `expire(boostID)` — timer callback; reverts and either deletes the row or transitions to `revert_failed`.
- `cancelOnLifecycle(ctx, sandboxID)` — called by `SandboxService.Stop` and `Destroy`. Drops the timer and deletes the boost row WITHOUT attempting a revert (the live VM is going away or being suspended; nothing to apply to).

### 3.5 `Start` flow

1. Validate `opts.DurationSeconds > 0 && <= s.maxDuration` → wrap `domain.ErrInvalidArgument` (mapped to 400).
2. Reject if both `opts.CPULimit` and `opts.MemoryLimitMB` are nil → `ErrInvalidArgument`.
3. `sbx, err := s.sandboxes.Get(ctx, opts.SandboxID)` — propagate `ErrNotFound`.
4. Reject if `sbx.State != SandboxRunning` → wrap `domain.ErrInvalidState` ("boost requires sandbox state running, got X").
5. `validateResourceBounds(opts.CPULimit, opts.MemoryLimitMB, sbx.Backend)` — same helper as resize.
6. Cancel any existing boost for this sandbox: under `s.mu`, look up the prior boost ID via `boosts.Get(ctx, sbx.SandboxID)`; if present, `Stop()` and remove its timer, then `boosts.Delete(prior.BoostID)`. No revert here — the new boost is about to apply new limits anyway.
7. Capture originals from `sbx.CPULimit` / `sbx.MemoryLimitMB` (the current persisted limits).
8. Build a `domain.Boost` with `started_at = time.Now().UTC()`, `expires_at = started_at + duration`, `state = active`, `boost_id = uuid`.
9. `s.boosts.Upsert(ctx, &boost)` — insert row.
10. Apply boosted limits via `s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{SandboxID, CPULimit: opts.CPULimit, MemoryLimitMB: opts.MemoryLimitMB, ApplyLiveOnly: true})`. The `ApplyLiveOnly` flag (added to `UpdateResourcesOpts`; see §3.7) skips the SQLite write so the persisted limits continue to reflect the user's intended steady-state. **On error:** `s.boosts.Delete(boost.BoostID)` (rollback) and propagate the error (likely `*ProviderResizeError` → 409).
11. Schedule the timer: `s.timers[boost.BoostID] = time.AfterFunc(duration, func() { s.scheduleRevert(boost.BoostID) })`.
12. Emit `EventBoostStarted`.
13. Return the boost.

> **Concurrency note:** the gap between step 10 (UpdateResources succeeds) and step 11 (timer scheduled) is small but real. If the daemon crashes here, the boost is on disk and applied to the running VM but no timer exists. Recovery (§3.8) reschedules from the persisted row on next startup, so the boost still reverts at its persisted `expires_at` — at most we lose a few hundred ms of timer accuracy.

### 3.6 `expire` flow (timer callback)

Reverts go through the async dispatcher (`workers`) so 100 boosts expiring simultaneously don't burst 100 concurrent provider calls. The dispatcher bounds concurrency to `--concurrency` (default 8) and gives existing observability hooks (operations, traces).

Pseudo-flow when a timer fires:

```go
func (s *BoostService) scheduleRevert(boostID string) {
    s.workers.Enqueue("revert_boost", map[string]any{"boost_id": boostID})
}

// handler registered on the dispatcher:
func (s *BoostService) handleRevert(ctx context.Context, payload map[string]any) error {
    boostID := payload["boost_id"].(string)
    return s.expire(ctx, boostID)
}
```

`expire` itself:

1. Under `s.mu`, drop `s.timers[boostID]` (no-op if already gone).
2. `boost := s.boosts.GetByID(ctx, boostID)`. If `ErrNotFound` (race: cancelled while timer was firing), return nil.
3. `sbx := s.sandboxes.Get(ctx, boost.SandboxID)`. If `ErrNotFound` (sandbox destroyed but cascade hasn't fired yet), `Delete(boostID)` and return nil.
4. If `sbx.State != SandboxRunning`, log + `Delete(boostID)` + emit `EventBoostExpired{cause: "sandbox_not_running"}` and return nil. (Lifecycle hooks should have caught this via `cancelOnLifecycle`, but as a defense-in-depth.)
5. Apply current persisted limits live: `s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{SandboxID, CPULimit: sbx.CPULimit, MemoryLimitMB: sbx.MemoryLimitMB, ApplyLiveOnly: true})`. **Note we read `sbx.CPULimit` / `sbx.MemoryLimitMB`, not `boost.OriginalCPULimit` — this is what makes "PATCH during boost takes effect" work (see §3.7).** `ApplyLiveOnly` is true because the persisted row is already the value we want; we just need to bring the live VM in line.
6. **On success:** `s.boosts.Delete(boostID)`, emit `EventBoostExpired{cause: "expired"}`. Return nil.
7. **On failure:** read the row's current `revert_attempts`, increment, set `last_error`, `s.boosts.UpdateState(ctx, boostID, BoostActive, attempts, err.Error())`. If `attempts < 5`, schedule a retry timer with backoff `[1s, 5s, 30s, 2m, 10m][attempts-1]` that re-enqueues the same job. Otherwise, transition to `BoostRevertFailed` and emit `EventBoostRevertFailed{attempts, last_error}`. The boost row stays for operator visibility.

### 3.7 The boost is a live-only overlay; persisted limits track user intent

A boost MUST NOT mutate the persisted `sandbox.cpu_limit` / `sandbox.memory_limit_mb` columns. The columns continue to reflect the user's steady-state intent throughout the boost's lifetime. The boost is a transient overlay applied directly to the live VM via the provider; the boost row itself records that the overlay exists, what it set, and when it expires.

This invariant is what makes the "revert to current persisted limits at expiry" rule (decided in §2) actually work:

- POST /boost applies boosted limits live but writes nothing to the sandbox row.
- PATCH /resources during the boost behaves exactly as it does outside a boost: it updates persisted limits AND, since the sandbox is running, applies live (transiently overwriting the boost's effects).
- At expiry, `expire` reads the persisted columns (which are either the original pre-boost values, or the post-PATCH-during-boost values — whichever the user intended) and applies them live again.

To enforce this, `service.UpdateResourcesOpts` (defined in spec #1) gains a new optional field:

```go
type UpdateResourcesOpts struct {
    SandboxID     string
    CPULimit      *int
    MemoryLimitMB *int
    ApplyLiveOnly bool  // NEW: when true, skip the SQLite write; only call the provider
}
```

`SandboxService.UpdateResources` checks the flag and skips the `sandboxes.Update` call when set. PATCH /resources continues to use the default `ApplyLiveOnly=false`. Boost paths use `true`.

A consequence worth noting: with `ApplyLiveOnly=true`, there's no rollback-on-provider-error to perform on the SQLite side (since we never wrote). The function still returns the typed `*ProviderResizeError` for the caller to handle.

### 3.8 Recovery on startup

In `BoostService.Recover(ctx)`, called from `cmd/navarisd/main.go` before HTTP listener starts:

1. `boosts := s.boosts.ListAll(ctx)`.
2. For each `b` where `b.State == BoostActive`:
    - Check `sbx, _ := s.sandboxes.Get(ctx, b.SandboxID)`. If sandbox missing or not running, `s.boosts.Delete(b.BoostID)` and continue.
    - **Re-apply the boosted limits live.** A clean restart of navarisd doesn't restart the sandboxes (Firecracker / Incus run independently), so the live VM still has the boosted limits in place — no re-apply needed for the common case. But if the sandbox itself was restarted while navarisd was down, the live VM is at its persisted (un-boosted) limits and the boost record is stale; in that case we re-apply by calling `UpdateResources(ApplyLiveOnly=true)` with the boost's `BoostedCPULimit` / `BoostedMemoryLimitMB`. Detection: compare provider-reported live limits to the boosted values; if they don't match, re-apply.
    - If `time.Now().After(b.ExpiresAt)`: enqueue immediate revert via `scheduleRevert(b.BoostID)`. Goroutine, doesn't block startup.
    - Else: `s.timers[b.BoostID] = time.AfterFunc(b.ExpiresAt.Sub(time.Now()), func() { s.scheduleRevert(b.BoostID) })`.
3. Boosts in `BoostRevertFailed`: leave alone — surfaced via GET; operator must DELETE.

> **Open question:** the "compare provider live limits to boosted values, re-apply if drift" logic in step 2 needs a provider-side `GetLiveLimits` accessor that doesn't exist yet. For v1, document the simplification: if navarisd was down for less than `expires_at`, assume the live VM still has the boost; if the sandbox is restarted during a boost (rare in practice), the boost is silently lost and the timer expires with a no-op revert. This is acceptable and avoids needing the provider extension. We can add the GetLiveLimits accessor in a follow-up if drift becomes a real issue.

### 3.9 Lifecycle hooks

In `internal/service/sandbox.go`:

- `Stop(ctx, id, force)`: after the existing pre-flight checks, before enqueuing the stop op, call `s.boostSvc.cancelOnLifecycle(ctx, id)`. Errors from this are logged but don't block the stop.
- `Destroy(ctx, id)`: same.

`cancelOnLifecycle`:
1. Under `s.mu`, look up + `Stop()` the timer for any boost on this sandbox; drop from map.
2. `s.boosts.Delete(boost.BoostID)` (or rely on `ON DELETE CASCADE` for destroy — for stop, we delete explicitly since the row can't ride out a stop/start cycle without re-application).
3. No event emitted (the sandbox state change events already cover this).

### 3.10 API surface

```
POST /v1/sandboxes/{id}/boost
Content-Type: application/json

Request:
{
  "cpu_limit": 8,
  "memory_limit_mb": 4096,
  "duration_seconds": 600
}

Response 200:
{
  "boost_id": "...",
  "sandbox_id": "...",
  "original_cpu_limit": 2,
  "original_memory_limit_mb": 1024,
  "boosted_cpu_limit": 8,
  "boosted_memory_limit_mb": 4096,
  "started_at": "2026-04-26T12:00:00Z",
  "expires_at": "2026-04-26T12:10:00Z",
  "state": "active"
}

Errors:
  400 — both fields omitted, malformed JSON, duration_seconds out of range,
        bounds violation
  404 — no such sandbox
  409 — sandbox not running, or *ProviderResizeError from backend (e.g.
        exceeds_ceiling on FC, or cpu_resize_unsupported_by_backend on FC CPU)

GET /v1/sandboxes/{id}/boost
  200 — same response shape; for revert_failed boosts, also includes
        `revert_attempts` and `last_error`
  404 — no active boost

DELETE /v1/sandboxes/{id}/boost
  204 — boost cancelled and reverted
  404 — no active boost
  409 — *ProviderResizeError if reverting a revert_failed boost still fails
```

`GET /v1/sandboxes/{id}` (existing) gains an optional `active_boost` field with the same shape as the POST response. Field is present only when an active or revert_failed boost exists; absent otherwise.

### 3.11 Events

In `internal/domain/event.go`:

```go
EventBoostStarted        EventType = "sandbox_boost_started"
EventBoostExpired        EventType = "sandbox_boost_expired"
EventBoostRevertFailed   EventType = "sandbox_boost_revert_failed"
```

Payloads (shape; fields use snake_case in JSON):

```jsonc
// EventBoostStarted
{
  "boost_id": "...",
  "sandbox_id": "...",
  "boosted_cpu_limit": 8,
  "boosted_memory_limit_mb": 4096,
  "expires_at": "..."
}

// EventBoostExpired
{
  "boost_id": "...",
  "sandbox_id": "...",
  "cause": "expired" | "cancelled" | "sandbox_not_running",
  "reverted_cpu_limit": 2,
  "reverted_memory_limit_mb": 1024
}

// EventBoostRevertFailed
{
  "boost_id": "...",
  "sandbox_id": "...",
  "attempts": 5,
  "last_error": "..."
}
```

`EventSandboxResourcesUpdated` (from spec #1) also fires automatically at boost start AND at expiry, since both go through `UpdateResources`. No extra wiring needed.

### 3.12 CLI

`navaris sandbox boost start <id> --cpu N --memory N --duration 5m`
`navaris sandbox boost show <id>`
`navaris sandbox boost cancel <id>`

`--duration` accepts Go duration strings.

### 3.13 Web UI

The `ResourcesPanel` on the sandbox detail page (added in spec #1) gains a "Boost" sub-section:
- No active boost: two number inputs (CPU, Mem) + duration select (`30s / 5m / 30m / custom`) + "Boost" button.
- Active boost: shows boosted values, countdown to `expires_at`, "Cancel" button.
- `revert_failed`: red-bordered banner with `last_error` + "Retry revert" button (calls DELETE).

## 4. Testing

### 4.1 Service-layer unit tests (`internal/service/boost_test.go`)

- `Start` happy path on running sandbox; emits `EventBoostStarted`; persists the boost row; applies boosted limits live without mutating `sandbox.cpu_limit` / `memory_limit_mb` columns
- `Start` rejects: stopped/destroyed/failed sandbox (409), bounds violation (400), duration > max (400), both fields nil (400), unknown sandbox (404)
- `Start` replaces existing boost: prior timer cancelled, prior row replaced, new POST applies new boosted limits; no spurious revert in between
- `Start` rolls back boost row when underlying `UpdateResources` fails
- `expire` reverts to current persisted limits (test PATCH-during-boost case: PATCH changes persisted CPU; expire then reverts to that PATCHed CPU, not the captured original)
- `expire` retries with backoff on `UpdateResources` failure; transitions to `BoostRevertFailed` and emits `EventBoostRevertFailed` after 5 attempts
- `Cancel` reverts immediately; cancelling a `BoostRevertFailed` boost retries the revert one more time and surfaces the provider error if it fails again
- `Recover` replays in-flight boosts on startup; expired-while-down trigger immediate revert; in-window reschedule timer for the remaining duration
- `Stop` and `Destroy` hooks call `cancelOnLifecycle`, dropping the timer and the row without reverting

Use a mockable clock (introduce `service.Clock` interface; production = `realClock{}`; tests = `fakeClock` advancing manually) so timer-based tests don't sleep.

### 4.2 API-layer tests (`internal/api/boost_test.go`)

- POST 200/400/404/409 paths
- GET 200/404 paths
- DELETE 204/404/409 paths
- `getSandbox` returns `active_boost` field when set, omits when nil

### 4.3 Integration tests (`test/integration/boost_test.go`, `//go:build integration`)

- Boost on a running sandbox, observe limits applied immediately, observe revert at expiry (use 2-3s `duration_seconds`)
- Memory boost on Firecracker exercises the spec #1 balloon path
- CPU boost on Firecracker → 409 `cpu_resize_unsupported_by_backend`
- Cancel mid-boost reverts immediately
- PATCH during boost overrides what comes back at expiry

## 5. Open Questions

1. **Live-limit drift detection in Recover.** §3.8 punts on detecting whether a sandbox was restarted during a boost. For v1 we accept silent loss; spec a follow-up if observed in practice.
2. **DELETE semantics for revert_failed boosts.** If the revert fails again on DELETE, current spec says return 409 and leave the row. Alternative: force-delete (drop the row, emit a warning event, leave the live VM at boosted limits). Current choice is more conservative and easier to reason about.

## 6. Migration / Compatibility

- New SQLite migration `003_boosts.sql`. Idempotent within the existing migration runner.
- New daemon flag `--boost-max-duration`; default 1h preserves no-flag deployments without surprise.
- API additions are pure-additive (new endpoints, new event types, optional `active_boost` field on `GET /sandboxes`). No client breakage.
- `service.UpdateResources` gains an optional `ApplyLiveOnly bool` field on `UpdateResourcesOpts`. Default false preserves existing PATCH /resources behavior.

## 7. Out-of-scope notes for spec #3

Spec #3 (in-sandbox channel) builds on this `BoostService` directly: the in-sandbox path lands at a new method on `BoostService` (or a thin wrapper), which adds rate-limiting and per-sandbox boost policy on top of the same `Start`/`Cancel` primitives.
