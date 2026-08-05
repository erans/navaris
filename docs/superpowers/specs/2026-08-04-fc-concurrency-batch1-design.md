# Firecracker + Service Concurrency Fixes — Batch 1

**Status:** Draft (pending approval)
**Date:** 2026-08-04
**Scope:** Fixes for three concurrency/resource issues identified by an
adversarial code review and adjudicated by an independent reviewer.

| ID | Issue | Adjudicated severity | Subsystem |
|----|-------|----------------------|-----------|
| F1 | `vminfo.json` lost-update race across concurrent port/resize/lifecycle writers | **High** | Firecracker provider |
| F12 | Firecracker SDK `Machine` created for one-shot API-socket calls is never closed — unix-socket FD leak | **Low** | Firecracker provider |
| F7 | `BoostService.expire` races with `Start`, silently reverting a freshly-started boost | **Medium** | Boost service |

All three share a common root pattern: **a state-mutating operation releases
its lock between mutating state and persisting/applying it.** The fix shape
is the same in all three: **hold a per-resource lock across the entire
read-modify-write-apply sequence.** This batch establishes that pattern once.

## Non-goals (deferred to later batches)

The other 9 findings from the review are out of scope for this batch and will
be addressed in subsequent batches:

- **Batch 2 — API input bounding:** F3 (unbounded fork count), F6 (unbounded request bodies)
- **Batch 3 — Incus provider:** F2 (port allocator), F5 (boost UDS permissions)
- **Batch 4 — Auth/web hygiene:** F4 (RateLimiter.GC), F8 (MCP empty-token), F9 (XFF trust), F10 (logout CSRF), F11 (constant-time compare)

---

## F1 — Per-VM `vminfo.json` serialization

### Problem

Every Firecracker operation that mutates a VM's persistent state performs an
**unlocked** read-modify-write of `vminfo.json`: `ReadVMInfo` → mutate a field
→ `info.Write`. The provider's `vmMu` only guards the in-memory `p.vms` map;
the file writes happen outside any lock:

- `port.go:28-53` (PublishPort) and `port.go:72-89` (UnpublishPort) take **no lock at all**.
- `sandbox.go:306` (StartSandbox) writes the file **before** acquiring `vmMu` at line 309.
- `sandbox_resize.go:135` (UpdateResources) **releases** `vmMu` before `info.Write`.

`VMInfo.Write` is atomic per-call (temp file + fsync + rename), so concurrent
writers do not corrupt the file, but they silently **clobber** each other's
updates (last-writer-wins).

**Impact:** Two concurrent `POST /v1/sandboxes/{id}/ports` requests, or a
port publish racing with a live resize, lose updates. A port mapping can be
lost from `info.Ports` while the iptables DNAT rule and the allocated host
port remain — orphaning iptables rules and leaking `portAlloc` ports. A race
between `UpdateResources` and `StopSandbox` can revert a live CPU/memory
resize or lose the recorded `PID`/`TapDevice` so `GetSandboxState` reports
the wrong state.

### Design decision: dedicated per-VM file lock

We add a **dedicated per-VM mutex** whose sole job is serializing
`vminfo.json` access. `vmMu` keeps its current role (guarding the `p.vms`
map). This is preferred over:

- Reusing `vmMu` and holding it across the whole RMW — would make `vmMu` a
  per-provider bottleneck (port publish on VM-A blocks VM-B), and we'd hold
  it through slow sinks (iptables, Firecracker API socket).
- Storing the lock inside the persisted `VMInfo` struct — `VMInfo` is
  serialized to `vminfo.json`; a `sync.Mutex` field would need `json:"-"` and
  is a code smell. Several writers also run before the VM is in `p.vms`.
- An actor pattern (per-VM goroutine owning the file) — bigger refactor,
  every caller becomes async; not justified for this fix.

### Data structure

Add to `Provider` (`internal/provider/firecracker/firecracker.go`):

```go
// fileMu serializes vminfo.json read-modify-write per VM. Guarded by vmMu
// for lookup only; each entry's mu is acquired standalone.
fileMu map[string]*vmFileLock
```

```go
// vmFileLock is the per-VM serialization lock for vminfo.json.
type vmFileLock struct {
    mu      sync.Mutex
    stopped bool // set true at the very start of StopSandbox; fail-fast for late writers
}
```

