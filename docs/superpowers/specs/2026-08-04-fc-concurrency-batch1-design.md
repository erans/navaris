# Firecracker + Service Concurrency Fixes — Batch 1

**Status:** Revised design approved in discussion; written-spec review pending
**Date:** 2026-08-04
**Revision reason:** The final whole-branch implementation review found that the
original F1 and F7 locking designs did not fully serialize their state
transitions. This revision replaces those designs while retaining the approved
F12 implementation.

| ID | Issue | Severity | Subsystem |
|----|-------|----------|-----------|
| F1 | `vminfo.json` lost updates across port, resize, and lifecycle writers | **High** | Firecracker provider |
| F12 | Firecracker SDK transports for one-shot calls retain idle Unix-socket FDs | **Low** | Firecracker provider |
| F7 | Boost Start, expiry, and cancellation can apply resource states out of order | **Medium** | Boost service |

## Goals and invariants

1. Existing `vminfo.json` updates are serialized per VM from fresh persistent
   state through live apply and persistence. A stale `p.vms` cache object is
   never written back over newer persistent fields.
2. VM teardown rejects late writers immediately and does not introduce a Go
   data race.
3. Boost bookkeeping and live resource application are linearizable per
   sandbox across Start, expiry, and explicit cancellation.
4. Global locks are never held while waiting for a per-resource lock or while
   calling slow provider operations.
5. Different VMs and different sandboxes remain independent.
6. One-shot Firecracker API transports reap idle connections without changing
   the behavior of long-lived launch machines.

## Non-goals

The remaining adversarial-review batches are unchanged:

- Batch 2: API input bounding (F3 fork count, F6 request body size)
- Batch 3: Incus provider (F2 port allocator, F5 boost UDS permissions)
- Batch 4: auth and web hygiene (F4, F8, F9, F10, F11)

The pre-existing Firecracker socket-path inconsistency between graceful stop
and snapshot/resize remains out of scope. The F12 test enhancement that would
observe a real idle connection being reaped is also deferred; the transport
configuration and production behavior have already been verified.

---

## F1 — Disk-authoritative per-VM state transitions

### Verified failure in the original implementation

Adding a per-VM mutex around file writes was necessary but insufficient.
`PublishPort` reads `vminfo.json`, adds a port, and writes the file, but it does
not update `p.vms`. `UpdateResources` previously obtained `info` from
`p.vms[vmID]` and later wrote that pointer to disk. Consequently, even a
sequential publish followed by resize could replace the newly persisted
`Ports` map with the cache's stale map while leaving the DNAT rule and allocated
host port live.

The original implementation also acquired the per-VM lock only for the resize
commit. Concurrent resizes could therefore apply live limits in one order and
persist them in another. Finally, `vmFileLock.stopped` was a plain `bool`
written under `vmMu` and re-read under the per-VM mutex, which is not a shared
synchronization mechanism and is a Go data race.

### State authority

`vminfo.json` is authoritative for persisted `VMInfo` fields. `p.vms` is a
runtime index/cache.

For every update to an existing `vminfo.json`:

1. Acquire the VM's per-file lock.
2. Read a fresh `VMInfo` from disk while holding the lock.
3. Validate and mutate that fresh object.
4. Perform any live apply required by the operation while still holding the
   lock.
5. Persist the fresh object.
6. After successful persistence, update the corresponding cache fields under
   `vmMu`.

Initial VM creation may construct the first `VMInfo` rather than read a file
that does not yet exist, but it still holds the per-VM lock while writing it.
The existing `recover()` write remains a documented exception because recovery
runs single-threaded before request handling begins.

No writer may pass a pointer obtained from `p.vms` directly to `VMInfo.Write`.
The implementation must audit every `info.Write` site for this rule.

### Lock structure and stop sentinel

```go
type vmFileLock struct {
    mu      sync.Mutex
    stopped atomic.Bool
}
```

`Provider.fileMu` remains a `map[string]*vmFileLock` guarded by `vmMu` for map
lookup only.

