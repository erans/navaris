# In-Sandbox Boost Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-sandbox HTTP-over-Unix-socket channel inside running sandboxes so guest code can request boosts (and read self-state) without going through the operator-facing daemon API.

**Architecture:** Each sandbox gets a dedicated host-side UDS that's exposed inside the guest at `/var/run/navaris-guest.sock`. Firecracker uses vsock + a guest-side proxy in `navaris-agent`; Incus uses an Incus-managed bind-mount. Both produce the same property: each accepted connection is unambiguously the boost channel for one sandbox. A shared `BoostHTTPHandler` routes `POST/GET/DELETE /boost` and `GET /sandbox` to spec #2's `BoostService`.

**Tech Stack:** Go (`net`, `net/http` request/response readers, `mdlayher/vsock` in the agent), sqlite (new migration), Incus client (`unix-socket` device type), existing event bus, existing `BoostService`.

**Spec:** [docs/superpowers/specs/2026-04-26-in-sandbox-boost-channel-design.md](../specs/2026-04-26-in-sandbox-boost-channel-design.md)

---

## File Plan

### Created
- `internal/api/boost_channel.go` — `BoostHTTPHandler`, request/response readers, route dispatch
- `internal/api/boost_channel_test.go` — handler unit tests
- `internal/api/ratelimit.go` — per-sandbox token bucket
- `internal/api/ratelimit_test.go`
- `internal/store/sqlite/migrations/004_boost_channel.sql` — adds `enable_boost_channel` column
- `internal/provider/firecracker/boost_listener.go` — per-VM `vsock_1025` UDS listener
- `internal/provider/firecracker/boost_listener_test.go`
- `internal/provider/incus/boost_socket.go` — per-sandbox host UDS + Incus `unix-socket` device wiring
- `internal/provider/incus/boost_socket_test.go`
- `cmd/navaris-agent/agent/boost_proxy.go` — guest-side HTTP-on-Unix-socket → vsock byte-pipe
- `cmd/navaris-agent/agent/boost_proxy_test.go`
- `test/integration/boost_channel_test.go` — Firecracker-only integration tests

### Modified
- `internal/domain/sandbox.go` — `EnableBoostChannel bool` field
- `internal/domain/provider.go` — `EnableBoostChannel *bool` on `CreateSandboxRequest`
- `internal/domain/event.go` — N/A (no new event types; spec #3 reuses spec #2's three)
- `internal/service/sandbox.go` — `CreateSandboxOpts.EnableBoostChannel *bool`; nil resolution to daemon flag at create time
- `internal/service/boost.go` — `StartBoostOpts.Source string`; emit on event payloads
- `internal/api/boost.go` — pass `Source: "external"` from the existing POST handler
- `internal/api/sandbox.go` — add `enable_boost_channel` to create-request types; embed `source` indirectly via the unchanged event flow
- `internal/api/server.go` — `ServerConfig.BoostHTTPHandler`; thread to providers
- `internal/store/sqlite/sandbox.go` — read/write the new column
- `internal/store/sqlite/sandbox_test.go` — round-trip the field
- `internal/provider/firecracker/firecracker.go` — `BoostHandler` field on `Provider`; setter; `EnableBoostChannel` on VMInfo
- `internal/provider/firecracker/sandbox.go` — start the boost listener after VM starts; stop on Destroy
- `internal/provider/firecracker/vminfo.go` — `EnableBoostChannel bool` field on `VMInfo`
- `internal/provider/firecracker/firecracker.go` `recover()` — recreate listeners
- `internal/provider/incus/incus.go` — `BoostHandler` field; setter; `BoostChannelDir` config
- `internal/provider/incus/sandbox.go` — register host UDS + Incus device add at create; remove on destroy
- `cmd/navaris-agent/main.go` — start the boost proxy alongside the existing port-1024 listener
- `cmd/navarisd/main.go` — `--boost-channel-enabled` / `--boost-channel-dir` flags; construct `BoostHTTPHandler`; inject into providers
- `pkg/client/sandbox.go` — `EnableBoostChannel *bool` on the `CreateSandboxRequest` struct
- `web/src/api/sandboxes.ts` — `enable_boost_channel?: boolean` on `CreateSandboxRequest`; `source?: "external" | "in_sandbox"` on `ActiveBoost` (rendering deferred — see Task 14 note)
- `web/src/components/NewSandboxDialog.tsx` — toggle for "Allow in-sandbox boost requests"
- `README.md` — feature bullet

---

## Conventions

- All work on a fresh feature branch off `main` (use `superpowers:using-git-worktrees`).
- Each task ends with a commit. Match existing commit prefixes (`feat:`, `feat(...)`, `test(...)`, `refactor(...)`, `fix(...)`, `chore(...)`, `docs:`).
- Build tags: Firecracker code is gated by `//go:build firecracker`; Incus by `//go:build incus`. Domain, service, API, store code: untagged.
- After every implementation task: `gofmt -l <files>` clean, all four builds green (`./...`, `-tags firecracker ./...`, `-tags incus ./...`, `-tags 'incus firecracker' ./...`).

---

## Task 1: Domain types — `EnableBoostChannel` field + `StartBoostOpts.Source`

**Files:**
- Modify: `internal/domain/sandbox.go`
- Modify: `internal/domain/provider.go`
- Modify: `internal/service/boost.go`

- [ ] **Step 1: Add the field on `domain.Sandbox`**

In `internal/domain/sandbox.go`, in the `Sandbox` struct, add a new field after `NetworkMode`:

```go
	EnableBoostChannel bool
```

- [ ] **Step 2: Add fields on `domain.CreateSandboxRequest`**

In `internal/domain/provider.go`, in the `CreateSandboxRequest` struct, add after `NetworkMode`:

```go
	EnableBoostChannel *bool  // nil = caller did not specify; service materializes to daemon flag value
	SandboxID          string // navaris-side sandbox UUID; service layer fills this in. Provider uses it as the boost-channel identity (vs. BackendRef which is the FC vmID / Incus container name).
```

`*bool` (not `bool`) so the service layer can distinguish "explicit override" from "use the default".

The `SandboxID` field is needed because the boost channel must dispatch each accepted connection as the navaris-side sandbox identity, but providers today only know their own backend ref. Threading the sandbox ID through `CreateSandboxRequest` is simpler than reverse-lookup `BackendRef → SandboxID` inside the boost handler.

- [ ] **Step 3: Add `Source` to `StartBoostOpts`**

In `internal/service/boost.go`, in the `StartBoostOpts` struct, add:

```go
	Source string   // "external" (operator API) or "in_sandbox" (boost channel); empty defaults to "external"
```

- [ ] **Step 4: Build to confirm everything compiles**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
```

All three must succeed. Existing callers of `StartBoostOpts{...}` without `Source` still compile (zero value is empty string).

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/domain/sandbox.go internal/domain/provider.go internal/service/boost.go
git add internal/domain/sandbox.go internal/domain/provider.go internal/service/boost.go
git commit -m "feat(domain,service): EnableBoostChannel + SandboxID on CreateSandboxRequest; StartBoostOpts.Source"
```

---

## Task 2: SQLite migration + sandbox store read/write

**Files:**
- Create: `internal/store/sqlite/migrations/004_boost_channel.sql`
- Modify: `internal/store/sqlite/sandbox.go` (Create + Update + scan)
- Modify: `internal/store/sqlite/sandbox_test.go`

- [ ] **Step 1: Write the migration**

Create `internal/store/sqlite/migrations/004_boost_channel.sql`:

```sql
-- Per-sandbox toggle for the in-sandbox boost channel.
-- See docs/superpowers/specs/2026-04-26-in-sandbox-boost-channel-design.md.
ALTER TABLE sandboxes ADD COLUMN enable_boost_channel INTEGER NOT NULL DEFAULT 1;
```

`1` (true) is the default — matches the daemon-on-by-default model. Existing rows after migration have the channel enabled in DB; the host-side listener gets created on next sandbox start (not at migration time).

- [ ] **Step 2: Add a failing round-trip test**

In `internal/store/sqlite/sandbox_test.go`, append:

```go
func TestSandboxStore_EnableBoostChannel_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ss := s.SandboxStore()
	ctx := t.Context()
	proj := createTestProject(t, s)

	cases := []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sbx := newTestSandboxFixture(proj.ProjectID, tc.name)
			sbx.EnableBoostChannel = tc.enabled
			if err := ss.Create(ctx, sbx); err != nil {
				t.Fatal(err)
			}
			got, err := ss.Get(ctx, sbx.SandboxID)
			if err != nil {
				t.Fatal(err)
			}
			if got.EnableBoostChannel != tc.enabled {
				t.Errorf("enable_boost_channel = %v; want %v", got.EnableBoostChannel, tc.enabled)
			}
		})
	}
}
```

> **Helper:** `newTestSandboxFixture` is a small helper for building a sandbox with sensible defaults; if it doesn't exist, copy the construction pattern from any existing test in the file (search for `&domain.Sandbox{...`) and inline it. The plan assumes `createTestProject` and `newTestStore` already exist (confirmed in spec #2's task 2).