`vmMu` guards **only** the `fileMu` map lookup. The per-VM `mu` guards the
`vminfo.json` RMW. The `stopped` flag is the fail-fast sentinel.

### Helper: `lockFor(vmID) (*vmFileLock, error)`

```go
// lockFor returns the per-VM file lock held, or ErrVMStopped if the VM is
// being torn down. Caller must unlock. Lookup is under vmMu; the per-VM mu
// is acquired standalone so different VMs never block each other.
func (p *Provider) lockFor(vmID string) (*vmFileLock, error) {
    p.vmMu.Lock()
    fl, ok := p.fileMu[vmID]
    if !ok {
        fl = &vmFileLock{}
        p.fileMu[vmID] = fl
    }
    p.vmMu.Unlock()
    fl.mu.Lock()
    if fl.stopped {
        fl.mu.Unlock()
        return nil, domain.ErrVMStopped
    }
    return fl, nil
}
```

**Fail-fast semantics:** late writers fail *immediately* — they never block
on a VM that's mid-teardown. They receive a typed error so callers
(`PublishPort`, `UpdateResources`) can return a clean "sandbox is stopping"
error to clients rather than silently writing a `vminfo.json` that's about
to be cleared.

### Writer pattern

Every site that does `ReadVMInfo → mutate → info.Write` becomes:

```go
fl, err := p.lockFor(vmID)
if err != nil {
    return fmt.Errorf("firecracker <op> %s: %w", vmID, err)
}
defer fl.mu.Unlock()

info, err := ReadVMInfo(infoPath)
if err != nil { ... }
// ... mutate info ...
if err := info.Write(infoPath); err != nil { ... }
```

### Writer sites to convert

From `grep -rn "ReadVMInfo\|info.Write(" internal/provider/firecracker/*.go`:

| File | Line | Op | Current locking |
|------|------|----|----|
| `port.go` | 28-53 | `PublishPort` | none |
| `port.go` | 72-89 | `UnpublishPort` | none |
| `sandbox.go` | 124 | `CreateSandbox` | (pre-`p.vms` insert) |
| `sandbox.go` | 306 | `StartSandbox` | writes before `vmMu` |
| `sandbox.go` | 469 | (mid-start) | check |
| `sandbox.go` | 523 | `StopSandbox` `Stopping=true` | check |
| `sandbox.go` | 577 | `StopSandbox` `ClearRuntime` | writes before `vmMu` |
| `sandbox.go` | 731 | (check; convert if RMW) | check |
| `sandbox_resize.go` | 135 | `UpdateResources` commit | releases `vmMu` before write |
| `snapshot.go` | 266 | `createLiveSnapshot` commit | check |
| `fork.go` | 195 | fork-point vminfo write | check |
| `firecracker.go` | 318 | `recover()` dead-VM cleanup | single-threaded (see below) |

Each site is converted to the `lockFor` pattern. Sites that currently take
`vmMu` around the in-memory `p.vms` mutation keep doing so — `vmMu` and the
per-VM `fileMu` are independent locks, acquired in a defined order
(`fileMu`'s per-VM mu first, then `vmMu` for map mutation, or vice versa as
long as the order is consistent — see "Lock ordering" below).

### StopSandbox: sentinel + lifecycle

At the very start of `StopSandbox` (before `stopBoostListener`, before any
teardown write):

```go
p.vmMu.Lock()
fl, ok := p.fileMu[vmID]
if !ok {
    fl = &vmFileLock{}
    p.fileMu[vmID] = fl
}
fl.stopped = true
p.vmMu.Unlock()
```

Then hold `fl.mu` for the **entire** Stop body — including the 30s
graceful-wait loop. Late writers fail fast via `lockFor`'s `stopped` check
(they never block waiting for Stop). At the very end, after the
`ClearRuntime` write + `delete(p.vms, vmID)`, also delete the file-lock
entry:

```go
p.vmMu.Lock()
delete(p.vms, vmID)
delete(p.fileMu, vmID)
p.vmMu.Unlock()
```

**Holding `fl.mu` through the 30s wait is correct and cheap:** no writer can
proceed anyway (they fail fast), and we hold a *per-VM* lock, so other VMs'
writers are unaffected.