`lockFor(vmID)` performs:

1. Under `vmMu`, find or lazily create the entry, then release `vmMu`.
2. Check `stopped.Load()` before waiting. If true, return a wrapped
   `domain.ErrVMStopped` immediately.
3. Acquire `fl.mu`.
4. Re-check `stopped.Load()`. If teardown began during the lookup-to-lock
   window, unlock and return `domain.ErrVMStopped`.
5. Return the held lock.

The atomic flag supplies synchronization for both checks. The per-VM mutex
serializes state transitions; `vmMu` does not protect the flag.

At the start of `StopSandbox`, before listener or VM teardown, lookup/create the
entry under `vmMu` and call `fl.stopped.Store(true)`. Then acquire `fl.mu` and
hold it through the complete teardown, including the graceful wait and final
`ClearRuntime` persistence. Writers that acquired the lock before the sentinel
transition finish before Stop; later writers fail fast. The `fileMu` entry is
removed only after final persistent and cache cleanup.

### Lock ordering

When both locks overlap, the only order is:

```text
vmFileLock.mu → Provider.vmMu
```

`lockFor` briefly takes `vmMu` for lookup but releases it before waiting on
`vmFileLock.mu`, so its lookup does not create an inverse overlapping order.
No global lock is held through iptables, Firecracker socket, cgroup, or file
operations.

### `UpdateResources` transaction

`UpdateResources` acquires `lockFor(ref.Ref)` before reading mutable VM state or
performing a live resource change. While holding `fl.mu`, it:

1. Confirms the VM is still registered in `p.vms`; a completed teardown returns
   `domain.ErrNotFound` rather than operating on the stopped file.
2. Reads fresh `vminfo.json` state.
3. Validates CPU and memory ceilings/capabilities from that state.
4. Captures prior effective CPU and memory values.
5. Applies CPU, then memory, preserving the existing partial-failure rollback.
6. Applies requested limits to the fresh disk object and persists it.
7. Under `vmMu`, copies the successfully persisted limit fields into the
   existing cache entry.

The cache is not mutated before persistence. If persistence fails, the disk and
cache remain at their prior values while live CPU/memory are reverted. If a
live revert also fails, the existing aggregated operator-visible error is
preserved.

Holding `fl.mu` across live apply intentionally serializes concurrent resizes
and port/lifecycle mutations for the same VM. It never blocks operations on a
different VM.

### Port cache coherence

`PublishPort` and `UnpublishPort` already read and mutate fresh disk state while
holding `fl.mu`. After a successful write, each copies the persisted `Ports`
map into the existing `p.vms` entry under `vmMu`. The copy must not alias the
mutable disk object. Failed DNAT, file, or allocator operations retain their
existing rollback behavior and do not update the cache.

### Error mapping

`domain.ErrVMStopped` remains mapped to HTTP 409. `UpdateResources` wraps a
stop-sentinel rejection in `domain.ProviderResizeError` with
`ResizeReasonVMStopped`, also mapped to 409. Existing not-found, validation,
and provider failure behavior remains unchanged.

### F1 tests

Required deterministic coverage:

- publish followed by resize preserves the port mapping in both disk and cache;
- port-vs-resize serialization preserves persisted mapping, recorded DNAT
  behavior, and allocator ownership/release behavior;
- two concurrent resizes cannot apply and persist in opposite orders;
- flipping `stopped` while a writer waits in the lookup-to-lock window is
  race-free and returns `ErrVMStopped`;
- existing concurrent PublishPort and StopSandbox tests remain passing;
- an audit test or verification grep confirms no existing-file writer writes a
  stale `p.vms` pointer.

---

## F12 — Idle-reaping transient Firecracker clients

### Problem

One-shot balloon, live-snapshot, and graceful-shutdown calls used
`fcsdk.NewMachine`. The SDK-created transport has no idle connection timeout
and `Machine` exposes no transport-only close operation. Repeated one-shot
calls against long-lived VMs can retain Unix-socket FDs.