- [ ] **Step 3: Run, confirm failure**

```bash
go test ./internal/store/sqlite/ -run TestSandboxStore_EnableBoostChannel_Roundtrip -v
```

Expected: FAIL — column not yet read or written.

- [ ] **Step 4: Update `internal/store/sqlite/sandbox.go`**

Add `enable_boost_channel` to the SQL `INSERT` in `Create` (one new column at the end):

```go
	_, err = ss.writeDB.ExecContext(ctx, `INSERT INTO sandboxes
		(sandbox_id, project_id, name, state, backend, backend_ref, host_id,
		 source_image_id, parent_snapshot_id, created_at, updated_at, expires_at,
		 cpu_limit, memory_limit_mb, network_mode, metadata, enable_boost_channel)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sbx.SandboxID, sbx.ProjectID, sbx.Name, string(sbx.State), sbx.Backend,
		nullString(sbx.BackendRef), nullString(sbx.HostID),
		nullString(sbx.SourceImageID), nullString(sbx.ParentSnapshotID),
		sbx.CreatedAt.Format(time.RFC3339Nano), sbx.UpdatedAt.Format(time.RFC3339Nano),
		nullTime(sbx.ExpiresAt), nullInt(sbx.CPULimit), nullInt(sbx.MemoryLimitMB),
		string(sbx.NetworkMode), meta, boolToInt(sbx.EnableBoostChannel))
	return mapError(err)
```

Add `boolToInt` helper at the bottom of the file (or inline if a helper already exists — `grep -n "boolToInt\|intToBool" internal/store/sqlite/`):

```go
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

In `Update`, add `enable_boost_channel = ?` at the end of the SET clause; pass `boolToInt(sbx.EnableBoostChannel)` as the corresponding arg.

In `Get`, `List`, and `ListExpired`: add `enable_boost_channel` to the SELECT column list (place after `metadata`).

In `scanSandbox` / `scanSandboxRow` (whichever helper does the actual `Scan` call), add a new `int` arg for the column, and after scanning, set `sbx.EnableBoostChannel = scanned == 1`.

- [ ] **Step 5: Update the TestOpen expected-tables list if applicable**

Spec #2's task 2 noted that `internal/store/sqlite/sqlite_test.go::TestOpen` asserts a hard-coded list of expected tables. The list is unchanged here (no new table; just a new column). No edit needed unless `TestOpen` also asserts column counts — search and confirm.

- [ ] **Step 6: Run, confirm success**

```bash
go test ./internal/store/sqlite/
```

All green, including the new `TestSandboxStore_EnableBoostChannel_Roundtrip`.

- [ ] **Step 7: gofmt and commit**

```bash
gofmt -l internal/store/sqlite/sandbox.go internal/store/sqlite/sandbox_test.go
git add internal/store/sqlite/migrations/004_boost_channel.sql internal/store/sqlite/sandbox.go internal/store/sqlite/sandbox_test.go
git commit -m "feat(store): enable_boost_channel column on sandboxes"
```

---

## Task 3: Service layer — resolve `EnableBoostChannel` at create time

**Files:**
- Modify: `internal/service/sandbox.go`

- [ ] **Step 1: Add the field to `CreateSandboxOpts`**

In `internal/service/sandbox.go`, in `CreateSandboxOpts`, add a field after `NetworkMode`:

```go
	EnableBoostChannel *bool   // nil = use daemon default
```

- [ ] **Step 2: Add a daemon-default field to `SandboxService`**

In the same file, add to the `SandboxService` struct (place near `defaultBackend`):

```go
	defaultBoostChannel bool
```

Update `NewSandboxService` to take a new `defaultBoostChannel bool` parameter (place after `defaultBackend`); set the field. Update all callers of `NewSandboxService` in this commit so the build stays green:

```bash
grep -rn "service.NewSandboxService(" cmd/ internal/ test/ pkg/
```

For each call site, pass `false`. Task 11 will swap the production `cmd/navarisd/main.go` call to `cfg.boostChannelEnabled` once the daemon flag exists; tests stay on `false`. Keeping every call site uniformly `false` here makes Task 3's diff small and Task 11's change obvious.

- [ ] **Step 3: Resolve nil opts to the default in Create / CreateFromSnapshot / CreateFromImage**

In each of the three create methods, after `validateLimits` but before constructing the `domain.Sandbox`, add:

```go
	enableBoostChannel := s.defaultBoostChannel
	if opts.EnableBoostChannel != nil {
		enableBoostChannel = *opts.EnableBoostChannel
	}
```

Then set `sbx.EnableBoostChannel = enableBoostChannel` when building the domain object. Same for the `domain.CreateSandboxRequest` passed down to the provider:

```go
	provReq := domain.CreateSandboxRequest{
		// ...existing fields...
		EnableBoostChannel: &enableBoostChannel,
		SandboxID:          sbx.SandboxID,   // thread navaris ID through to the provider for boost-channel binding
	}
```

> **Note:** the provider field is `*bool` so the provider knows whether the value was passed; pass a non-nil pointer here since we've already resolved. `SandboxID` is set unconditionally; the provider uses it only when `EnableBoostChannel` is true.

- [ ] **Step 4: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

All clean.

- [ ] **Step 5: Run service tests**

```bash
go test ./internal/service/
```

Existing tests pass with the default `false` from test helpers.

- [ ] **Step 6: gofmt and commit**

```bash
gofmt -l internal/service/sandbox.go
git add internal/service/sandbox.go
# also stage every caller of NewSandboxService updated in step 2:
git add -u
git commit -m "feat(service): CreateSandboxOpts.EnableBoostChannel + daemon-default resolution"
```

---

## Task 4: API request types + handler wiring for `enable_boost_channel`

**Files:**
- Modify: `internal/api/sandbox.go`
- Modify: `pkg/client/sandbox.go`

- [ ] **Step 1: Add the field to all three create-request types**

In `internal/api/sandbox.go`, find `createSandboxRequest`, `createSandboxFromSnapshotRequest`, `createSandboxFromImageRequest`. Add to each:

```go
	EnableBoostChannel *bool `json:"enable_boost_channel"`
```

In each of the three handlers (`createSandbox`, `createSandboxFromSnapshot`, `createSandboxFromImage`), add to the `service.CreateSandboxOpts{...}` literal:

```go
		EnableBoostChannel: req.EnableBoostChannel,
```

- [ ] **Step 2: Add the field to the SDK client**

In `pkg/client/sandbox.go`, find `CreateSandboxRequest` and add:

```go
	EnableBoostChannel *bool `json:"enable_boost_channel,omitempty"`
```

- [ ] **Step 3: Build**

```bash
go build ./...
```

Clean.

- [ ] **Step 4: Run API tests**

```bash
go test ./internal/api/
```

All green — existing tests don't pass the new field, default behavior is preserved.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/api/sandbox.go pkg/client/sandbox.go
git add internal/api/sandbox.go pkg/client/sandbox.go
git commit -m "feat(api,client): enable_boost_channel field on create-sandbox endpoints"
```

---

## Task 5: `BoostService.Source` emission

**Files:**
- Modify: `internal/service/boost.go`
- Modify: `internal/api/boost.go`
- Modify: `internal/service/boost_test.go`

- [ ] **Step 1: Default the source field**

In `internal/service/boost.go::Start`, add at the top of the method (after the existing `if opts.DurationSeconds <= 0` validation):

```go
	source := opts.Source
	if source == "" {
		source = "external"
	}
```

- [ ] **Step 2: Add `source` to the `EventBoostStarted` payload**

In the same method, in the existing `s.events.Publish(...)` block, add a new key:

```go
		Data: map[string]any{
			"boost_id":                boost.BoostID,
			"sandbox_id":              boost.SandboxID,
			"boosted_cpu_limit":       boost.BoostedCPULimit,
			"boosted_memory_limit_mb": boost.BoostedMemoryLimitMB,
			"expires_at":              boost.ExpiresAt.Format(time.RFC3339Nano),
			"source":                  source,
		},
```

- [ ] **Step 3: Add `source` to the `EventBoostExpired` payload**

In `emitExpired`, add:

```go
		Data: map[string]any{
			"boost_id":                 b.BoostID,
			"sandbox_id":               b.SandboxID,
			"cause":                    cause,
			"reverted_cpu_limit":       cpu,
			"reverted_memory_limit_mb": mem,
			"source":                   "external", // expired/cancelled events don't have a meaningful source — they're triggered by the daemon. Use "external" as a stable default.
		},
```

> The `source` distinction is most meaningful on `EventBoostStarted` (the only event the spec §3.10 explicitly requires it on). For `EventBoostExpired` and `EventBoostRevertFailed` we emit `"source": "external"` as a stable default so consumers can rely on the field always being present without nil checks. Note this is a deliberate extension beyond spec §3.10 — call it out in the PR description.

- [ ] **Step 4: Same for `EventBoostRevertFailed`**

In the revert-failed event publish (search for `EventBoostRevertFailed` in `internal/service/boost.go`), add `"source": "external"` to the Data map for the same reason.

- [ ] **Step 5: Update the existing external POST handler**

In `internal/api/boost.go::startBoost`, add `Source: "external"` to the `service.StartBoostOpts{...}` literal:

```go
	b, err := s.cfg.Boosts.Start(r.Context(), service.StartBoostOpts{
		SandboxID:       id,
		CPULimit:        req.CPULimit,
		MemoryLimitMB:   req.MemoryLimitMB,
		DurationSeconds: req.DurationSeconds,
		Source:          "external",
	})