### `recover()` path (`firecracker.go:280-318`)

Runs single-threaded at daemon startup — no concurrent writers possible.

1. Create the `fileMu` entry for every recovered VM (so the first
   post-startup writer finds it): under `vmMu`,
   `p.fileMu[info.ID] = &vmFileLock{}`.
2. The `info.Write(infoPath)` at line 316 (dead-VM port cleanup) is safe
   *as-is* without `lockFor` because recovery is single-threaded. **Add a
   comment documenting this assumption** so a future caller doesn't
   introduce a race.

### Lock ordering

Two locks are involved in writer sites that also mutate `p.vms`:
`fileMu[vmID].mu` (file serialization) and `vmMu` (map mutation). To prevent
lock-order inversion:

- **Acquire `fileMu[vmID].mu` first**, then `vmMu` for any `p.vms` map
  mutation. This order is consistent with `lockFor` (which acquires the
  per-VM mu) preceding any `p.vms` mutation.
- `lockFor`'s `vmMu`-protected lookup **releases `vmMu` before** acquiring
  the per-VM mu, so there is no overlap — no ordering constraint arises
  from the lookup itself.
- Document the order in a comment on `vmFileLock`.

### New typed error

Add `domain.ErrVMStopped` so API handlers can map it to a clean 409 Conflict
rather than a generic 500. (`ErrSandboxStopping` was considered and rejected —
`ErrVMStopped` matches the provider-layer vocabulary where the resource being
stopped is the VM, not the sandbox; the sandbox is the higher-level domain
object that maps to a VM in the Firecracker backend.)

### API layer

- `internal/api/port.go` `createPort`/`deletePort` handlers map
  `ErrVMStopped` → 409 Conflict.
- `UpdateResources` already returns `*domain.ProviderResizeError`; add a
  `ResizeReasonVMStopped` reason mapped to 409.

### Tests

- **Race test:** spawn N concurrent `PublishPort` calls on the same VM;
  assert no port mapping is lost and `portAlloc` has no orphaned entries
  (i.e. `info.Ports` matches the live iptables rules + allocated ports).
- **Stop race test:** start a `PublishPort` concurrently with
  `StopSandbox`; assert the `PublishPort` either completes before Stop
  (port is cleaned up by Stop) or fails fast with `ErrVMStopped` (no
  orphaned iptables rule).
- **Resize-vs-stop test:** `UpdateResources` racing `StopSandbox` does not
  leave `info.LimitCPU`/`LimitMemMib` reverted or `PID`/`TapDevice` lost.
- **Unit test for `lockFor`:** lazy creation, fail-fast after `stopped` is
  set, and cleanup deletes the entry.
- **`go test -race`** must pass across the Firecracker provider suite.

---

## F12 — Firecracker SDK transport FD leak

### Problem

`sandbox_resize.go:181` (`patchBalloon`), `snapshot.go:149`
(`createLiveSnapshot`), and `sandbox.go:536` (graceful-stop `Shutdown`) call
`fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})` for one-shot
API-socket calls. The SDK's `NewMachine` builds its client via
`NewUnixSocketTransport`, which constructs a bare `http.Transport` with **no
`IdleConnTimeout`** and no `Close` method on `Machine`. Idle unix
connections persist until the peer (the VMM) closes them — fine for
graceful-stop (the VM dies right after) but a leak under sustained
resize/boost churn on a long-lived VM. Under churn, FDs accumulate and can
reach the per-process FD limit.

### Constraints

- `Machine.StopVMM()` is **not** a fix — it stops the VMM (kills the VM),
  not just the transport.
- The SDK exposes no `Machine.Close()`.
- **`Machine.client` is unexported** (`client *Client`), and the SDK's
  `machine_test.go` is white-box (`package firecracker`, same import path).
  Our navaris `package firecracker` is a *different package* (different
  import path), so an `fcsdk.Opt` closure in our code **cannot** assign
  `m.client`. The SDK's `NewClient(socketPath, logger, debug, opts...)`
  builds the transport internally with no hook for a custom one;
  `WithOpsClient` is for mocking, not transport injection. **Approach
  (A) — inject a custom transport via an `Opt` — is infeasible.**