`Machine.client` is unexported, so injecting a custom transport through an SDK
option is not feasible from the Navaris package. The generated low-level
`*client.Firecracker` exposes its operations and transport and is the correct
integration boundary.

### Approved implementation

`transientFirecrackerClient(sockPath, idleTimeout)` creates a generated
Firecracker client with an `http.Transport` that dials the supplied Unix socket
and uses:

```go
MaxIdleConns:        1
MaxIdleConnsPerHost: 1
IdleConnTimeout:     idleTimeout
```

Production call sites use a 30-second idle timeout. The helper is only for
one-shot API calls; long-lived VMM launch paths retain `fcsdk.NewMachine`.

The transient sites call generated operations directly:

- balloon resize: `PatchBalloon`, retaining the SDK-equivalent 500ms request
  timeout and activation retry loop;
- live snapshot: `PatchVM` pause/resume and `CreateSnapshot`, retaining
  defer-resume cleanup and 500ms PatchVM timeouts;
- graceful stop: `CreateSyncAction`, retaining raw context and existing
  best-effort error behavior.

The helper accepts the socket path rather than normalizing it, so it does not
hide or bake in the separately tracked jailer socket-path inconsistency.

### F12 tests and verification

Configuration tests assert the custom transport and timeout. Verification grep
must show `fcsdk.NewMachine` only at the two long-lived launch sites. A true
idle-reap integration test is a deferred Minor enhancement and is not required
for this correction cycle.

---

## F7 — Linearizable per-sandbox boost operations

### Verified failures in the original implementation

The first F7 implementation serialized only `UpdateResources`, after Start had
already replaced boost bookkeeping. Two concurrent Starts could therefore
persist A then B but acquire the apply mutex in the order B then A, leaving the
boost row describing B while the VM ran A's limits.

The implementation also manually deleted a per-sandbox mutex on successful
expiry or cancellation. A waiter could retain the removed mutex while a later
caller created a new one, allowing same-sandbox applies to overlap. Explicit
`Cancel` also performs a live resource revert and therefore belongs in the
same serialization domain as Start and expiry.

### Ref-counted keyed lock registry

```go
type sandboxLock struct {
    mu   sync.Mutex
    refs int // guarded by BoostService.mu
}

sbxLocks map[string]*sandboxLock
```

`acquireSandbox(sandboxID)` behaves as follows:

1. Under `s.mu`, find/create the entry and increment `refs`. Waiters count as
   references.
2. Release `s.mu` before waiting on the entry's mutex.
3. Acquire `entry.mu` and return a release function.
4. The release function unlocks `entry.mu`, then under `s.mu` decrements
   `refs`. It deletes the map entry only when `refs == 0` and the map still
   points to that entry.

Because every holder and waiter increments before waiting, no entry can be
removed while an overlapping operation retains its pointer. If the count
reaches zero, the old mutex is unlocked and has no holder or waiter, so a later
operation may safely create a new entry.

No Start, expiry, Cancel, or lifecycle path manually deletes `sbxLocks`.

### Lock ordering and responsibilities

`BoostService.mu` guards only `timers`, `sbxLocks`, and each entry's reference
count. It is not held through boost-store calls or provider calls.

When locks overlap, the order is:

```text
sandboxLock.mu → BoostService.mu
```

`acquireSandbox` briefly uses `s.mu` to register a reference but releases it
before waiting on `sandboxLock.mu`, so it cannot create an AB-BA cycle.
Different sandbox IDs use different entries and remain concurrent.

### Start

After cheap input validation and an initial sandbox lookup, Start acquires the
sandbox lock and defers release. It then re-fetches and validates the sandbox
under the serialization boundary so it does not act on a stale pre-lock view.
While holding the sandbox lock, Start:

1. stops/removes the prior timer under `s.mu`;
2. deletes or replaces prior boost bookkeeping in the store;
3. persists the new boost row;
4. applies the boosted limits live;
5. on failure, deletes its own row while still serialized;
6. on success, schedules the new timer under `s.mu` and publishes the event.