```

- [ ] **Step 6: Add a service-layer test for source emission**

In `internal/service/boost_test.go`, append:

```go
func TestBoostStart_EmitsSource(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	ch, cancel, _ := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostStarted},
	})
	defer cancel()

	cpu := 4
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
		Source: "in_sandbox",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Data["source"] != "in_sandbox" {
			t.Errorf("source = %v, want in_sandbox", ev.Data["source"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostStarted not received")
	}
}

func TestBoostStart_DefaultSourceExternal(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	ch, cancel, _ := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostStarted},
	})
	defer cancel()

	cpu := 4
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
		// no Source set
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Data["source"] != "external" {
			t.Errorf("source = %v, want external (default)", ev.Data["source"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostStarted not received")
	}
}
```

- [ ] **Step 7: Run the new tests + the full boost suite**

```bash
go test ./internal/service/ -run TestBoost -v
go test ./internal/service/
```

All green.

- [ ] **Step 8: gofmt and commit**

```bash
gofmt -l internal/service/boost.go internal/api/boost.go internal/service/boost_test.go
git add internal/service/boost.go internal/api/boost.go internal/service/boost_test.go
git commit -m "feat(service): StartBoostOpts.Source + emit source on boost events"
```

---

## Task 6: Per-sandbox token bucket rate limiter

**Files:**
- Create: `internal/api/ratelimit.go`
- Create: `internal/api/ratelimit_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/api/ratelimit_test.go`:

```go
package api

import (
	"testing"
	"time"
)

func TestRateLimiter_BurstThenWait(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	r := NewRateLimiter(RateLimiterConfig{Burst: 3, RefillPerSec: 1.0, IdleTTL: time.Hour}, clock)

	for i := 0; i < 3; i++ {
		if !r.Allow("sbx-1") {
			t.Fatalf("burst attempt %d denied", i)
		}
	}
	if r.Allow("sbx-1") {
		t.Fatal("4th attempt should be denied")
	}

	// Refill: advance by 2 seconds → 2 tokens.
	now = now.Add(2 * time.Second)
	for i := 0; i < 2; i++ {
		if !r.Allow("sbx-1") {
			t.Fatalf("after refill attempt %d denied", i)
		}
	}
	if r.Allow("sbx-1") {
		t.Fatal("3rd attempt after refill should be denied")
	}
}

func TestRateLimiter_PerSandboxIsolation(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	r := NewRateLimiter(RateLimiterConfig{Burst: 1, RefillPerSec: 1.0, IdleTTL: time.Hour}, func() time.Time { return now })

	if !r.Allow("a") {
		t.Fatal("a denied first attempt")
	}
	if r.Allow("a") {
		t.Fatal("a allowed second attempt within burst")
	}
	if !r.Allow("b") {
		t.Fatal("b denied first attempt — buckets should be per-sandbox")
	}
}

func TestRateLimiter_RetryAfter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	r := NewRateLimiter(RateLimiterConfig{Burst: 1, RefillPerSec: 0.5, IdleTTL: time.Hour}, func() time.Time { return now })

	r.Allow("sbx-1")            // consume token
	wait := r.RetryAfter("sbx-1") // empty bucket; refill is 0.5/s → ~2s for next token
	// Allow a small float-precision window.
	if wait < 1900*time.Millisecond || wait > 2100*time.Millisecond {
		t.Errorf("retryAfter = %v, want ~2s", wait)
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

```bash
go test ./internal/api/ -run TestRateLimiter -v
```

Expected: FAIL — `NewRateLimiter`, `RateLimiterConfig` undefined.

- [ ] **Step 3: Implement `internal/api/ratelimit.go`**

```go
package api

import (
	"math"
	"sync"
	"time"
)

// RateLimiterConfig is exported so cmd/navarisd/main.go can construct one
// without importing internal types.
type RateLimiterConfig struct {
	Burst        int
	RefillPerSec float64
	IdleTTL      time.Duration
}

type RateLimiter struct {
	cfg RateLimiterConfig
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	tokens   float64
	lastFill time.Time
}

func NewRateLimiter(cfg RateLimiterConfig, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{
		cfg:     cfg,
		now:     now,
		buckets: make(map[string]*rateBucket),
	}
}

// NewRateLimiterDefault returns a limiter with the canonical boost-channel
// config: 1 rps, burst 10, 1h idle TTL.
func NewRateLimiterDefault() *RateLimiter {
	return NewRateLimiter(RateLimiterConfig{Burst: 10, RefillPerSec: 1.0, IdleTTL: time.Hour}, nil)
}

// Allow takes one token from the sandbox's bucket. Returns true on success
// (token taken), false on bucket-empty.
func (r *RateLimiter) Allow(sandboxID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	b, ok := r.buckets[sandboxID]
	if !ok {
		b = &rateBucket{tokens: float64(r.cfg.Burst), lastFill: now}
		r.buckets[sandboxID] = b
	} else {
		elapsed := now.Sub(b.lastFill).Seconds()
		b.tokens = math.Min(float64(r.cfg.Burst), b.tokens+elapsed*r.cfg.RefillPerSec)
		b.lastFill = now
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}

// RetryAfter returns the duration until the next token is available. Only
// meaningful immediately after a denied Allow().
func (r *RateLimiter) RetryAfter(sandboxID string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.buckets[sandboxID]
	if !ok || b.tokens >= 1.0 {
		return 0
	}
	missing := 1.0 - b.tokens
	secs := missing / r.cfg.RefillPerSec
	return time.Duration(secs * float64(time.Second))
}

// GC drops buckets idle longer than IdleTTL. Call from a periodic loop.
func (r *RateLimiter) GC() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-r.cfg.IdleTTL)
	for k, b := range r.buckets {
		if b.lastFill.Before(cutoff) {
			delete(r.buckets, k)
		}
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/api/ -run TestRateLimiter -v
go test ./internal/api/
```

All green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/api/ratelimit.go internal/api/ratelimit_test.go
git add internal/api/ratelimit.go internal/api/ratelimit_test.go
git commit -m "feat(api): per-sandbox token-bucket RateLimiter for boost channel"
```

---

## Task 7: `BoostHTTPHandler` — the in-sandbox HTTP server

**Files:**
- Create: `internal/api/boost_channel.go`
- Create: `internal/api/boost_channel_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/api/boost_channel_test.go`:

```go
package api

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

// boostChannelEnv is a minimal harness for BoostHTTPHandler tests. It builds
// the same service stack the other tests use, then constructs a BoostHTTPHandler.
type boostChannelEnv struct {
	store      *sqlite.Store
	mock       *provider.MockProvider
	events     *eventbus.MemoryBus
	dispatcher *worker.Dispatcher
	sandboxes  *service.SandboxService
	boost      *service.BoostService
	handler    *BoostHTTPHandler
}

func newBoostChannelEnv(t *testing.T) *boostChannelEnv {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	s, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	mock := provider.NewMock()
	bus := eventbus.New(64)
	disp := worker.NewDispatcher(s.OperationStore(), bus, 4)
	disp.Start()
	t.Cleanup(func() { disp.Stop() })

	sbxSvc := service.NewSandboxService(
		s.SandboxStore(), s.SnapshotStore(), s.OperationStore(),
		s.PortBindingStore(), s.SessionStore(), mock, bus, disp, "mock", true,
	)
	boostSvc := service.NewBoostService(
		s.BoostStore(), s.SandboxStore(), sbxSvc, bus, service.RealClock{}, time.Hour,
	)
	sbxSvc.SetBoostService(boostSvc)

	h := NewBoostHTTPHandler(boostSvc, s.SandboxStore(),
		NewRateLimiter(RateLimiterConfig{Burst: 10, RefillPerSec: 1.0, IdleTTL: time.Hour}, nil))

	return &boostChannelEnv{
		store: s, mock: mock, events: bus, dispatcher: disp,
		sandboxes: sbxSvc, boost: boostSvc, handler: h,
	}
}

func (e *boostChannelEnv) seedRunningSandbox(t *testing.T, name string) *domain.Sandbox {
	t.Helper()
	cpu, mem := 1, 256
	sbx := &domain.Sandbox{
		SandboxID: "sbx-" + name, ProjectID: "proj-test", Name: name,
		State:     domain.SandboxRunning,
		Backend:   "mock",
		BackendRef: "ref-" + name,
		CPULimit:   &cpu, MemoryLimitMB: &mem,
		NetworkMode: domain.NetworkIsolated,
		EnableBoostChannel: true,
		CreatedAt:  time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := e.store.SandboxStore().Create(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	return sbx
}

// pipeConn pairs a client and server conn over a synchronous in-memory pipe
// so we can drive the handler with raw HTTP bytes.
func pipeConn() (client, server net.Conn) { return net.Pipe() }

func TestBoostChannel_PostBoost_OK(t *testing.T) {
	env := newBoostChannelEnv(t)
	sbx := env.seedRunningSandbox(t, "ok")

	cli, srv := pipeConn()
	go env.handler.Serve(context.Background(), srv, sbx.SandboxID)

	req := "POST /boost HTTP/1.1\r\nHost: _\r\nContent-Type: application/json\r\nContent-Length: 37\r\nConnection: close\r\n\r\n" +
		`{"cpu_limit":4,"duration_seconds":60}`
	if _, err := cli.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	cli.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _ := cli.Read(buf)
	resp := string(buf[:n])
	cli.Close()

	if !strings.HasPrefix(resp, "HTTP/1.1 200") {
		t.Fatalf("status line: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
	if !strings.Contains(resp, `"boost_id"`) {
		t.Errorf("body missing boost_id: %s", resp)
	}
}

func TestBoostChannel_PostBoost_BothFieldsOmitted_400(t *testing.T) {
	env := newBoostChannelEnv(t)
	sbx := env.seedRunningSandbox(t, "empty")

	cli, srv := pipeConn()
	go env.handler.Serve(context.Background(), srv, sbx.SandboxID)

	req := "POST /boost HTTP/1.1\r\nHost: _\r\nContent-Type: application/json\r\nContent-Length: 23\r\nConnection: close\r\n\r\n" +
		`{"duration_seconds":60}`
	cli.Write([]byte(req))
	cli.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _ := cli.Read(buf)
	cli.Close()
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "HTTP/1.1 400") {
		t.Fatalf("status: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
}

func TestBoostChannel_GetSandbox_OK(t *testing.T) {
	env := newBoostChannelEnv(t)
	sbx := env.seedRunningSandbox(t, "info")

	cli, srv := pipeConn()
	go env.handler.Serve(context.Background(), srv, sbx.SandboxID)

	req := "GET /sandbox HTTP/1.1\r\nHost: _\r\nConnection: close\r\n\r\n"
	cli.Write([]byte(req))
	cli.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _ := cli.Read(buf)
	cli.Close()
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "HTTP/1.1 200") {
		t.Fatalf("status: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
	if !strings.Contains(resp, sbx.SandboxID) {
		t.Errorf("body missing sandbox_id: %s", resp)
	}
}

func TestBoostChannel_PostBoost_429_AfterBurst(t *testing.T) {
	env := newBoostChannelEnv(t)
	// Drop limiter to burst=1 so we hit 429 quickly.
	env.handler.limiter = NewRateLimiter(RateLimiterConfig{Burst: 1, RefillPerSec: 1.0, IdleTTL: time.Hour}, nil)

	sbx := env.seedRunningSandbox(t, "rl")

	doRaw := func() string {
		cli, srv := pipeConn()
		go env.handler.Serve(context.Background(), srv, sbx.SandboxID)
		req := "POST /boost HTTP/1.1\r\nHost: _\r\nContent-Type: application/json\r\nContent-Length: 37\r\nConnection: close\r\n\r\n" +
			`{"cpu_limit":4,"duration_seconds":60}`
		cli.Write([]byte(req))
		cli.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 4096)
		n, _ := cli.Read(buf)
		cli.Close()
		return string(buf[:n])
	}

	if !strings.HasPrefix(doRaw(), "HTTP/1.1 200") {
		t.Fatal("first request should succeed")
	}
	resp := doRaw()
	if !strings.HasPrefix(resp, "HTTP/1.1 429") {
		t.Fatalf("second request should 429: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
	if !strings.Contains(resp, "Retry-After") {
		t.Errorf("429 missing Retry-After: %s", resp)
	}
}
```

> **Note:** these tests use raw HTTP bytes over an in-memory pipe to exercise the handler directly. The handler doesn't run a `net/http` server (per spec §3.4), so we can't use `httptest`. The raw approach is verbose but isolates the byte-level protocol behavior the handler must produce.

- [ ] **Step 2: Run, expect compile failures**

```bash
go test ./internal/api/ -run TestBoostChannel -v
```

Expected: FAIL — `NewBoostHTTPHandler`, `BoostHTTPHandler`, `Serve` undefined.

- [ ] **Step 3: Implement `internal/api/boost_channel.go`**

```go
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

// BoostHTTPHandler serves the in-sandbox HTTP API on connections that have
// already been bound to a single sandbox by the transport (FC vsock listener
// or Incus per-sandbox UDS). All requests on a given conn act as that sandbox.
//
// The handler doesn't run net/http.Server because the transport is inherently
// one-conn-per-listener-per-sandbox, and we want byte-level control over the
// response (so we can write 502s if the underlying service is unavailable
// without HTTP server lifecycle). It reads one request, writes one response,
// closes the conn.
type BoostHTTPHandler struct {
	boosts    *service.BoostService
	sandboxes domain.SandboxStore
	limiter   *RateLimiter
}

func NewBoostHTTPHandler(boosts *service.BoostService, sandboxes domain.SandboxStore, limiter *RateLimiter) *BoostHTTPHandler {
	return &BoostHTTPHandler{boosts: boosts, sandboxes: sandboxes, limiter: limiter}
}

// Serve handles one request on conn, then closes conn. sandboxID is the
// implicit identity of the channel — every request on this conn acts as
// that sandbox.
func (h *BoostHTTPHandler) Serve(ctx context.Context, conn net.Conn, sandboxID string) {
	defer conn.Close()

	// v1: flat per-conn rate limiting — every accepted connection consumes one
	// token regardless of method/path. This is a deliberate simplification: the
	// boost channel's hot path is POST /boost; cheap GETs sharing the budget is
	// acceptable for the canonical 1 rps / burst 10 ceiling. If GETs become a
	// pain point, gate by method/path here after the request is parsed.
	if !h.limiter.Allow(sandboxID) {
		retry := h.limiter.RetryAfter(sandboxID)
		writeResp(conn, http.StatusTooManyRequests, map[string]string{"Retry-After": fmt.Sprintf("%d", int(retry.Seconds())+1)},
			[]byte(`{"error":"rate limit exceeded"}`))
		return
	}

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"invalid HTTP request"}`))
		return
	}

	switch {
	case req.Method == http.MethodPost && req.URL.Path == "/boost":
		h.handlePostBoost(ctx, conn, req, sandboxID)
	case req.Method == http.MethodGet && req.URL.Path == "/boost":
		h.handleGetBoost(ctx, conn, sandboxID)
	case req.Method == http.MethodDelete && req.URL.Path == "/boost":
		h.handleDeleteBoost(ctx, conn, sandboxID)
	case req.Method == http.MethodGet && req.URL.Path == "/sandbox":
		h.handleGetSandbox(ctx, conn, sandboxID)
	default:
		writeResp(conn, http.StatusNotFound, nil, []byte(`{"error":"unknown route"}`))
	}
}

func (h *BoostHTTPHandler) handlePostBoost(ctx context.Context, conn net.Conn, req *http.Request, sandboxID string) {
	var body struct {
		CPULimit        *int `json:"cpu_limit"`
		MemoryLimitMB   *int `json:"memory_limit_mb"`
		DurationSeconds int  `json:"duration_seconds"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"invalid JSON"}`))
		return
	}
	if body.CPULimit == nil && body.MemoryLimitMB == nil {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"at least one of cpu_limit, memory_limit_mb is required"}`))
		return
	}
	if body.DurationSeconds <= 0 {
		writeResp(conn, http.StatusBadRequest, nil, []byte(`{"error":"duration_seconds must be > 0"}`))
		return
	}

	b, err := h.boosts.Start(ctx, service.StartBoostOpts{
		SandboxID:       sandboxID,
		CPULimit:        body.CPULimit,
		MemoryLimitMB:   body.MemoryLimitMB,
		DurationSeconds: body.DurationSeconds,
		Source:          "in_sandbox",
	})
	if err != nil {
		writeServiceError(conn, err)
		return
	}
	respBody, _ := json.Marshal(boostToResponse(b))
	writeResp(conn, http.StatusOK, nil, respBody)
}

func (h *BoostHTTPHandler) handleGetBoost(ctx context.Context, conn net.Conn, sandboxID string) {
	b, err := h.boosts.Get(ctx, sandboxID)
	if err != nil {
		writeServiceError(conn, err)
		return
	}
	respBody, _ := json.Marshal(boostToResponse(b))
	writeResp(conn, http.StatusOK, nil, respBody)
}

func (h *BoostHTTPHandler) handleDeleteBoost(ctx context.Context, conn net.Conn, sandboxID string) {
	if err := h.boosts.Cancel(ctx, sandboxID); err != nil {
		writeServiceError(conn, err)
		return
	}
	writeResp(conn, http.StatusNoContent, nil, nil)
}

func (h *BoostHTTPHandler) handleGetSandbox(ctx context.Context, conn net.Conn, sandboxID string) {
	sbx, err := h.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		writeServiceError(conn, err)
		return
	}
	resp := sandboxResponse{Sandbox: sbx}
	if b, err := h.boosts.Get(ctx, sandboxID); err == nil {
		br := boostToResponse(b)
		resp.ActiveBoost = &br
	}
	respBody, _ := json.Marshal(resp)
	writeResp(conn, http.StatusOK, nil, respBody)
}

// writeResp writes an HTTP/1.1 response with a JSON body and the requested
// status. extraHeaders is optional; pass nil if none.
func writeResp(conn net.Conn, status int, extraHeaders map[string]string, body []byte) {
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "Status"
	}
	io.WriteString(conn, fmt.Sprintf("HTTP/1.1 %d %s\r\n", status, statusText))
	if body != nil {
		io.WriteString(conn, "Content-Type: application/json\r\n")
		io.WriteString(conn, "Content-Length: "+strconv.Itoa(len(body))+"\r\n")
	} else {
		io.WriteString(conn, "Content-Length: 0\r\n")
	}
	for k, v := range extraHeaders {
		io.WriteString(conn, k+": "+v+"\r\n")
	}
	io.WriteString(conn, "Connection: close\r\n\r\n")
	if body != nil {
		conn.Write(body)
	}
}

// writeServiceError maps a service-layer error to an HTTP response.
// Mirrors mapErrorCode from response.go but without using net/http.Error.
func writeServiceError(conn net.Conn, err error) {
	status := mapErrorCode(err)
	body, _ := json.Marshal(map[string]string{"error": err.Error()})
	writeResp(conn, status, nil, body)
}
```

> **Note on `mapErrorCode`:** that function lives in `internal/api/response.go` from spec #1. It's already exported within the package — same package as the new handler — so we call it directly.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/api/ -run TestBoostChannel -v
go test ./internal/api/
```

All green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/api/boost_channel.go internal/api/boost_channel_test.go
git add internal/api/boost_channel.go internal/api/boost_channel_test.go
git commit -m "feat(api): BoostHTTPHandler for the in-sandbox boost channel"
```

---

## Task 8: Firecracker per-VM boost listener

**Files:**
- Create: `internal/provider/firecracker/boost_listener.go`
- Create: `internal/provider/firecracker/boost_listener_test.go`
- Modify: `internal/provider/firecracker/firecracker.go` (add `BoostHandler` field + setter)
- Modify: `internal/provider/firecracker/sandbox.go` (start the listener after VM Start, stop on Destroy)
- Modify: `internal/provider/firecracker/vminfo.go` (add `EnableBoostChannel` field)

- [ ] **Step 1: Add `EnableBoostChannel` and `SandboxID` to `VMInfo`**

In `internal/provider/firecracker/vminfo.go`, in the `VMInfo` struct, add:

```go
	EnableBoostChannel bool   `json:"enable_boost_channel,omitempty"`
	SandboxID          string `json:"sandbox_id,omitempty"`  // navaris-side sandbox UUID; distinct from VMInfo.ID (the FC vmID). Persisted in vminfo.json so recover() can rebind boost listeners after daemon restart.
```

> **FC vsock config note:** the existing FC vsock device (search `internal/provider/firecracker/sandbox.go` for the `VsockDevices[0]` setup) is configured with a UDS base path like `<vmDir>/vsock`. Firecracker's contract is that when the **guest** initiates an outbound vsock connection to port N, FC creates `<UDSBase>_<N>` on the host side (so guest port-1025 connect → `<vmDir>/vsock_1025`). No SDK / device-config change is needed for this task — we only add a host-side `net.Listen("unix", ...)` on that auto-created path. Confirm by reading the existing vsock setup before implementing Step 3.

- [ ] **Step 2: Add `BoostHandler` field + setter to `Provider`**

In `internal/provider/firecracker/firecracker.go`, in the `Provider` struct, add:

```go
	boostHandler *api.BoostHTTPHandler
	boostListeners map[string]*boostListener  // keyed by vmID
	boostMu        sync.Mutex
```

`api.BoostHTTPHandler` import: `"github.com/navaris/navaris/internal/api"`. If that creates a circular import (api → provider → api), introduce an interface:

```go
// In firecracker package:
type boostServer interface {
	Serve(ctx context.Context, conn net.Conn, sandboxID string)
}
```

…and have `Provider.boostHandler` be of type `boostServer`. The api package's `*BoostHTTPHandler` satisfies it via duck-typing. Search for existing similar patterns (e.g. provider doesn't import api today; the daemon main.go wires both).

Add a setter:

```go
func (p *Provider) SetBoostHandler(h boostServer) {
	p.boostHandler = h
}
```

Initialize the map in `New`:

```go
	p.boostListeners = make(map[string]*boostListener)
```

- [ ] **Step 3: Implement `internal/provider/firecracker/boost_listener.go`**

```go
//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
)

// boostListener accepts connections on a per-VM UDS and dispatches each one
// to the BoostHTTPHandler bound to a specific sandbox. The UDS file is the
// FC vsock device's auto-created `<vmDir>/vsock_1025` — Firecracker writes
// any guest connect-to-port-1025 to this path.
type boostListener struct {
	vmID      string
	sandboxID string
	udsPath   string
	listener  net.Listener
	cancel    context.CancelFunc
}

func (p *Provider) startBoostListener(ctx context.Context, vmID string) error {
	if p.boostHandler == nil {
		// Daemon was started without a boost handler — nothing to do.
		return nil
	}

	p.vmMu.RLock()
	info, ok := p.vms[vmID]
	p.vmMu.RUnlock()
	if !ok {
		return fmt.Errorf("vm %s not registered", vmID)
	}
	if info.SandboxID == "" {
		return fmt.Errorf("vm %s has no SandboxID; cannot bind boost channel", vmID)
	}

	udsPath := p.boostUDSPath(vmID)
	// Ensure parent dir exists; the FC vsock UDS file is auto-created when
	// the guest connects, so removing any leftover socket is safe.
	_ = os.Remove(udsPath)

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", udsPath, err)
	}
	if p.config.EnableJailer {
		// chown so the jailer UID can connect from inside the chroot.
		_ = os.Chown(udsPath, info.UID, info.UID)
	}

	listenerCtx, cancel := context.WithCancel(ctx)
	bl := &boostListener{vmID: vmID, sandboxID: info.SandboxID, udsPath: udsPath, listener: ln, cancel: cancel}

	p.boostMu.Lock()
	p.boostListeners[vmID] = bl
	p.boostMu.Unlock()

	go bl.acceptLoop(listenerCtx, p.boostHandler)
	slog.Info("firecracker: boost listener started", "vm", vmID, "sandbox", info.SandboxID, "path", udsPath)
	return nil
}