- The SDK wrappers we use (`UpdateBalloon`, `CreateSnapshot`, `Shutdown`
  → `CreateSyncAction`, `PauseVM`/`ResumeVM` → `PatchVM`) are 4-line thin
  wrappers over the low-level generated operations, and the navaris call
  sites pass **no opts**. `*client.Firecracker` exposes those operations
  via an exported `Operations` field and has an exported `Transport`
  field. So we can build a `*client.Firecracker` directly with our own
  idle-reaping transport and call the low-level operations — same Firecracker
  API surface, no HTTP re-implementation, no drift.

### Pre-existing latent bug (out of scope, flagged)

The three call sites use **different socket paths**: `sandbox.go:533`
(graceful stop) uses `root/firecracker.sock`, while `sandbox_resize.go:176`
and `snapshot.go:148` use `root/run/firecracker.socket`. The
`transientClient` helper below takes `sockPath` as an argument rather
than hardcoding one pattern, so it does not bake in this inconsistency.
This inconsistency is a separate latent bug (the graceful-stop CtrlAltDel
under jailer may be hitting the wrong socket and silently failing, since
`if merr == nil { machine.Shutdown(ctx) }` swallows the error) and is
**out of scope for this batch**; flagged for a future fix.

### Fix

Build a transient `*client.Firecracker` per one-shot call with an
idle-reaping transport; call the low-level operations directly. Each
transient client's idle connection auto-reaps after `IdleConnTimeout`.
No lifecycle to manage; independent of F1.

### New helper: `internal/provider/firecracker/fcapi_transport.go`

```go
package firecracker

import (
    "context"
    "net"
    "net/http"
    "time"

    "github.com/go-openapi/runtime"
    httptransport "github.com/go-openapi/runtime/client"
    "github.com/firecracker-microvm/firecracker-go-sdk/client"
    "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
    "github.com/firecracker-microvm/firecracker-go-sdk/client/operations"
    "github.com/go-openapi/strfmt"
)

// transientFirecrackerClient builds a low-level *client.Firecracker bound to
// a running VM's API socket with an idle-reaping transport. Caller issues
// one or two operations then lets it fall out of scope; the idle unix
// connection auto-reaps after idleTimeout, preventing FD retention between
// GC cycles. Use for one-shot API-socket calls; do NOT use for the
// long-lived Machine that manages a VMM lifecycle.
func transientFirecrackerClient(sockPath string, idleTimeout time.Duration) (*client.Firecracker, error) {
    if err := validateSockPath(sockPath); err != nil {
        return nil, err
    }
    fc := client.NewHTTPClient(strfmt.NewFormats())
    fc.SetTransport(buildIdleReapingTransport(sockPath, idleTimeout))
    return fc, nil
}

func buildIdleReapingTransport(sockPath string, idleTimeout time.Duration) runtime.ClientTransport {
    socketTransport := &http.Transport{
        DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
            return net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
        },
        MaxIdleConns:        1,
        MaxIdleConnsPerHost: 1,
        IdleConnTimeout:     idleTimeout,
    }
    transport := httptransport.New(client.DefaultHost, client.DefaultBasePath, client.DefaultSchemes)
    transport.Transport = socketTransport
    return transport
}
```

`IdleConnTimeout: 30s` (configurable; comfortably longer than any request
RTT, short enough to reap quickly between calls). `MaxIdleConnsPerHost: 1`
since there is exactly one host (the socket).

### Call-site changes (3 sites, 5 wrapper calls)

Each site replaces `fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})`
+ a thin SDK wrapper call with a `transientFirecrackerClient(sockPath, ...)`
+ the corresponding low-level operation. The helper takes `sockPath` as an
argument (the three sites use different socket-path conventions — see
"Pre-existing latent bug" above).

- `sandbox_resize.go:176-191` (`patchBalloon`): replace
  `fcsdk.NewMachine` + `machine.UpdateBalloon(ctx, amountMib)` with
  `transientFirecrackerClient(sockPath, 30*time.Second)` +
  `fc.Operations.PatchBalloon(operations.NewPatchBalloonParams().
    WithContext(ctx).WithBody(&models.BalloonUpdate{AmountMib: &amountMib}))`.
  The existing retry loop (up to 10 attempts on "not activated") is preserved.