Bookkeeping and apply therefore have one order for concurrent Starts.

### Expiry

The timer callback may fetch the old boost once to learn its sandbox ID, then
acquires that sandbox's lock. After acquiring it, expiry re-fetches the boost by
`boostID` before any revert. If Start or Cancel already replaced/deleted the
row, the stale callback removes any stale timer entry and exits without
applying resources.

For a current boost, expiry removes the timer under `s.mu`, fetches the current
sandbox limits, applies them live, then deletes the row on success. Failure
retains the existing retry and `revert_failed` state behavior; retry timer map
updates use `s.mu`. The sandbox lock remains held through the complete attempt.

### Explicit Cancel

Cancel acquires the sandbox lock before re-fetching the active boost. It removes
that boost's timer under `s.mu`, applies current persisted sandbox limits live,
and then deletes the boost row. On provider failure it retains the existing
`revert_failed` visibility. A concurrent Start is therefore ordered entirely
before or after Cancel, never split around its revert.

### Lifecycle cleanup

Lifecycle cleanup acquires the same sandbox lock before removing timer and row
bookkeeping without a live revert. This prevents cleanup from interleaving an
already-running Start, expiry, or Cancel. Lock-entry lifetime remains governed
only by reference counting, not lifecycle timing.

This batch does not otherwise redesign sandbox Stop/Destroy state transitions.

### Events and failure handling

Event publication retains existing best-effort semantics and occurs after the
corresponding state transition while the sandbox operation remains serialized.
No path holds global `s.mu` while publishing or calling `UpdateResources`.
Every operation defers its sandbox-lock release so store, provider, rollback,
and event failures cannot leak a registry reference.

### F7 tests

Required deterministic coverage:

- Start-vs-Start cannot reorder bookkeeping and live apply; final row and VM
  limits describe the same boost;
- the existing Start-vs-expire test remains and verifies no overlapping apply;
- Start-vs-Cancel is serialized and leaves a consistent final row/VM state;
- holders and waiters share one entry, the entry survives while any reference
  remains, and it is reclaimed at zero references;
- different sandboxes can enter live apply concurrently;
- retry and `revert_failed` behavior remains covered by existing tests.

---

## Verification-only test race

`TestBoostListener_AcceptsAndDispatches` has a pre-existing test race: the fake
server appends to a slice from the listener goroutine while the test polls that
slice. Replace the polling slice with channel-based completion or protect all
access with a mutex. Production listener behavior is unchanged. This test-only
fix is included so the full race gate can pass without an exception.

## Implementation sequence

1. Add failing F1 stale-state, ordering, and sentinel-race tests.
2. Implement disk-authoritative resize/port cache behavior and atomic sentinel.
3. Add failing F7 Start/Start, Start/Cancel, registry-lifecycle, and
   cross-sandbox tests.
4. Implement the ref-counted registry and serialize complete boost operations.
5. Fix the test-only boost-listener race.
6. Run the complete build and race verification gate.

F12 production code is not changed during this correction cycle.

## Verification gate

```bash
go build ./...
go build -tags firecracker ./...
go test -race -tags firecracker \
  ./internal/provider/firecracker/... \
  ./internal/service/... \
  ./internal/api/... \
  ./internal/domain/...
```

Additionally:

- audit every Firecracker `info.Write` site for fresh-state locking;
- confirm only two long-lived `fcsdk.NewMachine` call sites remain;
- confirm boost code contains no manual `delete(s.sbxLocks, ...)`.

## Compatibility

No database schema or public API shape changes. `ErrVMStopped` and
`ResizeReasonVMStopped` retain their already-implemented HTTP 409 behavior.
The F1 and F7 revisions change only concurrency ordering and cache consistency;
F12 behavior is unchanged from the approved implementation.

## Open questions

None. The state-authority model, stop synchronization, keyed-lock lifecycle,
serialized boost operations, correction-test scope, and verification-only test
fix were approved during the revision discussion.