func (p *Provider) stopBoostListener(vmID string) {
	p.boostMu.Lock()
	bl, ok := p.boostListeners[vmID]
	delete(p.boostListeners, vmID)
	p.boostMu.Unlock()
	if !ok {
		return
	}
	bl.cancel()
	bl.listener.Close()
	_ = os.Remove(bl.udsPath)
}

func (bl *boostListener) acceptLoop(ctx context.Context, handler boostServer) {
	for {
		conn, err := bl.listener.Accept()
		if err != nil {
			// Listener closed → exit cleanly.
			return
		}
		go handler.Serve(ctx, conn, bl.sandboxID)
	}
}

// boostUDSPath returns the host-side UDS path that FC creates when the guest
// connects to vsock port 1025.
func (p *Provider) boostUDSPath(vmID string) string {
	if p.config.EnableJailer {
		return filepath.Join(p.vmDir(vmID), "root", "vsock_1025")
	}
	return filepath.Join(p.vmDir(vmID), "vsock_1025")
}
```

- [ ] **Step 4: Hook into `StartSandbox` and `DestroySandbox`**

In `internal/provider/firecracker/sandbox.go`, in `StartSandbox`, after `info.Write(infoPath)` and before the balloon attach (so the listener exists when the guest agent first comes up):

```go
	if info.EnableBoostChannel {
		if err := p.startBoostListener(context.Background(), vmID); err != nil {
			slog.Warn("firecracker: start boost listener failed", "vm", vmID, "err", err)
		}
	}