- `snapshot.go:148-210` (`createLiveSnapshot`): replace `fcsdk.NewMachine` +
  `machine.PauseVM` / `machine.CreateSnapshot` / `machine.ResumeVM` with
  `transientFirecrackerClient` + `fc.Operations.PatchVM(...)` (pause),
  `fc.Operations.CreateSnapshot(...)` (snapshot), `fc.Operations.PatchVM(...)`
  (resume). The defer-resume-on-error cleanup is preserved.
- `sandbox.go:533-539` (graceful-stop): replace `fcsdk.NewMachine` +
  `machine.Shutdown(ctx)` with `transientFirecrackerClient` +
  `fc.Operations.CreateSyncAction(operations.NewCreateSyncActionParams().
    WithContext(ctx).WithInfo(&models.InstanceActionInfo{ActionType: 
      ptrTo(models.InstanceActionInfoActionTypeSendCtrlAltDel)}))`.
  (This path's leak is harmless — the VM dies right after — but using the
  helper uniformly is cleaner and costs nothing. The existing
  `if merr == nil { ... }` error-swallowing behavior is preserved to avoid
  behavior change; flagged as a separate bug in "Pre-existing latent bug".)

### What this does not change

- `CreateSandbox`/`StartSandbox`'s long-lived `Machine` (the one that
  actually launches the VMM) keeps using the default transport — correct,
  it manages the VMM lifecycle.
- The Firecracker API surface used (`PatchBalloon`/`CreateSnapshot`/
  `PatchVM`/`CreateSyncAction`) is unchanged — same generated operations,
  no drift risk, no HTTP re-implementation.

### Tests