```

In `CreateSandbox`, populate `info.EnableBoostChannel` and `info.SandboxID` from the request:

```go
	if req.EnableBoostChannel != nil {
		info.EnableBoostChannel = *req.EnableBoostChannel
	}
	info.SandboxID = req.SandboxID  // unconditional; empty when boost channel is disabled, but cheap to keep
```

In `DestroySandbox` (or wherever VM teardown happens — search for "destroy" or "stop" handlers in `sandbox.go`), call:

```go
	p.stopBoostListener(vmID)
```

…before the existing teardown.

- [ ] **Step 5: Add a unit test**

Create `internal/provider/firecracker/boost_listener_test.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type fakeBoostServer struct {
	calls []string
}

func (f *fakeBoostServer) Serve(_ context.Context, conn net.Conn, sandboxID string) {
	f.calls = append(f.calls, sandboxID)
	conn.Close()
}

func TestBoostListener_AcceptsAndDispatches(t *testing.T) {
	tmp := t.TempDir()
	udsPath := filepath.Join(tmp, "vsock_1025")

	server := &fakeBoostServer{}
	p := &Provider{
		boostHandler:   server,
		boostListeners: make(map[string]*boostListener),
	}

	// Manually create a listener (bypassing startBoostListener's path-derivation
	// logic). The path-derivation is exercised in integration tests where a real
	// jailer-aware Provider is constructed; unit-testing it here would require
	// mocking the jailer UID allocator.
	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bl := &boostListener{vmID: "vm-1", sandboxID: "sbx-1", udsPath: udsPath, listener: ln, cancel: cancel}
	p.boostListeners["vm-1"] = bl
	go bl.acceptLoop(ctx, server)

	// Connect from "guest" side and let the loop accept.
	conn, err := net.Dial("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	// Give the goroutine a moment.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(server.calls) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(server.calls) != 1 || server.calls[0] != "sbx-1" {
		t.Fatalf("server.calls = %v, want [sbx-1]", server.calls)
	}
}

func TestBoostListener_PathDerivation_NoJailer(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{config: Config{ChrootBase: tmp, EnableJailer: false}}
	got := p.boostUDSPath("vm-x")
	want := filepath.Join(tmp, "vm-x", "vsock_1025")
	if got != want {
		t.Errorf("boostUDSPath = %s, want %s", got, want)
	}
	// The jailer path variant is exercised by integration tests with a
	// real Provider; constructing one in unit tests requires the full
	// jailer UID allocator and chroot setup.
}
```

- [ ] **Step 6: Run all FC tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/
```

All green.

- [ ] **Step 7: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/boost_listener.go internal/provider/firecracker/boost_listener_test.go internal/provider/firecracker/firecracker.go internal/provider/firecracker/sandbox.go internal/provider/firecracker/vminfo.go
git add internal/provider/firecracker/boost_listener.go internal/provider/firecracker/boost_listener_test.go internal/provider/firecracker/firecracker.go internal/provider/firecracker/sandbox.go internal/provider/firecracker/vminfo.go
git commit -m "feat(firecracker): per-VM boost channel listener (vsock_1025)"
```

---

## Task 9: Firecracker `navaris-agent` boost proxy

**Files:**
- Create: `cmd/navaris-agent/agent/boost_proxy.go`
- Create: `cmd/navaris-agent/agent/boost_proxy_test.go`
- Modify: `cmd/navaris-agent/main.go` — start the proxy alongside the existing port-1024 listener

- [ ] **Step 1: Implement `cmd/navaris-agent/agent/boost_proxy.go`**

```go
package agent

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/mdlayher/vsock"
)

// runBoostProxy serves HTTP-over-Unix-socket at listenPath; each inbound
// connection is piped to a fresh AF_VSOCK conn to (CID=2, port=vsockPort).
// The proxy is a dumb byte-pipe — both sides of the proxy are the host.
//
// Caller is responsible for unlinking listenPath if it already exists.
func RunBoostProxy(ctx context.Context, listenPath string, vsockPort uint32) error {
	_ = os.Remove(listenPath)
	listener, err := net.Listen("unix", listenPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(listenPath)

	// Mode 0666 so any process inside the sandbox can connect.
	_ = os.Chmod(listenPath, 0o666)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("agent: boost proxy accept: %v", err)
			continue
		}
		go pipeToVsock(ctx, conn, vsockPort)
	}
}

func pipeToVsock(ctx context.Context, in net.Conn, vsockPort uint32) {
	defer in.Close()

	out, err := vsock.Dial(2, vsockPort, nil)
	if err != nil {
		// Write a 502 Bad Gateway response and close.
		io.WriteString(in, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	defer out.Close()

	// Bidirectional copy.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(out, in); out.Close() }()
	go func() { defer wg.Done(); io.Copy(in, out); in.Close() }()
	wg.Wait()
}
```

- [ ] **Step 2: Wire the proxy into `cmd/navaris-agent/main.go`**

After the existing `agent.NewServer(ln).Serve()` registration, but before that call (so the proxy starts before main blocks), add a goroutine:

```go
	go func() {
		if err := agent.RunBoostProxy(context.Background(), "/var/run/navaris-guest.sock", 1025); err != nil {
			log.Printf("agent: boost proxy: %v", err)
		}
	}()
```

If the agent's main.go doesn't currently import `context`, add it.

- [ ] **Step 3: Add a unit test**

Create `cmd/navaris-agent/agent/boost_proxy_test.go`:

```go
package agent

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunBoostProxy_ServerUnreachable verifies the proxy responds with 502
// when the upstream vsock is unavailable. Tests the path that runs in CI
// without /dev/vsock.
func TestRunBoostProxy_ServerUnreachable(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "g.sock")

	// Use a port that no one listens on.
	go func() { _ = RunBoostProxy(t.Context(), sock, 65000) }()

	// Wait for the listener to come up.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 502") {
		t.Fatalf("expected 502, got %s", string(buf[:n]))
	}
}
```

> The "happy path" of the proxy (HTTP request → vsock → response) is exercised end-to-end by Task 13's integration tests. The unit test here just verifies the proxy runs and gracefully reports unreachable upstream.

- [ ] **Step 4: Run agent tests**

```bash
go test ./cmd/navaris-agent/agent/
```

All green.

- [ ] **Step 5: Cross-build the agent for the guest target**

The agent runs inside FC VMs (Linux/amd64 typically). Confirm the cross-build still works:

```bash
GOOS=linux GOARCH=amd64 go build -o /tmp/navaris-agent ./cmd/navaris-agent
```

No errors.

- [ ] **Step 6: gofmt and commit**

```bash
gofmt -l cmd/navaris-agent/agent/boost_proxy.go cmd/navaris-agent/agent/boost_proxy_test.go cmd/navaris-agent/main.go
git add cmd/navaris-agent/agent/boost_proxy.go cmd/navaris-agent/agent/boost_proxy_test.go cmd/navaris-agent/main.go
git commit -m "feat(agent): HTTP-over-Unix-socket → vsock boost proxy"
```

---

## Task 10: Incus per-sandbox boost socket

**Files:**
- Create: `internal/provider/incus/boost_socket.go`
- Create: `internal/provider/incus/boost_socket_test.go` (compile-only; mocks where needed)
- Modify: `internal/provider/incus/incus.go` (add `BoostHandler`, `BoostChannelDir` config)
- Modify: `internal/provider/incus/sandbox.go` (start UDS + Incus device add at create; remove on destroy)

- [ ] **Step 1: Add `BoostHandler` field + `BoostChannelDir` config to `IncusProvider`**

In `internal/provider/incus/incus.go`, in the `Config` struct add:

```go
	BoostChannelDir string  // e.g. "/var/lib/navaris/boost-channels"; empty disables boost channel
```

In the `IncusProvider` struct add (mirroring the FC pattern):

```go
	boostHandler   boostServer
	boostListeners map[string]*incusBoostListener  // keyed by container name
	boostMu        sync.Mutex
```

```go
type boostServer interface {
	Serve(ctx context.Context, conn net.Conn, sandboxID string)
}
```

Add a setter:

```go
func (p *IncusProvider) SetBoostHandler(h boostServer) {
	p.boostHandler = h
}
```

In the constructor, initialize the listeners map.

- [ ] **Step 2: Implement `internal/provider/incus/boost_socket.go`**

```go
//go:build incus

package incus

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	incusapi "github.com/lxc/incus/v6/shared/api"
)

// incusBoostListener mirrors the Firecracker per-VM listener: each container
// gets one host UDS, bind-mounted into the container at /var/run/navaris-guest.sock
// via Incus's unix-socket device type.
type incusBoostListener struct {
	containerName string
	sandboxID     string
	udsPath       string
	listener      net.Listener
	cancel        context.CancelFunc
}

const boostDeviceName = "navaris-boost"

func (p *IncusProvider) startBoostChannel(ctx context.Context, name string, sandboxID string) error {
	if p.boostHandler == nil || p.config.BoostChannelDir == "" {
		return nil
	}

	if err := os.MkdirAll(p.config.BoostChannelDir, 0o755); err != nil {
		return fmt.Errorf("create boost channel dir: %w", err)
	}

	udsPath := filepath.Join(p.config.BoostChannelDir, sandboxID+".sock")
	_ = os.Remove(udsPath)

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", udsPath, err)
	}
	_ = os.Chmod(udsPath, 0o666)

	listenerCtx, cancel := context.WithCancel(ctx)
	bl := &incusBoostListener{containerName: name, sandboxID: sandboxID, udsPath: udsPath, listener: ln, cancel: cancel}

	p.boostMu.Lock()
	p.boostListeners[name] = bl
	p.boostMu.Unlock()

	go bl.acceptLoop(listenerCtx, p.boostHandler)

	// Add the unix-socket device that bind-mounts the host UDS into the container.
	put := incusapi.InstancePut{
		Devices: map[string]map[string]string{
			boostDeviceName: {
				"type":   "unix-socket",
				"source": udsPath,
				"path":   "/var/run/navaris-guest.sock",
			},
		},
	}
	inst, etag, err := p.client.GetInstance(name)
	if err != nil {
		// Best-effort cleanup; the listener exists but the bind isn't there.
		ln.Close()
		os.Remove(udsPath)
		return fmt.Errorf("get instance for boost device add: %w", err)
	}
	// Merge our device into the existing config.
	if inst.Devices == nil {
		inst.Devices = map[string]map[string]string{}
	}
	inst.Devices[boostDeviceName] = put.Devices[boostDeviceName]
	op, err := p.client.UpdateInstance(name, inst.Writable(), etag)
	if err != nil {
		ln.Close()
		os.Remove(udsPath)
		return fmt.Errorf("incus update instance (add boost device): %w", err)
	}
	if err := op.WaitContext(ctx); err != nil {
		ln.Close()
		os.Remove(udsPath)
		return fmt.Errorf("incus update wait: %w", err)
	}

	slog.Info("incus: boost channel started", "container", name, "path", udsPath)
	return nil
}