- Unit test: `transientFirecrackerClient` produces a `*client.Firecracker`
  whose transport is the idle-reaping one (assert via `fc.Transport` field,
  checking the underlying `*http.Transport`'s `IdleConnTimeout == 30s`).
- Unit test: against a mock unix listener, `PatchBalloon` issued through
  the transient client connects and the idle conn is reaped after
  `IdleConnTimeout` (use a short timeout in the test).
- Existing resize/snapshot/graceful-stop tests pass.
- `grep -rn "fcsdk.NewMachine" internal/provider/firecracker/` confirms
  the long-lived launch Machine is unchanged and the 3 transient sites no
  longer use `fcsdk.NewMachine`.

---

## F7 — Boost expire/Start race

### Problem

`BoostService.expire` (boost.go:172-202) releases `s.mu` after deleting the
timer (lines 173-175), then calls `s.sandboxSvc.UpdateResources` (line 197,
the slow live resize) **unlocked**. `Start` holds `s.mu` for its entire
body (line 94 `defer s.mu.Unlock()`) including its own `UpdateResources`
(line 125). When the auto-revert timer for boost A fires while a new boost
B is being started for the same sandbox, `Start` deletes A, creates B,
applies B's limits — then `expire` (already past its lock) applies the
pre-boost limits, reverting B. The operator sees "boost B active" but the
VM runs un-boosted.

### Fix shape (mirrors F1)

Per-sandbox lock in `BoostService`. Both `Start` and `expire` hold it
across their `UpdateResources` call. Different sandboxes never block each
other. `s.mu` is held only for fast bookkeeping + the `timers` map, never
across `UpdateResources`.

### Data structure

Add to `BoostService`:

```go
// sbxLocks serializes per-sandbox boost apply (UpdateResources) so that
// Start and expire for the same sandbox cannot interleave. Guarded by
// s.mu for lookup; each entry's mutex is acquired standalone.
sbxLocks map[string]*sync.Mutex
```

### `Start` changes (boost.go:61-160)

Currently holds `s.mu` for the entire body. Restructure into phases:

1. **Bookkeeping phase (under `s.mu`):** validation, prior-boost cancel,
   new boost row upsert (lines 94-118). Fast, no slow calls.
2. **Grab per-sandbox lock:** `sbxMu := s.lockSandbox(sbx.SandboxID)`
   (lookup while `s.mu` held). `sbxMu.Lock(); defer sbxMu.Unlock()`.
   Release `s.mu` (or hold through a fast transition — see below).
3. **Apply phase (under `sbxMu`, not `s.mu`):** `UpdateResources` (line 125).
4. **Timer schedule phase (under `s.mu`):** re-acquire `s.mu` for the
   `timers` map mutation + event publish (lines 141-160). The `timers` map
   is guarded by `s.mu`.

Two-phase lock release: `s.mu` (bookkeeping) → `sbxMu` (apply) → `s.mu`
(timer schedule).

### `expire` changes (boost.go:172-202)

Currently: `s.mu.Lock(); delete(s.timers, boostID); s.mu.Unlock()` (lines
173-175), then `GetByID` + `UpdateResources` unlocked. Restructure:

1. **Bookkeeping phase (under `s.mu`):** `delete(s.timers, boostID)`;
   `boost, err := s.boosts.GetByID(ctx, boostID)` (a fast DB read — move it
   inside `s.mu` so we know the sandboxID); `sbxMu := s.lockSandbox(boost.SandboxID)`.
2. Release `s.mu`.
3. **Apply phase (under `sbxMu`, not `s.mu`):** the sandbox lookup +
   `UpdateResources` (line 197).

The retry path (lines 230-236) schedules a timer — re-acquires `s.mu` for
the `timers` map mutation, consistent with the above.

### `Cancel` (boost.go:271-276) and `CleanupForSandbox` (boost.go:352-357)

Both delete from `s.timers` under `s.mu`. They don't call
`UpdateResources`, so they don't strictly need `sbxMu`. For defense-in-depth
against a `Cancel` racing `expire` for the same sandbox (deleting a timer
`expire` is about to reschedule), the spec recommends taking `sbxMu` first
in any path that touches `s.timers[boostID]` for a given sandbox. (Low risk
either way; implementer's judgment.)

### Helper

```go
// lockSandbox returns the per-sandbox boost lock. Must be called while
// holding s.mu (the lookup); the returned mutex is acquired standalone so
// slow UpdateResources calls don't hold s.mu.
func (s *BoostService) lockSandbox(sandboxID string) *sync.Mutex {
    m, ok := s.sbxLocks[sandboxID]
    if !ok {
        m = &sync.Mutex{}
        s.sbxLocks[sandboxID] = m
    }
    return m
}
```

### Lifecycle of `sbxLocks`

Lazy creation is fine — a boost lock lives only while a boost is active
for that sandbox. Delete on:

- successful `expire` (boost ended, row deleted) →
  `delete(s.sbxLocks, sandboxID)` under `s.mu`
- `Cancel` → same
- `CleanupForSandbox` → same

Edge case: a new `Start` arriving just after `expire` deletes the lock
lazily creates a fresh one — correct, since the prior boost is fully gone.

### What this guarantees

`Start` and `expire` for the *same* sandbox serialize on `sbxMu` across
their `UpdateResources` calls — the race is closed. Different sandboxes run
fully concurrently. `s.mu` is held only for fast bookkeeping + the
`timers` map, never across `UpdateResources`.

### Tests

- Existing boost tests pass.
- **Race test:** start boost A with a short duration; concurrently start
  boost B; assert B's limits are what's applied (not A's revert). Use a
  fake `sandboxSvc` whose `UpdateResources` records the last-applied
  limits and blocks on a channel to widen the window.
- **Unit test:** `sbxLocks` is created on first use and deleted on
  expire/cancel.
- `go test -race` passes across the boost service suite.

---

## Implementation notes

- **Worktree + feature branch:** implementation will run in a dedicated
  git worktree on a feature branch (e.g. `fix/fc-concurrency-batch1`),
  isolated from the main working tree. Set up via the using-git-worktrees
  skill at implementation time.
- **Order of work:** F1 first (highest severity, establishes the
  `lockFor` pattern), then F7 (mirrors the same per-resource-lock
  pattern), then F12 (independent, lowest risk).
- **Testing gate:** `go test -race ./internal/provider/firecracker/...
  ./internal/service/...` must pass before any merge.
- **Backward compatibility:** `domain.ErrVMStopped` is a new public error;
  API handlers map it to 409. Existing clients see a 409 instead of a 500
  on ops-against-stopping-sandbox — a strict improvement.

## Open questions

None — all design decisions resolved during brainstorming. The
implementer may surface line-number drift (the line numbers above are from
the reviewed commit; a few may shift slightly during implementation) and
resolve by re-running the cited greps.