func (p *IncusProvider) stopBoostChannel(name string) {
	p.boostMu.Lock()
	bl, ok := p.boostListeners[name]
	delete(p.boostListeners, name)
	p.boostMu.Unlock()
	if !ok {
		return
	}
	bl.cancel()
	bl.listener.Close()
	_ = os.Remove(bl.udsPath)
	// Best-effort: remove the device from the container if it still exists.
	if inst, etag, err := p.client.GetInstance(name); err == nil {
		if _, has := inst.Devices[boostDeviceName]; has {
			delete(inst.Devices, boostDeviceName)
			if op, err := p.client.UpdateInstance(name, inst.Writable(), etag); err == nil {
				_ = op.WaitContext(context.Background())
			}
		}
	}
}

func (bl *incusBoostListener) acceptLoop(ctx context.Context, handler boostServer) {
	for {
		conn, err := bl.listener.Accept()
		if err != nil {
			return
		}
		go handler.Serve(ctx, conn, bl.sandboxID)
	}
}
```

- [ ] **Step 3: Hook into `IncusProvider.CreateSandbox`**

In `internal/provider/incus/sandbox.go::CreateSandbox`, after the existing `op.WaitContext(ctx)` (after the container has been created in Incus), add:

```go
	enableChan := req.EnableBoostChannel != nil && *req.EnableBoostChannel
	if enableChan {
		if req.SandboxID == "" {
			slog.Warn("incus: EnableBoostChannel set but SandboxID empty; skipping boost channel start", "container", name)
		} else if err := p.startBoostChannel(ctx, name, req.SandboxID); err != nil {
			slog.Warn("incus: start boost channel failed", "container", name, "err", err)
		}
	}
```

> The navaris-side `SandboxID` arrives via `domain.CreateSandboxRequest.SandboxID` (added in Task 1, populated in Task 3). The Incus container `name` is the BackendRef. The boost-channel UDS filename is `<sandboxID>.sock` so it's stable across restarts.

- [ ] **Step 4: Hook into `DestroySandbox`**

In `internal/provider/incus/sandbox.go::DestroySandbox`, before the existing teardown:

```go
	p.stopBoostChannel(ref.Ref)
```

- [ ] **Step 5: Compile-only test**

Create `internal/provider/incus/boost_socket_test.go`:

```go
//go:build incus

package incus

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type fakeIncusBoostServer struct {
	got []string
}

func (f *fakeIncusBoostServer) Serve(_ context.Context, conn net.Conn, sandboxID string) {
	f.got = append(f.got, sandboxID)
	conn.Close()
}

// TestIncusBoostListener_AcceptLoop tests the accept→dispatch path
// independently of incus client interactions.
func TestIncusBoostListener_AcceptLoop(t *testing.T) {
	tmp := t.TempDir()
	udsPath := filepath.Join(tmp, "sbx-1.sock")

	server := &fakeIncusBoostServer{}
	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	bl := &incusBoostListener{containerName: "c1", sandboxID: "sbx-1", udsPath: udsPath, listener: ln, cancel: cancel}
	go bl.acceptLoop(ctx, server)

	conn, err := net.Dial("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(server.got) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(server.got) != 1 || server.got[0] != "sbx-1" {
		t.Fatalf("server.got = %v, want [sbx-1]", server.got)
	}
}
```

- [ ] **Step 6: Run incus tests**

```bash
go test -tags incus ./internal/provider/incus/
```

All green.

- [ ] **Step 7: gofmt and commit**

```bash
gofmt -l internal/provider/incus/boost_socket.go internal/provider/incus/boost_socket_test.go internal/provider/incus/incus.go internal/provider/incus/sandbox.go
git add internal/provider/incus/boost_socket.go internal/provider/incus/boost_socket_test.go internal/provider/incus/incus.go internal/provider/incus/sandbox.go
git commit -m "feat(incus): per-sandbox boost channel UDS + unix-socket device"
```

---

## Task 11: Daemon wiring

**Files:**
- Modify: `cmd/navarisd/main.go`

- [ ] **Step 1: Add the flags**

In the `config` struct (find around `boostMaxDuration` from spec #2):

```go
	boostChannelEnabled bool
	boostChannelDir     string
```

In `parseFlags`, near the existing `--boost-max-duration` flag:

```go
	flag.BoolVar(&cfg.boostChannelEnabled, "boost-channel-enabled", true,
		"enable the in-sandbox boost channel by default for new sandboxes")
	flag.StringVar(&cfg.boostChannelDir, "boost-channel-dir", "/var/lib/navaris/boost-channels",
		"host directory for per-sandbox Incus boost-channel UDS files")
```

- [ ] **Step 2: Pass `defaultBoostChannel` into `NewSandboxService`**

Find the existing `service.NewSandboxService(...)` call and add `cfg.boostChannelEnabled` as the new last argument (matching the signature change from Task 3).

- [ ] **Step 3: Construct the rate limiter and `BoostHTTPHandler`**

After the existing `boostSvc := service.NewBoostService(...)` line:

```go
	rateLim := api.NewRateLimiterDefault()  // 1 rps, burst 10, 1h idle TTL
	boostHandler := api.NewBoostHTTPHandler(boostSvc, store.SandboxStore(), rateLim)
```

`NewRateLimiterDefault` was added in Task 6.

- [ ] **Step 4: Inject the handler into both providers**

Find where `firecracker.New(...)` and `incus.New(...)` are constructed. After construction, call:

```go
	if fcProv != nil {
		fcProv.SetBoostHandler(boostHandler)
	}
	if incusProv != nil {
		incusProv.SetBoostHandler(boostHandler)
	}
```

For Incus, also pass `BoostChannelDir: cfg.boostChannelDir` into the `incus.Config{...}` literal.

- [ ] **Step 5: Build all four configurations + verify flag**

```bash
go build -tags 'incus firecracker' -o /tmp/navarisd ./cmd/navarisd/
/tmp/navarisd --help 2>&1 | grep boost-channel
```

Both flags should appear.

```bash
go build ./...
go build -tags incus ./...
go build -tags firecracker ./...
```

All clean.

- [ ] **Step 6: gofmt and commit**

```bash
gofmt -l cmd/navarisd/main.go
git add cmd/navarisd/main.go
git commit -m "feat(navarisd): --boost-channel-enabled / --boost-channel-dir + handler wiring"
```

---

## Task 12: Recovery — recreate listeners on daemon restart

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go::recover()`
- Modify: `internal/provider/incus/incus.go` (recovery scan)

- [ ] **Step 1: FC recovery**

In `internal/provider/firecracker/firecracker.go`, find the existing `recover()` method (the one that scans `<chrootBase>/nvrs-fc-*` and rebuilds the in-memory `vms` map). After registering each `info` in `p.vms`, add:

```go
		if info.EnableBoostChannel && info.PID > 0 && processAlive(info.PID) {
			if err := p.startBoostListener(context.Background(), info.ID); err != nil {
				slog.Warn("firecracker: recover boost listener", "vm", info.ID, "err", err)
			}
		}
```

Skip listener creation for VMs that aren't running — they'll get one when restarted.

- [ ] **Step 2: Incus recovery**

Incus doesn't have a `recover()` like Firecracker — its provider is stateless on the daemon side, with state owned by `incusd`. So recovery for Incus runs in `cmd/navarisd/main.go`: after the BoostHandler is wired, list all running Incus sandboxes from the SandboxStore and call `startBoostChannel` for each that has `EnableBoostChannel: true`.

In `cmd/navarisd/main.go`, after the `incusProv.SetBoostHandler(boostHandler)` call:

```go
	// Replay boost channels for running Incus sandboxes after daemon restart.
	if incusProv != nil {
		ctx := context.Background()
		sandboxes, err := store.SandboxStore().List(ctx, domain.SandboxFilter{
			Backend: ptrString("incus"),
		})
		if err == nil {
			for _, sbx := range sandboxes {
				if sbx.State != domain.SandboxRunning || !sbx.EnableBoostChannel {
					continue
				}
				if err := incusProv.RestartBoostChannel(ctx, sbx.BackendRef, sbx.SandboxID); err != nil {
					slog.Warn("incus: replay boost channel", "sandbox", sbx.SandboxID, "err", err)
				}
			}
		}
	}
```

> **Helper `ptrString`:** if not present in main.go already, add a tiny helper:
>
> ```go
> func ptrString(s string) *string { return &s }
> ```

> **`RestartBoostChannel`:** add this small public method on `IncusProvider`. On daemon restart, the host UDS file is gone (process death cleans it) but the Incus container may still have a stale `navaris-boost` device pointing at the now-vanished source. Remove the stale device first, then call `startBoostChannel` to re-create both UDS and device.
>
> ```go
> // internal/provider/incus/boost_socket.go
> func (p *IncusProvider) RestartBoostChannel(ctx context.Context, containerName, sandboxID string) error {
>     // Best-effort: remove any stale boost device left over from a prior daemon
>     // process. If the container still has it, the source UDS doesn't exist and
>     // re-adding the device with the same name would otherwise fail.
>     if inst, etag, err := p.client.GetInstance(containerName); err == nil {
>         if _, has := inst.Devices[boostDeviceName]; has {
>             delete(inst.Devices, boostDeviceName)
>             if op, err := p.client.UpdateInstance(containerName, inst.Writable(), etag); err == nil {
>                 _ = op.WaitContext(ctx)
>             }
>         }
>     }
>     return p.startBoostChannel(ctx, containerName, sandboxID)
> }
> ```

- [ ] **Step 3: Run all backend tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/
go test -tags incus ./internal/provider/incus/
```

All green.

- [ ] **Step 4: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/firecracker.go internal/provider/incus/boost_socket.go cmd/navarisd/main.go
git add internal/provider/firecracker/firecracker.go internal/provider/incus/boost_socket.go cmd/navarisd/main.go
git commit -m "feat(boost-channel): recreate listeners on daemon restart"
```

---

## Task 13: Integration tests (Firecracker only)

**Files:**
- Create: `test/integration/boost_channel_test.go`

- [ ] **Step 1: Write the integration tests**

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

func ptrIntChan(v int) *int { return &v }
func ptrBoolChan(v bool) *bool { return &v }

// TestBoostChannel_FC_StartFromInside creates an FC sandbox with the boost
// channel enabled, exec's curl from inside to POST /boost on the local UDS,
// and verifies the boost shows up via the external GET /v1/sandboxes/{id}/boost
// with source: "in_sandbox".
//
// Skipped on Incus per spec #2's pattern: limits.memory on Incus create
// breaks forkstart in this CI environment.
func TestBoostChannel_FC_StartFromInside(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s); see spec #2 task 13 note", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boostchan-fc",
		ImageID: img, MemoryLimitMB: &mem,
		EnableBoostChannel: ptrBoolChan(true),
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	// From inside the guest, POST to the local socket.
	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c",
			`curl --unix-socket /var/run/navaris-guest.sock -sS -X POST http://_/boost ` +
				`-H 'Content-Type: application/json' ` +
				`-d '{"memory_limit_mb":192,"duration_seconds":3}'`},
	})
	if err != nil {
		t.Fatalf("exec curl: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec exit %d: stderr=%s stdout=%s", exec.ExitCode, exec.Stderr, exec.Stdout)
	}
	if !strings.Contains(exec.Stdout, `"boost_id"`) {
		t.Fatalf("response missing boost_id: %s", exec.Stdout)
	}

	// Verify externally that the boost is live with source="in_sandbox".
	b, err := c.GetBoost(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetBoost: %v", err)
	}
	if b.State != "active" {
		t.Errorf("state = %s", b.State)
	}
	// source isn't on the Boost client struct yet — check via a raw GET if needed,
	// or skip this assertion for now if pkg/client doesn't expose source.
	// (Add Source string field to client.Boost in Task 5 if you want it here.)

	// Wait past expiry.
	time.Sleep(5 * time.Second)
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatal("expected 404 after expiry")
	}
}

func TestBoostChannel_FC_OptOut(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s)", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boostchan-optout",
		ImageID: img, MemoryLimitMB: &mem,
		EnableBoostChannel: ptrBoolChan(false),
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s", op.State)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"test", "-e", "/var/run/navaris-guest.sock"},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if exec.ExitCode == 0 {
		t.Fatal("/var/run/navaris-guest.sock should not exist when opt-out")
	}
}

func TestBoostChannel_FC_GetSandbox(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s)", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu, mem := 1, 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boostchan-info",
		ImageID: img, CPULimit: &cpu, MemoryLimitMB: &mem,
		EnableBoostChannel: ptrBoolChan(true),
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s", op.State)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c",
			`curl --unix-socket /var/run/navaris-guest.sock -sS http://_/sandbox`},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec exit %d: %s", exec.ExitCode, exec.Stderr)
	}
	if !strings.Contains(exec.Stdout, sandboxID) {
		t.Errorf("response missing sandbox id: %s", exec.Stdout)
	}
}
```

- [ ] **Step 2: Compile-only check**

```bash
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both clean.

- [ ] **Step 3: gofmt and commit**

```bash
gofmt -l test/integration/boost_channel_test.go
git add test/integration/boost_channel_test.go
git commit -m "test(integration): in-sandbox boost channel (FC)"
```

---

## Task 14: Web UI — `enable_boost_channel` toggle in NewSandboxDialog

**Files:**
- Modify: `web/src/api/sandboxes.ts` — `enable_boost_channel?: boolean` on `CreateSandboxRequest`; `source?: "external" | "in_sandbox"` on `ActiveBoost`
- Modify: `web/src/components/NewSandboxDialog.tsx`

- [ ] **Step 1: Add the fields to the API client types**

In `web/src/api/sandboxes.ts`:

- On `CreateSandboxRequest`, add:

```ts
  enable_boost_channel?: boolean;
```

- On `ActiveBoost`, add:

```ts
  source?: "external" | "in_sandbox";
```

> **Note on `source`:** spec §3.10 puts `source` on the `EventBoostStarted` payload, not on the persisted boost row. The current `boostResponse` from `internal/api/boost.go` therefore doesn't include it, and the existing `ActiveBoost` shape on the UI mirrors that response. Adding the optional TS field now lets event-stream consumers (and any future API change that surfaces source on the row) populate it without a breaking type change. Rendering in `SandboxDetail.tsx` is intentionally deferred — to display it, add `Source string` to `domain.Boost` plus a backing migration, persist on `BoostService.Start`, and surface in `boostToResponse`. Out of scope for this plan.

- [ ] **Step 2: Add the toggle to the dialog**

In `web/src/components/NewSandboxDialog.tsx`, find the form fields. Add a checkbox near the network-mode radio:

```tsx
const [enableBoostChannel, setEnableBoostChannel] = useState(true);
// ... in JSX:
<label className="flex items-center gap-2">
  <input
    type="checkbox"
    checked={enableBoostChannel}
    onChange={(e) => setEnableBoostChannel(e.currentTarget.checked)}
  />
  <span>Allow in-sandbox boost requests</span>
</label>
```

In the create-sandbox submit handler, add to the request body:

```tsx
  enable_boost_channel: enableBoostChannel,
```

- [ ] **Step 3: Build the front-end**

```bash
cd web && npm run build 2>&1 | tail -3
```

Clean.

- [ ] **Step 4: Commit**

```bash
git add web/src
git commit -m "feat(web): enable_boost_channel toggle + ActiveBoost.source type"
```

---

## Task 15: README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add the feature line**

Find the "Time-bounded boost" line (added in spec #2). Insert after:

```markdown
- **In-sandbox boost channel**: guest code can request its own boost via `POST /boost` on `/var/run/navaris-guest.sock` inside the sandbox. Implicitly authenticated (channel == sandbox identity), per-sandbox token-bucket rate-limited (1 rps, burst 10), opt-in/opt-out per sandbox (`enable_boost_channel`) with daemon-default flag `--boost-channel-enabled` (default true). See [docs/superpowers/specs/2026-04-26-in-sandbox-boost-channel-design.md](docs/superpowers/specs/2026-04-26-in-sandbox-boost-channel-design.md).
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(readme): in-sandbox boost channel feature"
```

---

## Final verification

- [ ] **Run the full unit test matrix:**

```bash
go test ./...
go test -tags incus ./...
go test -tags firecracker ./...
go test -tags 'incus firecracker' ./...
```

All green.

- [ ] **Web build:**

```bash
cd web && npm run build
```

Clean.

- [ ] **Smoke test (if a dev environment is available):**

```bash
./navarisd ... &
./navaris sandbox create --image alpine-3.21 --memory 512 my-sbx
./navaris sandbox exec my-sbx -- sh -c \
  "curl --unix-socket /var/run/navaris-guest.sock -X POST http://_/boost \
    -d '{\"memory_limit_mb\":384,\"duration_seconds\":30}'"
./navaris sandbox boost show my-sbx   # expect source=in_sandbox somehow surfaced
```

- [ ] **Open a PR** referencing the spec doc and this plan.
