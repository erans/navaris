# Firecracker + Service Concurrency Fixes — Batch 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close three concurrency/resource bugs in the Firecracker provider and boost service by holding a per-resource lock across the entire read-modify-write-apply sequence for `vminfo.json` (F1), the boost apply path (F7), and by replacing leaked Firecracker SDK `Machine` instances with a transient idle-reaping low-level client (F12).

**Architecture:** F1 adds a per-VM `vmFileLock{mu, stopped}` map to the Firecracker `Provider`, accessed via a `lockFor(vmID)` helper that fail-fasts when a VM is stopping. F7 adds a per-sandbox `sbxLocks` map to `BoostService` and restructures `Start`/`expire` into two-phase `s.mu`→`sbxMu` lock release so the slow `UpdateResources` call runs under the per-sandbox lock, not the global one. F12 introduces a `transientFirecrackerClient(sockPath, idleTimeout)` helper that builds a `*client.Firecracker` with an idle-reaping `http.Transport` (`IdleConnTimeout: 30s`) and replaces the 3 `fcsdk.NewMachine` call sites with direct low-level operations.

**Tech Stack:** Go 1.22+, `sync`, `net/http`, `github.com/firecracker-microvm/firecracker-go-sdk` v1.0.0 (low-level `client.Firecracker` + `client/operations` + `client/models`), `github.com/go-openapi/runtime/client`, `github.com/go-openapi/strfmt`. Tests use the existing `//go:build firecracker` tag for white-box Firecracker tests and `package service_test` (black-box) for boost tests with a `fakeClock`.

## Global Constraints

- All new sentinel errors live in `internal/domain/errors.go` and follow the existing `var Err... = errors.New(...)` pattern.
- New `ResizeReason*` constants live in `internal/domain/provider.go` alongside the existing ones.
- API error→status mapping is via the existing `respondError` helper in `internal/api/response.go` (which already switches on sentinel errors); do not introduce a new mapping path.
- Firecracker-provider tests use `//go:build firecracker` and `package firecracker` (white-box). Construct `Provider` directly with `&Provider{...}` in tests (see `sandbox_resize_test.go:13-31` for the pattern), not via `New`.
- Boost tests use `package service_test` (black-box) with the existing `boostEnv`/`fakeClock` helpers in `internal/service/boost_test.go`.
- **TDD:** every task writes the failing test first, runs it to confirm it fails, implements the minimum to pass, runs it to confirm pass, then commits.
- **Testing gate before any merge:** `go build ./... && go test -race ./internal/provider/firecracker/... ./internal/service/...` must pass.
- The line numbers cited from the spec are from commit `2330537`; they may drift slightly. Re-locate each site with the cited `grep` before editing.
- **Worktree + feature branch:** implementation runs in a dedicated git worktree on branch `fix/fc-concurrency-batch1`, set up via the using-git-worktrees skill before Task 1.

---

## File Structure

**Created:**
- `internal/provider/firecracker/fcapi_transport.go` — F12 helper: `transientFirecrackerClient`, `buildIdleReapingTransport`, tiny `validateSockPath`.
- `internal/provider/firecracker/fcapi_transport_test.go` — F12 unit tests for the helper.
- `internal/provider/firecracker/vminfo_lock_test.go` — F1 unit tests for `lockFor` and the stopped-sentinel.
- `internal/provider/firecracker/port_race_test.go` — F1 concurrency test for `PublishPort`/`UnpublishPort`.
- `internal/service/boost_race_test.go` — F7 concurrency test for `Start` vs `expire`.

**Modified:**
- `internal/domain/errors.go` — add `ErrVMStopped`.
- `internal/domain/provider.go` — add `ResizeReasonVMStopped`.
- `internal/provider/firecracker/firecracker.go` — add `fileMu` field to `Provider`, init in `New`, create entries in `recover()`.
- `internal/provider/firecracker/vminfo.go` — add `vmFileLock` type + comment.
- `internal/provider/firecracker/port.go` — wrap `PublishPort`/`UnpublishPort` RMW in `lockFor`.
- `internal/provider/firecracker/sandbox.go` — convert all vminfo writers to `lockFor`; StopSandbox sentinel + lifecycle; replace `fcsdk.NewMachine` (graceful stop) with `transientFirecrackerClient`.
- `internal/provider/firecracker/sandbox_resize.go` — convert `UpdateResources` commit to `lockFor`; replace `fcsdk.NewMachine` (`patchBalloon`) with `transientFirecrackerClient`.
- `internal/provider/firecracker/snapshot.go` — convert snapshot vminfo writer to `lockFor`; replace `fcsdk.NewMachine` (`createLiveSnapshot`) with `transientFirecrackerClient`.
- `internal/provider/firecracker/fork.go` — convert fork-point vminfo writer to `lockFor`.
- `internal/service/boost.go` — add `sbxLocks` field; restructure `Start`/`expire` to two-phase locking; lazy-create/delete lifecycle.
- `internal/service/boost_test.go` — extend `boostEnv` to support the new race test (may need a blocking `UpdateResourcesFn`).
- `internal/api/port.go` — map `ErrVMStopped` → 409 in `respondError` (likely auto-handled by sentinel switch; verify).
- `internal/api/response.go` — extend the sentinel-error switch to map `ErrVMStopped` → 409 Conflict (if not already).

---

## Task 1: Add `domain.ErrVMStopped` sentinel + API 409 mapping

**Files:**
- Modify: `internal/domain/errors.go`
- Modify: `internal/api/response.go`
- Test: `internal/domain/errors_test.go` (extend) and `internal/api/response_test.go` (extend if exists; otherwise a small new test)

**Interfaces:**
- Consumes: existing `respondError` sentinel switch in `internal/api/response.go`.
- Produces: `domain.ErrVMStopped` (a `var Err... = errors.New("vm is stopping")`), mapped to HTTP 409 by `respondError`. Later tasks (`lockFor`, `UpdateResources`) wrap it with `fmt.Errorf("...: %w", domain.ErrVMStopped)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/domain/errors_test.go` (create the file if it doesn't exist; check first with `ls internal/domain/errors_test.go`):

```go
package domain_test

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrVMStopped_IsSentinel(t *testing.T) {
	// Wrapped errors must still match via errors.Is (used by respondError).
	wrapped := fmt.Errorf("firecracker publish port vm-x: %w", ErrVMStopped)
	if !errors.Is(wrapped, ErrVMStopped) {
		t.Fatalf("errors.Is(wrapped, ErrVMStopped) = false; want true")
	}
}
```

If `internal/domain/errors_test.go` already exists with `package domain` (white-box), match its existing package declaration instead of `package domain_test`. Run `head -5 internal/domain/errors_test.go` to check.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/ -run TestErrVMStopped -v`
Expected: FAIL with `undefined: ErrVMStopped` (compile error).

- [ ] **Step 3: Implement — add the sentinel**

In `internal/domain/errors.go`, add `ErrVMStopped` to the existing `var (...)` block:

```go
var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrInvalidState     = errors.New("invalid state transition")
	ErrCapacityExceeded = errors.New("capacity exceeded")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrForbidden        = errors.New("forbidden")
	ErrBusy             = errors.New("database busy")
	ErrNotSupported     = errors.New("operation not supported")
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrVMStopped        = errors.New("vm is stopping") // F1: returned by Provider.lockFor when the VM is mid-teardown
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/domain/ -run TestErrVMStopped -v`
Expected: PASS.

- [ ] **Step 5: Add the API 409 mapping (test-first)**

First inspect the existing sentinel switch in `internal/api/response.go` around the `respondError` function (run `sed -n '60,130p' internal/api/response.go`). The existing switch maps `ErrConflict` → 409 (line ~94). Add `ErrVMStopped` to the same branch so it also maps to 409.

If there is an existing `response_test.go` covering `respondError`, add a case there; otherwise add a small test:

```go
// In internal/api/response_test.go (or extend existing). Match the file's
// existing package declaration.
func TestRespondError_VMStopped_MapsTo409(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, fmt.Errorf("publish port: %w", domain.ErrVMStopped))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusConflict)
	}
}
```

- [ ] **Step 6: Run test to verify it fails, then implement**

Run: `go test ./internal/api/ -run TestRespondError_VMStopped -v` → FAIL.
Then edit `internal/api/response.go` to add `ErrVMStopped` to the 409 branch (the same `case` that maps `ErrConflict`). Re-run → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/errors.go internal/domain/errors_test.go internal/api/response.go internal/api/response_test.go
git commit -m "feat(domain): add ErrVMStopped sentinel + map to HTTP 409"
```

---

## Task 2: Add `ResizeReasonVMStopped` constant

**Files:**
- Modify: `internal/domain/provider.go` (the `const (...)` block around line 71-76)
- Test: `internal/domain/provider_test.go` if it exists; otherwise skip the test (a bare constant needs no test, but verify it compiles via `go build`).

**Interfaces:**
- Consumes: nothing.
- Produces: `domain.ResizeReasonVMStopped = "vm_stopped"` for use in Task 7 when `UpdateResources` hits `ErrVMStopped` from `lockFor`.

- [ ] **Step 1: Add the constant**

In `internal/domain/provider.go`, extend the existing `const (...)` block:

```go
const (
	ResizeReasonExceedsCeiling          = "exceeds_ceiling"
	ResizeReasonCPUUnsupportedByBackend = "cpu_resize_unsupported_by_backend"
	ResizeReasonBackendRejected         = "backend_rejected"
	ResizeReasonCgroupUnavailable       = "cgroup_unavailable"
	ResizeReasonCgroupWriteFailed       = "cgroup_write_failed"
	ResizeReasonVMStopped               = "vm_stopped" // F1: VM is mid-teardown; live resize cannot run
)
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/domain/`
Expected: succeeds.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/provider.go
git commit -m "feat(domain): add ResizeReasonVMStopped constant"
```

---

## Task 3: Add `vmFileLock` type and `fileMu` field + `lockFor` helper

**Files:**
- Modify: `internal/provider/firecracker/vminfo.go` — add `vmFileLock` type.
- Modify: `internal/provider/firecracker/firecracker.go` — add `fileMu` field to `Provider`, init in `New`.
- Create: `internal/provider/firecracker/vminfo_lock_test.go` — unit tests for `lockFor`.
- Note: `lockFor` is added to `firecracker.go` (or a new `vminfo_lock.go`); pick one and stay consistent. Recommended: new file `internal/provider/firecracker/vminfo_lock.go` for the helper + its tests' mental model.

**Interfaces:**
- Consumes: `domain.ErrVMStopped` (Task 1), `p.vmMu`, `p.fileMu`.
- Produces: `(*Provider).lockFor(vmID string) (*vmFileLock, error)` — returns the per-VM lock held, or `domain.ErrVMStopped` (wrapped) if the VM is stopping. Caller must `defer fl.mu.Unlock()`. `vmFileLock` is unexported (provider-internal).

- [ ] **Step 1: Write the failing test**

Create `internal/provider/firecracker/vminfo_lock_test.go`:

```go
//go:build firecracker

package firecracker

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

func TestLockFor_LazyCreation(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}

	fl, err := p.lockFor("vm-1")
	if err != nil {
		t.Fatalf("lockFor: %v", err)
	}
	defer fl.mu.Unlock()

	// Same vmID returns the same lock object on a second call (after unlock).
	p.vmMu.Lock()
	fl2, ok := p.fileMu["vm-1"]
	p.vmMu.Unlock()
	if !ok || fl2 != fl {
		t.Fatalf("lazy-created lock not stored in fileMu")
	}
}

func TestLockFor_FailFastWhenStopped(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}

	// Simulate StopSandbox having flipped the sentinel.
	p.vmMu.Lock()
	p.fileMu["vm-2"] = &vmFileLock{stopped: true}
	p.vmMu.Unlock()

	_, err := p.lockFor("vm-2")
	if !errors.Is(err, domain.ErrVMStopped) {
		t.Fatalf("err = %v; want ErrVMStopped", err)
	}
}

func TestLockFor_SerializesConcurrentWriters(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}

	var (
		inFlight  int
		maxInFlight int
		mu        sync.Mutex
	)
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			fl, err := p.lockFor("vm-3")
			if err != nil {
				t.Errorf("lockFor: %v", err)
				return
			}
			defer fl.mu.Unlock()

			mu.Lock()
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			mu.Unlock()

			// Hold briefly to force overlap.
			// (no-op; scheduler contention is enough at N=50.)
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if maxInFlight != 1 {
		t.Errorf("max in-flight writers = %d; want 1 (must be serialized)", maxInFlight)
	}
}

// TestLockFor_FailFastWhileStopHoldsLock verifies the fast path: a late
// writer must fail with ErrVMStopped WITHOUT blocking on fl.mu while
// StopSandbox holds it. (With a naive "acquire fl.mu then check stopped"
// design, this test would block for the full hold time and time out.)
func TestLockFor_FailFastWhileStopHoldsLock(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}
	p.vmMu.Lock()
	fl := &vmFileLock{stopped: true}
	p.fileMu["vm-4"] = fl
	p.vmMu.Unlock()

	// Simulate StopSandbox holding fl.mu through a long teardown.
	fl.mu.Lock()
	defer fl.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := p.lockFor("vm-4")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrVMStopped) {
			t.Fatalf("err = %v; want ErrVMStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lockFor blocked on fl.mu while stopped; fail-fast broken")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/ -run TestLockFor -v`
Expected: FAIL with `p.fileMu undefined` and `p.lockFor undefined`.

- [ ] **Step 3: Implement `vmFileLock` type**

In `internal/provider/firecracker/vminfo.go`, add near the bottom (before `ScanVMDirs` or after `ReadVMInfo`):

```go
// vmFileLock serializes per-VM read-modify-write access to vminfo.json.
// The mu field is acquired standalone (via Provider.lockFor); the stopped
// flag is the fail-fast sentinel set at the very start of StopSandbox.
//
// Lock ordering: acquire fileMu[vmID].mu BEFORE p.vmMu when both are needed
// (lockFor acquires vmMu only for the map lookup and releases it before
// taking the per-VM mu, so no ordering constraint arises from the lookup).
type vmFileLock struct {
	mu      sync.Mutex
	stopped bool
}
```

(Add `"sync"` to the import block of `vminfo.go` if not already present.)

- [ ] **Step 4: Add `fileMu` field + init in `New`**

In `internal/provider/firecracker/firecracker.go`, add the field to the `Provider` struct (next to `vms`):

```go
type Provider struct {
	config        Config
	// ... existing fields ...
	vms           map[string]*VMInfo
	vmMu          sync.RWMutex
	fileMu        map[string]*vmFileLock // F1: per-VM vminfo.json serialization; guarded by vmMu for lookup
	// ... existing fields ...
}
```

In `New` (the `p := &Provider{...}` literal around line 173-184), add:

```go
p := &Provider{
	// ... existing fields ...
	vms:            make(map[string]*VMInfo),
	fileMu:         make(map[string]*vmFileLock),
	// ... existing fields ...
}
```

- [ ] **Step 5: Implement `lockFor` helper**

Create `internal/provider/firecracker/vminfo_lock.go`:

```go
package firecracker

import "github.com/navaris/navaris/internal/domain"

// lockFor returns the per-VM file lock held, or an error wrapping
// domain.ErrVMStopped if the VM is mid-teardown. Caller must unlock fl.mu.
//
// Lookup is under p.vmMu; the per-VM mu is acquired standalone so different
// VMs never block each other. Fail-fast: late writers receive the error
// immediately rather than blocking on a stopping VM.
func (p *Provider) lockFor(vmID string) (*vmFileLock, error) {
	p.vmMu.Lock()
	fl, ok := p.fileMu[vmID]
	if !ok {
		fl = &vmFileLock{}
		p.fileMu[vmID] = fl
	}
	// Fast path: check stopped under vmMu so a late writer fails immediately
	// WITHOUT acquiring fl.mu — which StopSandbox holds for its entire
	// teardown (up to 30s). Acquiring fl.mu first would block, defeating
	// fail-fast.
	if fl.stopped {
		p.vmMu.Unlock()
		return nil, fmt.Errorf("firecracker: vm %s is stopping: %w", vmID, domain.ErrVMStopped)
	}
	p.vmMu.Unlock()

	fl.mu.Lock()
	// Re-check after acquiring: StopSandbox may have flipped stopped between
	// the vmMu.Unlock above and this Lock. If so, bail.
	if fl.stopped {
		fl.mu.Unlock()
		return nil, fmt.Errorf("firecracker: vm %s is stopping: %w", vmID, domain.ErrVMStopped)
	}
	return fl, nil
}
```

(Add `"fmt"` to the import block.)

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -tags firecracker -race ./internal/provider/firecracker/ -run TestLockFor -v`
Expected: PASS (all three subtests).

- [ ] **Step 7: Commit**

```bash
git add internal/provider/firecracker/vminfo.go internal/provider/firecracker/vminfo_lock.go internal/provider/firecracker/firecracker.go internal/provider/firecracker/vminfo_lock_test.go
git commit -m "feat(firecracker): add vmFileLock + Provider.lockFor per-VM file serialization"
```

---

## Task 4: Wire `fileMu` into `recover()` and convert `PublishPort`/`UnpublishPort`

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go` (recover path ~line 280-318)
- Modify: `internal/provider/firecracker/port.go` (PublishPort ~28-53, UnpublishPort ~72-89)
- Test: `internal/provider/firecracker/port_race_test.go` (new)

**Interfaces:**
- Consumes: `Provider.lockFor` (Task 3).
- Produces: `PublishPort`/`UnpublishPort` now serialize per-VM via `lockFor`. `recover()` creates `fileMu` entries for recovered VMs.

- [ ] **Step 1: Write the failing concurrency test**

Create `internal/provider/firecracker/port_race_test.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
)

// TestPublishPort_Concurrent_NoLostUpdate: N concurrent PublishPort calls
// on one VM must produce exactly N distinct port mappings in vminfo.json.
// Without lockFor, the unlocked read-modify-write loses updates (last
// writer wins, so entries vanish). The iptables path is stubbed via the
// package-level addDNATFn/removeDNATFn vars (introduced in Step 3) so the
// test needs no iptables or root.
func TestPublishPort_Concurrent_NoLostUpdate(t *testing.T) {
	addDNATFn = func(int, string, int) error { return nil }
	removeDNATFn = func(int, string, int) {}
	defer func() {
		addDNATFn = network.AddDNAT
		removeDNATFn = network.RemoveDNAT
	}()

	dir := t.TempDir()
	p := &Provider{
		config:    Config{ChrootBase: dir, EnableJailer: false},
		subnets:   network.NewAllocator(),
		portAlloc: network.NewPortAllocator(),
		vms:       map[string]*VMInfo{},
		fileMu:    map[string]*vmFileLock{},
	}
	seed := &VMInfo{ID: "vm-race", PID: os.Getpid(), TapDevice: "fc-race", SubnetIdx: 0, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir("vm-race"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath("vm-race")); err != nil {
		t.Fatal(err)
	}
	p.vmMu.Lock()
	p.vms["vm-race"] = seed
	p.fileMu["vm-race"] = &vmFileLock{}
	p.vmMu.Unlock()

	const N = 25
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			if _, err := p.PublishPort(context.Background(),
				domain.BackendRef{Backend: "firecracker", Ref: "vm-race"},
				4000+i, domain.PublishPortOptions{}); err != nil {
				t.Errorf("PublishPort %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	info, err := ReadVMInfo(p.vmInfoPath("vm-race"))
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Ports) != N {
		t.Fatalf("info.Ports has %d entries; want %d (lost-update race)", len(info.Ports), N)
	}
}
```

This test references `addDNATFn`/`removeDNATFn` (package-level vars introduced in Step 3) and `p.fileMu`/`vmFileLock` (from Task 3), so it will not compile until those exist — that is the expected RED state.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker -race ./internal/provider/firecracker/ -run TestPublishPort_Concurrent -v`
Expected: FAIL to compile — `addDNATFn`/`removeDNATFn` (Step 3) and `p.fileMu`/`vmFileLock` (Task 3) are not yet defined. (If Task 3 is already merged, only `addDNATFn`/`removeDNATFn` are undefined.)

- [ ] **Step 3: Convert `PublishPort` to use `lockFor`**

First, introduce package-level function variables in `port.go` (top of file, after imports) so the iptables path is stubbable in tests:

```go
// addDNATFn/removeDNATFn wrap network.AddDNAT/RemoveDNAT so tests can stub
// the iptables path. Default to the real functions.
var (
	addDNATFn    = network.AddDNAT
	removeDNATFn = network.RemoveDNAT
)
```

Then replace every `network.AddDNAT(...)` / `network.RemoveDNAT(...)` call in `port.go` with `addDNATFn(...)` / `removeDNATFn(...)`. The real `PublishPort`/`UnpublishPort` now wrap their read-modify-write in `lockFor`:

In `internal/provider/firecracker/port.go`, wrap the RMW. Current code (lines 27-53):

```go
// Allocate host port.
hostPort, err := p.portAlloc.Allocate()
if err != nil { ... }
infoPath := p.vmInfoPath(vmID)
info, err := ReadVMInfo(infoPath)
if err != nil { p.portAlloc.Release(hostPort); ... }
// ... validation, AddDNAT ...
info.Ports[hostPort] = targetPort
if err := info.Write(infoPath); err != nil { ... }
```

New code:

```go
hostPort, err := p.portAlloc.Allocate()
if err != nil { return ..., fmt.Errorf("...: %w", err) }

fl, err := p.lockFor(vmID)
if err != nil {
	p.portAlloc.Release(hostPort)
	return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port %s: %w", vmID, err)
}
defer fl.mu.Unlock()

infoPath := p.vmInfoPath(vmID)
info, err := ReadVMInfo(infoPath)
if err != nil { p.portAlloc.Release(hostPort); ... }
// ... validation, AddDNAT (unchanged) ...
info.Ports[hostPort] = targetPort
if err := info.Write(infoPath); err != nil { ... }
```

Note: `portAlloc.Allocate()` happens before `lockFor` (it has its own mutex; no deadlock risk since `portAlloc.mu` is never held while acquiring `fileMu`). `portAlloc.Release(hostPort)` on the error paths stays as-is.

- [ ] **Step 4: Convert `UnpublishPort` similarly**

Same `lockFor` wrapping for the `ReadVMInfo → delete(info.Ports, publishedPort) → info.Write` RMW in `UnpublishPort` (lines 72-89). `portAlloc.Release(publishedPort)` stays after the successful write (unchanged).

- [ ] **Step 5: Wire `fileMu` into `recover()`**

In `internal/provider/firecracker/firecracker.go`, in `recover()` (around line 286-289 where `p.vms[info.ID] = info` is set under `vmMu`):

```go
p.vmMu.Lock()
p.vms[info.ID] = info
p.fileMu[info.ID] = &vmFileLock{} // F1: ensure first post-startup writer finds the lock
p.vmMu.Unlock()
```

The existing `info.Write(infoPath)` at line ~316 (dead-VM port cleanup) stays as-is — add a comment above it:

```go
// F1: safe without lockFor — recover() is single-threaded at daemon
// startup, so no concurrent writer can race this write. Do not call this
// path from any concurrent context without adding lockFor.
info.Ports = nil
infoPath := p.vmInfoPath(info.ID)
if err := info.Write(infoPath); err != nil { ... }
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -tags firecracker -race ./internal/provider/firecracker/ -run TestPublishPort_Concurrent -v`
Expected: PASS.

Then run the full firecracker suite to catch regressions:
`go test -tags firecracker -race ./internal/provider/firecracker/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/provider/firecracker/port.go internal/provider/firecracker/firecracker.go internal/provider/firecracker/port_race_test.go
git commit -m "fix(firecracker): serialize PublishPort/UnpublishPort vminfo RMW via lockFor"
```

---

## Task 5: Convert `UpdateResources` commit and StopSandbox sentinel + lifecycle

**Files:**
- Modify: `internal/provider/firecracker/sandbox_resize.go` (commit block ~127-145)
- Modify: `internal/provider/firecracker/sandbox.go` (StopSandbox ~503-585; all vminfo writers in this file)
- Test: extend `internal/provider/firecracker/sandbox_resize_test.go` and add a stop-race test in `internal/provider/firecracker/port_race_test.go` or a new `stop_race_test.go`.

**Interfaces:**
- Consumes: `Provider.lockFor` (Task 3), `domain.ResizeReasonVMStopped` (Task 2).
- Produces: `UpdateResources` returns `*domain.ProviderResizeError{Reason: ResizeReasonVMStopped, ...}` when `lockFor` fails. `StopSandbox` flips the sentinel at start, holds `fl.mu` through the entire body, deletes `fileMu[vmID]` at end.

- [ ] **Step 1: Write the failing test — Stop fail-fast**

Add to `internal/provider/firecracker/port_race_test.go`:

```go
// TestPublishPort_AfterStopSentinel_FailsFast: once StopSandbox has flipped
// the stopped sentinel, a subsequent PublishPort on that VM must fail fast
// with ErrVMStopped (not block, not succeed). The sentinel is set directly
// here to simulate the instant StopSandbox begins; this deterministically
// tests that PublishPort consults lockFor's fast path. No real StopSandbox
// is invoked, so there is no signal/graceful-wait risk.
func TestPublishPort_AfterStopSentinel_FailsFast(t *testing.T) {
	addDNATFn = func(int, string, int) error { return nil }
	defer func() { addDNATFn = network.AddDNAT }()

	dir := t.TempDir()
	p := &Provider{
		config:    Config{ChrootBase: dir, EnableJailer: false},
		subnets:   network.NewAllocator(),
		portAlloc: network.NewPortAllocator(),
		vms:       map[string]*VMInfo{},
		fileMu:    map[string]*vmFileLock{},
	}
	seed := &VMInfo{ID: "vm-s", PID: os.Getpid(), TapDevice: "fc-s", SubnetIdx: 0, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir("vm-s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath("vm-s")); err != nil {
		t.Fatal(err)
	}
	p.vmMu.Lock()
	p.vms["vm-s"] = seed
	// Simulate StopSandbox having just flipped the sentinel.
	p.fileMu["vm-s"] = &vmFileLock{stopped: true}
	p.vmMu.Unlock()

	_, err := p.PublishPort(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-s"},
		41000, domain.PublishPortOptions{})
	if !errors.Is(err, domain.ErrVMStopped) {
		t.Fatalf("PublishPort err = %v; want ErrVMStopped", err)
	}
}

// TestStopSandbox_CleansUpFileMuEntry: with a dead VM (PID=0 so the graceful
// block is skipped; no TapDevice/Ports so network+port cleanup is skipped),
// StopSandbox must delete the p.vms and p.fileMu entries for the VM. No
// signals are sent (PID is not > 0), so the test is safe.
func TestStopSandbox_CleansUpFileMuEntry(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{
		config: Config{ChrootBase: dir, EnableJailer: false},
		vms:    map[string]*VMInfo{},
		fileMu: map[string]*vmFileLock{},
	}
	seed := &VMInfo{ID: "vm-d", PID: 0, SubnetIdx: 0, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir("vm-d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath("vm-d")); err != nil {
		t.Fatal(err)
	}
	p.vmMu.Lock()
	p.vms["vm-d"] = seed
	p.fileMu["vm-d"] = &vmFileLock{}
	p.vmMu.Unlock()

	if err := p.StopSandbox(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-d"}, false); err != nil {
		t.Fatal(err)
	}

	p.vmMu.Lock()
	_, vmsHas := p.vms["vm-d"]
	_, fmHas := p.fileMu["vm-d"]
	p.vmMu.Unlock()
	if vmsHas {
		t.Error("p.vms[vm-d] not deleted by StopSandbox")
	}
	if fmHas {
		t.Error("p.fileMu[vm-d] not deleted by StopSandbox")
	}

	got, err := ReadVMInfo(p.vmInfoPath("vm-d"))
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 0 {
		t.Errorf("vminfo PID = %d; want 0 (ClearRuntime)", got.PID)
	}
}
```

Add `"errors"` to the import block of `port_race_test.go` if not already present.

These tests reference `p.fileMu`/`vmFileLock` (Task 3) and the `stopped` sentinel + `fileMu` deletion (Step 3). The fail-fast test fails (PublishPort does not yet consult lockFor) until Step 3 lands; the lifecycle test fails (`p.fileMu[vm-d]` still present) until Step 3 deletes the entry.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker -race ./internal/provider/firecracker/ -run TestPublishPort_DuringStop -v`
Expected: FAIL (the sentinel doesn't exist yet / PublishPort doesn't fail fast).

- [ ] **Step 3: Implement StopSandbox sentinel + lifecycle**

In `internal/provider/firecracker/sandbox.go`, at the very top of `StopSandbox` (before `stopBoostListener`):

```go
func (p *Provider) StopSandbox(ctx context.Context, ref domain.BackendRef, force bool) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "StopSandbox")
	defer func() { endSpan(retErr) }()

	vmID := ref.Ref

	// F1: flip the stopped sentinel and acquire the per-VM file lock for
	// the entire teardown. Late writers (PublishPort/UpdateResources) call
	// lockFor, observe stopped=true, and fail fast with ErrVMStopped
	// without blocking.
	p.vmMu.Lock()
	fl, ok := p.fileMu[vmID]
	if !ok {
		fl = &vmFileLock{}
		p.fileMu[vmID] = fl
	}
	fl.stopped = true
	p.vmMu.Unlock()
	fl.mu.Lock()
	defer fl.mu.Unlock()

	// ... existing body (stopBoostListener, ReadVMInfo, Stopping=true write,
	//     graceful-wait loop, ClearRuntime write) ...
```

All existing `info.Write(infoPath)` calls inside StopSandbox (the `Stopping=true` write at ~523, the `ClearRuntime` write at ~577) now run *under* `fl.mu` — remove any redundant locking around them (they no longer need `vmMu` for the file write; `vmMu` is still used for the `p.vms` map delete at the end).

At the very end, where `delete(p.vms, vmID)` happens, also delete the file-lock entry:

```go
p.vmMu.Lock()
delete(p.vms, vmID)
delete(p.fileMu, vmID) // F1: lock lifetime == VM lifetime
p.vmMu.Unlock()
```

- [ ] **Step 4: Convert the other `sandbox.go` vminfo writers**

For each remaining `info.Write(infoPath)` / `ReadVMInfo → mutate → Write` in `sandbox.go` (lines 124 CreateSandbox, 306 StartSandbox, 469, 731), wrap in `lockFor`. Pattern:

```go
fl, err := p.lockFor(vmID)
if err != nil {
	return fmt.Errorf("firecracker <op> %s: %w", vmID, err)
}
defer fl.mu.Unlock()
// ... existing ReadVMInfo → mutate → Write ...
```

For `CreateSandbox` (line 124), the VM may not be in `p.vms` yet — `lockFor` lazy-creates the `fileMu` entry, so this works. Ensure the `fileMu` entry is not deleted prematurely (it should live until `StopSandbox`).

For `StartSandbox` (line 306), the existing `info.Write(infoPath)` happens *before* `p.vmMu.Lock()` — keep that ordering but wrap the write in `lockFor` first (the `p.vms[vmID] = info` map insert stays under `vmMu` as today; the file write is under `fl.mu`). Acquire `fl.mu` at the top of the relevant block, then `vmMu` for the map insert — consistent with the lock-ordering rule (fileMu before vmMu).

- [ ] **Step 5: Convert `UpdateResources` commit (sandbox_resize.go ~127-145)**

Current code releases `vmMu` before `info.Write`. Replace with: hold `fl.mu` (via `lockFor`) across the whole commit block, keep `vmMu` for the in-memory `info.LimitCPU`/`LimitMemMib` mutation *inside* `fl.mu`. If `lockFor` returns `ErrVMStopped`, return a `*domain.ProviderResizeError{Reason: ResizeReasonVMStopped}`:

```go
fl, err := p.lockFor(ref.Ref)
if err != nil {
	return &domain.ProviderResizeError{
		Reason: domain.ResizeReasonVMStopped,
		Detail: err.Error(),
	}
}
defer fl.mu.Unlock()

p.vmMu.Lock()
if req.CPULimit != nil { info.LimitCPU = int64(*req.CPULimit) }
if req.MemoryLimitMB != nil { info.LimitMemMib = newMem }
p.vmMu.Unlock()

if err := info.Write(p.vmInfoPath(ref.Ref)); err != nil {
	// ... existing revert logic, keeping fl.mu held ...
}
```

(The revert block stays inside `defer fl.mu.Unlock()` scope — correct, since the revert also writes vminfo and mutates `info`.)

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -tags firecracker -race ./internal/provider/firecracker/ -run TestPublishPort_DuringStop -v`
Expected: PASS.

Run the full firecracker suite:
`go test -tags firecracker -race ./internal/provider/firecracker/...`
Expected: PASS (existing resize tests must still pass; if any break, the lock ordering or the CreateSandbox lazy-create path is the likely culprit — re-check Step 4).

- [ ] **Step 7: Commit**

```bash
git add internal/provider/firecracker/sandbox.go internal/provider/firecracker/sandbox_resize.go internal/provider/firecracker/port_race_test.go
git commit -m "fix(firecracker): StopSandbox sentinel + convert sandbox/resize vminfo writers to lockFor"
```

---

## Task 6: Convert remaining vminfo writers (snapshot.go, fork.go)

**Files:**
- Modify: `internal/provider/firecracker/snapshot.go` (line ~266 vminfo write; also the `createLiveSnapshot` `fcsdk.NewMachine` replacement happens in Task 8)
- Modify: `internal/provider/firecracker/fork.go` (line ~195)

**Interfaces:**
- Consumes: `Provider.lockFor` (Task 3).
- Produces: all remaining vminfo writers are serialized.

- [ ] **Step 1: Identify the exact writer sites**

Run:
```bash
grep -n "ReadVMInfo\|info.Write(" internal/provider/firecracker/snapshot.go internal/provider/firecracker/fork.go | grep -v "_test.go"
```

- [ ] **Step 2: Convert each site to `lockFor`**

For each `ReadVMInfo → mutate → info.Write` site in `snapshot.go` (around line 260-266) and `fork.go` (around line 195), apply the same pattern:

```go
fl, err := p.lockFor(vmID)
if err != nil {
	return fmt.Errorf("firecracker <op> %s: %w", vmID, err)
}
defer fl.mu.Unlock()
// ... existing ReadVMInfo → mutate → Write ...
```

Use the correct `vmID` for each site (snapshot uses `vmID`; fork uses the relevant VM ID — verify at the site).

- [ ] **Step 3: Verify build and tests**

Run: `go build -tags firecracker ./internal/provider/firecracker/...`
Run: `go test -tags firecracker -race ./internal/provider/firecracker/...`
Expected: PASS (no new tests needed — these sites are covered by existing snapshot/fork tests if they exist; if no test exists, the lockFor conversion is verified by the race detector running the existing suite).

- [ ] **Step 4: Commit**

```bash
git add internal/provider/firecracker/snapshot.go internal/provider/firecracker/fork.go
git commit -m "fix(firecracker): serialize snapshot/fork vminfo writers via lockFor"
```

---

## Task 7: F12 — `transientFirecrackerClient` helper + tests

**Files:**
- Create: `internal/provider/firecracker/fcapi_transport.go`
- Create: `internal/provider/firecracker/fcapi_transport_test.go`

**Interfaces:**
- Consumes: `github.com/firecracker-microvm/firecracker-go-sdk/client` (`NewHTTPClient`), `github.com/go-openapi/strfmt` (`NewFormats`), `github.com/go-openapi/runtime/client` (`New`), `github.com/go-openapi/runtime` (`ClientTransport`).
- Produces:
  - `transientFirecrackerClient(sockPath string, idleTimeout time.Duration) (*client.Firecracker, error)` — builds a `*client.Firecracker` whose `Transport` is an idle-reaping unix-socket transport.
  - `buildIdleReapingTransport(sockPath string, idleTimeout time.Duration) runtime.ClientTransport` — the transport builder (testable in isolation).
  - `validateSockPath(sockPath string) error` — guards against empty/invalid paths.

- [ ] **Step 1: Write the failing test**

Create `internal/provider/firecracker/fcapi_transport_test.go`:

```go
//go:build firecracker

package firecracker

import (
	"net"
	"net/http"
	"testing"
	"time"

	httptransport "github.com/go-openapi/runtime/client"
)

func TestBuildIdleReapingTransport_SetsIdleConnTimeout(t *testing.T) {
	tr := buildIdleReapingTransport("/tmp/fc.sock", 30*time.Second)

	// The transport is a go-openapi runtime.ClientTransport wrapping an
	// *http.Transport. go-openapi's httptransport.Transport exposes the
	// underlying transport via its Transport field.
	ht, ok := tr.(*httptransport.Transport)
	if !ok {
		t.Fatalf("transport = %T; want *httptransport.Transport", tr)
	}
	socketTransport, ok := ht.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("inner transport = %T; want *http.Transport", ht.Transport)
	}
	if socketTransport.IdleConnTimeout != 30*time.Second {
		t.Errorf("IdleConnTimeout = %v; want 30s", socketTransport.IdleConnTimeout)
	}
	if socketTransport.MaxIdleConnsPerHost != 1 {
		t.Errorf("MaxIdleConnsPerHost = %d; want 1", socketTransport.MaxIdleConnsPerHost)
	}
}

func TestTransientFirecrackerClient_RejectsEmptyPath(t *testing.T) {
	_, err := transientFirecrackerClient("", 30*time.Second)
	if err == nil {
		t.Fatalf("expected error for empty socket path")
	}
}

func TestTransientFirecrackerClient_ReapsIdleConn(t *testing.T) {
	// Stand up a unix socket server that accepts one connection and
	// responds with a minimal HTTP/1.1 204 to any request.
	dir := t.TempDir()
	sockPath := dir + "/fc.sock"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				_, _ = c.Read(buf) // drain request
				_, _ = c.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
			}(c)
		}
	}()

	fc, err := transientFirecrackerClient(sockPath, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	_ = fc // a real PatchBalloon round-trip would go here; the reap is
	// verified by observing the underlying transport's IdleConnTimeout
	// (covered by TestBuildIdleReapingTransport_SetsIdleConnTimeout).
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/ -run TestBuildIdleReapingTransport -v`
Expected: FAIL with `buildIdleReapingTransport undefined`.

- [ ] **Step 3: Implement the helper**

Create `internal/provider/firecracker/fcapi_transport.go`:

```go
package firecracker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-openapi/runtime"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/firecracker-microvm/firecracker-go-sdk/client"
	"github.com/go-openapi/strfmt"
)

// transientFirecrackerClient builds a low-level *client.Firecracker bound to
// a running VM's API socket with an idle-reaping transport. The caller
// issues one or two operations then lets it fall out of scope; the idle
// unix connection auto-reaps after idleTimeout, preventing FD retention
// between GC cycles. Use for one-shot API-socket calls; do NOT use for the
// long-lived Machine that manages a VMM lifecycle.
//
// F12: replaces fcsdk.NewMachine + thin SDK wrappers (UpdateBalloon,
// CreateSnapshot, Shutdown, PauseVM, ResumeVM) at the 3 transient call
// sites. The long-lived launch Machine is unchanged.
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

func validateSockPath(sockPath string) error {
	if sockPath == "" {
		return fmt.Errorf("transientFirecrackerClient: socket path is empty")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags firecracker ./internal/provider/firecracker/ -run "TestBuildIdleReapingTransport|TestTransientFirecrackerClient" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/fcapi_transport.go internal/provider/firecracker/fcapi_transport_test.go
git commit -m "feat(firecracker): add transientFirecrackerClient with idle-reaping transport"
```

---

## Task 8: Replace `fcsdk.NewMachine` at the 3 transient call sites

**Files:**
- Modify: `internal/provider/firecracker/sandbox_resize.go` (`patchBalloon` ~170-200)
- Modify: `internal/provider/firecracker/snapshot.go` (`createLiveSnapshot` ~140-210)
- Modify: `internal/provider/firecracker/sandbox.go` (graceful-stop Shutdown ~530-540)

**Interfaces:**
- Consumes: `transientFirecrackerClient` (Task 7), `client/operations` (`NewPatchBalloonParams`, `NewCreateSnapshotParams`, `NewPatchVMParams`, `NewCreateSyncActionParams`), `client/models` (`BalloonUpdate`, `SnapshotCreateParams`, `VM`, `InstanceActionInfo`, `VMStatePaused`, `VMStateResumed`, `InstanceActionInfoActionTypeSendCtrlAltDel`).

- [ ] **Step 1: Replace `patchBalloon`**

In `internal/provider/firecracker/sandbox_resize.go`, replace:

```go
machine, err := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
if err != nil {
	return fmt.Errorf("attach to vm: %w", err)
}
// ... retry loop calling machine.UpdateBalloon(ctx, amountMib) ...
```

with:

```go
fc, err := transientFirecrackerClient(sockPath, 30*time.Second)
if err != nil {
	return fmt.Errorf("attach to vm: %w", err)
}
amountMibVal := amountMib
// ... retry loop:
//   params := operations.NewPatchBalloonParams().WithContext(ctx).
//       WithBody(&models.BalloonUpdate{AmountMib: &amountMibVal})
//   if _, err := fc.Operations.PatchBalloon(params); err == nil { return nil } else { ... }
```

Add the new imports to `sandbox_resize.go`:

```go
"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
"github.com/firecracker-microvm/firecracker-go-sdk/client/operations"
```

Remove the now-unused `fcsdk` import if this was the only use in the file (run `grep -n "fcsdk" internal/provider/firecracker/sandbox_resize.go` to check).

- [ ] **Step 2: Verify build + run patchBalloon-related tests**

Run: `go build -tags firecracker ./internal/provider/firecracker/...`
Run: `go test -tags firecracker -race ./internal/provider/firecracker/ -run TestUpdateResources -v`
Expected: PASS (existing resize tests should pass; the API surface is identical).

- [ ] **Step 3: Replace `createLiveSnapshot`**

In `internal/provider/firecracker/snapshot.go`, replace `fcsdk.NewMachine` + `machine.PauseVM` / `machine.CreateSnapshot` / `machine.ResumeVM` with `transientFirecrackerClient` + low-level ops:

```go
fc, err := transientFirecrackerClient(sockPath, 30*time.Second)
if err != nil {
	return nil, fmt.Errorf("firecracker live snapshot connect %s: %w", vmID, err)
}

// Pause
if _, err := fc.Operations.PatchVM(
	operations.NewPatchVMParams().WithContext(ctx).
		WithBody(&models.VM{State: models.VMStatePaused}),
); err != nil {
	return nil, fmt.Errorf("firecracker pause %s: %w", vmID, err)
}

// ... existing defer-resume-on-error cleanup, using:
//   fc.Operations.PatchVM(operations.NewPatchVMParams().
//     WithContext(cleanupCtx).WithBody(&models.VM{State: models.VMStateResumed}))
//
// CreateSnapshot:
//   fc.Operations.CreateSnapshot(operations.NewCreateSnapshotParams().
//     WithContext(ctx).WithBody(&models.SnapshotCreateParams{
//       MemFilePath:  models.String(memFile),
//       SnapshotPath: models.String(snapMeta),
//     }))
//
// Resume:
//   fc.Operations.PatchVM(operations.NewPatchVMParams().
//     WithContext(ctx).WithBody(&models.VM{State: models.VMStateResumed}))
```

Note: `models.String` is the SDK's pointer-to-string helper used by the SDK wrappers (see `machine.go` PauseVM uses `String(...)` — confirm the exact name with `grep -n "^func String" /home/eran/go/pkg/mod/github.com/firecracker-microvm/firecracker-go-sdk@v1.0.0/*.go`). If it's `models.String`, use that; if it's `firecracker.String`, import the root package.

Add imports: `client/models`, `client/operations`. Remove `fcsdk` if unused in this file after the change.

- [ ] **Step 4: Verify build + snapshot tests**

Run: `go build -tags firecracker ./internal/provider/firecracker/...`
Run: `go test -tags firecracker -race ./internal/provider/firecracker/ -run "Snapshot" -v`
Expected: PASS.

- [ ] **Step 5: Replace graceful-stop `Shutdown`**

In `internal/provider/firecracker/sandbox.go` (around line 536), replace:

```go
machine, merr := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
if merr == nil {
	machine.Shutdown(ctx)
}
```

with:

```go
fc, merr := transientFirecrackerClient(sockPath, 30*time.Second)
if merr == nil {
	actionType := models.InstanceActionInfoActionTypeSendCtrlAltDel
	_, _ = fc.Operations.CreateSyncAction(
		operations.NewCreateSyncActionParams().
			WithContext(ctx).
			WithInfo(&models.InstanceActionInfo{ActionType: &actionType}),
	)
}
```

Preserve the existing error-swallowing behavior (the `if merr == nil { ... }` pattern) to avoid behavior change — flagged as a separate latent bug in the spec. Add imports: `client/models`, `client/operations`. Remove `fcsdk` from `sandbox.go` only if no other use remains (the long-lived launch Machine still uses `fcsdk` — run `grep -n "fcsdk" internal/provider/firecracker/sandbox.go` before removing).

- [ ] **Step 6: Verify build + full firecracker suite**

Run: `go build -tags firecracker ./...`
Run: `go test -tags firecracker -race ./internal/provider/firecracker/...`
Expected: PASS.

Run the verification grep:
```bash
grep -rn "fcsdk.NewMachine" internal/provider/firecracker/
```
Expected: only the long-lived launch Machine sites remain (e.g. in `StartSandbox`/`CreateSandbox`), NOT the 3 transient sites.

- [ ] **Step 7: Commit**

```bash
git add internal/provider/firecracker/sandbox_resize.go internal/provider/firecracker/snapshot.go internal/provider/firecracker/sandbox.go
git commit -m "fix(firecracker): replace transient fcsdk.NewMachine with idle-reaping client (F12)"
```

---

## Task 9: F7 — `sbxLocks` field + `lockSandbox` helper

**Files:**
- Modify: `internal/service/boost.go` — add `sbxLocks` field, init in `NewBoostService`, add `lockSandbox` helper.
- Test: `internal/service/boost_test.go` — add a unit test for lazy creation.

**Interfaces:**
- Consumes: `s.mu` (existing), `s.sbxLocks` (new).
- Produces: `(*BoostService).lockSandbox(sandboxID string) *sync.Mutex` — must be called while holding `s.mu`; returns the per-sandbox mutex (not yet locked). Caller locks it, releases `s.mu`, does the slow apply, then re-acquires `s.mu` for timer work.

- [ ] **Step 1: Write the failing test**

Create `internal/service/boost_internal_test.go` (white-box `package service`, since `lockSandbox` is unexported — the existing `boost_test.go` is black-box `package service_test` and cannot reach unexported fields):

```go
// internal/service/boost_internal_test.go
package service

import (
	"sync"
	"testing"
)

func TestLockSandbox_LazyCreation(t *testing.T) {
	bs := &BoostService{timers: map[string]Timer{}, sbxLocks: map[string]*sync.Mutex{}}
	bs.mu.Lock()
	m1 := bs.lockSandbox("sbx-1")
	m2 := bs.lockSandbox("sbx-1")
	bs.mu.Unlock()

	if m1 != m2 {
		t.Fatalf("lockSandbox returned different mutexes for the same sandbox")
	}
	bs.mu.Lock()
	m3 := bs.lockSandbox("sbx-2")
	bs.mu.Unlock()
	if m1 == m3 {
		t.Fatalf("lockSandbox returned the same mutex for different sandboxes")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestLockSandbox -v`
Expected: FAIL with `bs.sbxLocks undefined` / `bs.lockSandbox undefined`.

- [ ] **Step 3: Implement `sbxLocks` field + `lockSandbox`**

In `internal/service/boost.go`, add the field:

```go
type BoostService struct {
	// ... existing fields ...
	mu       sync.Mutex
	timers   map[string]Timer
	sbxLocks map[string]*sync.Mutex // F7: per-sandbox apply lock; guarded by mu for lookup
}
```

In `NewBoostService`, init the map:

```go
return &BoostService{
	// ... existing fields ...
	timers:   make(map[string]Timer),
	sbxLocks: make(map[string]*sync.Mutex),
}
```

Add the helper:

```go
// lockSandbox returns the per-sandbox boost lock. Must be called while
// holding s.mu (for the lookup); the returned mutex is acquired standalone
// so slow UpdateResources calls don't hold s.mu. F7: serializes Start and
// expire for the same sandbox across their UpdateResources apply.
func (s *BoostService) lockSandbox(sandboxID string) *sync.Mutex {
	m, ok := s.sbxLocks[sandboxID]
	if !ok {
		m = &sync.Mutex{}
		s.sbxLocks[sandboxID] = m
	}
	return m
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/service/ -run TestLockSandbox -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/boost.go internal/service/boost_internal_test.go
git commit -m "feat(service): add BoostService.sbxLocks + lockSandbox helper (F7)"
```

---

## Task 10: F7 — Restructure `Start` and `expire` to two-phase locking

**Files:**
- Modify: `internal/service/boost.go` — `Start` (61-160) and `expire` (172-235).
- Test: `internal/service/boost_race_test.go` (new).

**Interfaces:**
- Consumes: `lockSandbox` (Task 9), the existing `s.mu` + `timers` map.
- Produces: `Start` and `expire` hold `sbxMu` across their `UpdateResources` call. Different sandboxes run concurrently. `sbxLocks` entries are deleted on successful expire / Cancel / CleanupForSandbox.

- [ ] **Step 1: Write the failing race test**

Create `internal/service/boost_race_test.go`:

```go
package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

// TestBoostStartVsExpire_SerializeApply verifies the F7 fix: Start(B) and
// expire(A) for the same sandbox must serialize their UpdateResources apply
// calls via the per-sandbox lock. Without the fix, both applies run
// concurrently (no shared lock), so the test observes Start(B)'s apply while
// expire(A) is still mid-apply. With the fix, Start(B) blocks on sbxMu until
// expire(A) releases it, so Start(B)'s apply cannot overlap expire(A)'s.
//
// Mechanism: UpdateResourcesFn signals applyCh (cpu value) then blocks on
// releaseCh, letting the test hold each apply open. The test starts A, fires
// expire(A) (held open), then starts B and asserts B does NOT apply while
// expire(A) is held. It then releases expire(A) and asserts B applies next.
func TestBoostStartVsExpire_SerializeApply(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx-ser", domain.SandboxRunning, "mock")

	applyCh := make(chan int)       // signals an apply started (cpu value)
	releaseCh := make(chan struct{}) // test gates each apply's return
	var mu sync.Mutex
	var order []int
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		cpu := -1
		if req.CPULimit != nil {
			cpu = *req.CPULimit
		}
		mu.Lock()
		order = append(order, cpu)
		mu.Unlock()
		applyCh <- cpu
		<-releaseCh
		return nil
	}

	cpuA := 2
	if _, err := env.boost.Start(context.Background(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	<-applyCh // Start(A) apply started (cpu=2); let it finish.
	releaseCh <- struct{}{}

	// Launch expire(A) in a goroutine (fire the timer). Its revert apply starts
	// and blocks on releaseCh, holding the per-sandbox lock (with the fix).
	go clk.fire(61 * time.Second)
	<-applyCh // expire(A) revert started; it now holds the per-sandbox lock.

	// Launch Start(B) in a goroutine. With the fix, Start(B) blocks on the
	// per-sandbox lock (expire holds it), so its apply does NOT start.
	// Without the fix, Start(B) applies concurrently and sends on applyCh.
	done := make(chan error, 1)
	go func() {
		cpuB := 4
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		done <- err
	}()
	select {
	case <-applyCh:
		t.Fatal("Start(B) applied concurrently with expire(A); per-sandbox lock did not serialize (F7 not fixed)")
	case <-time.After(100 * time.Millisecond):
		// Good: Start(B) is blocked on the per-sandbox lock.
	}

	// Release expire(A); it finishes and releases the per-sandbox lock.
	// Start(B) then acquires the lock and applies cpu=4.
	releaseCh <- struct{}{}
	select {
	case cpu := <-applyCh:
		if cpu != 4 {
			t.Fatalf("after expire release, apply cpu = %d; want 4 (B)", cpu)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start(B) did not apply after expire released")
	}
	releaseCh <- struct{}{} // let Start(B) finish
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start(B): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start(B) did not complete")
	}

	mu.Lock()
	last := order[len(order)-1]
	mu.Unlock()
	if last != 4 {
		t.Fatalf("last apply = %d; want 4 (B's limit)", last)
	}
}
```

Before the fix, the first `select` receives from `applyCh` (Start(B) applied concurrently) and the test fails. With the fix, Start(B) blocks on the per-sandbox lock, the first `select` times out, and after releasing expire(A), B's apply (cpu=4) is the last one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/service/ -run TestBoostStartVsExpire -v`
Expected: FAIL (the last apply is the original, not B's limit).

- [ ] **Step 3: Restructure `Start` (boost.go:61-160)**

Currently `Start` holds `s.mu` for the entire body via `defer s.mu.Unlock()` at line 95. Restructure:

```go
func (s *BoostService) Start(ctx context.Context, opts StartBoostOpts) (*domain.Boost, error) {
	// ... validation (lines 62-90, unchanged) ...

	// Phase 1: bookkeeping under s.mu (cancel prior, upsert new boost row).
	s.mu.Lock()
	if prior, err := s.boosts.Get(ctx, opts.SandboxID); err == nil {
		if t, ok := s.timers[prior.BoostID]; ok { t.Stop(); delete(s.timers, prior.BoostID) }
		if err := s.boosts.Delete(ctx, prior.BoostID); err != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("delete prior boost: %w", err)
		}
	}
	now := s.clock.Now().UTC()
	boost := &domain.Boost{ /* ... same as today ... */ }
	if err := s.boosts.Upsert(ctx, boost); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("persist boost: %w", err)
	}
	sbxMu := s.lockSandbox(sbx.SandboxID) // lookup while s.mu held
	s.mu.Unlock()

	// Phase 2: apply under sbxMu (NOT s.mu). defer sbxMu.Unlock() so it
	// stays held through the rollback path too.
	sbxMu.Lock()
	defer sbxMu.Unlock()

	_, err = s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{ /* same as today */ })
	if err != nil {
		// rollback boost row — re-acquire s.mu briefly for the delete
		s.mu.Lock()
		if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("apply boost failed: %v; rollback also failed: %w", err, delErr)
		}
		s.mu.Unlock()
		return nil, err
	}

	// Phase 3: schedule timer under s.mu.
	s.mu.Lock()
	s.timers[boost.BoostID] = s.clock.AfterFunc(dur, func() {
		s.expire(context.Background(), boost.BoostID)
	})
	s.mu.Unlock()

	_ = s.events.Publish(ctx, /* same as today */)
	return boost, nil
}
```

- [ ] **Step 4: Restructure `expire` (boost.go:172-235)**

Currently: `s.mu.Lock(); delete(s.timers, boostID); s.mu.Unlock()` then unlocked `GetByID` + `UpdateResources`. Restructure:

```go
func (s *BoostService) expire(ctx context.Context, boostID string) {
	// Phase 1: delete timer + fetch boost row + look up sbxMu, all under s.mu.
	s.mu.Lock()
	delete(s.timers, boostID)
	boost, err := s.boosts.GetByID(ctx, boostID)
	if err != nil {
		s.mu.Unlock()
		return // boost cancelled/deleted while timer fired
	}
	sbxMu := s.lockSandbox(boost.SandboxID)
	s.mu.Unlock()

	// Phase 2: apply under sbxMu.
	sbxMu.Lock()
	defer sbxMu.Unlock()

	sbx, err := s.sandboxes.Get(ctx, boost.SandboxID)
	if err != nil {
		// sandbox gone; clean up boost row + lock
		s.mu.Lock()
		_ = s.boosts.Delete(ctx, boostID)
		delete(s.sbxLocks, boost.SandboxID)
		s.mu.Unlock()
		return
	}
	if sbx.State != domain.SandboxRunning {
		s.mu.Lock()
		_ = s.boosts.Delete(ctx, boostID)
		delete(s.sbxLocks, boost.SandboxID)
		s.mu.Unlock()
		s.emitExpired(ctx, boost, "sandbox_not_running", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

	_, applyErr := s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{ /* same as today */ })
	if applyErr == nil {
		s.mu.Lock()
		_ = s.boosts.Delete(ctx, boostID)
		delete(s.sbxLocks, boost.SandboxID) // F7: boost ended; release lock
		s.mu.Unlock()
		s.emitExpired(ctx, boost, "expired", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}
	// ... failure/retry path: re-acquire s.mu for the timers map mutation
	// (lines 230-236), same as today. The retry re-schedules expire, which
	// will re-acquire sbxMu — fine.
	attempts := boost.RevertAttempts + 1
	if attempts > len(boostBackoff) {
		s.mu.Lock()
		_ = s.boosts.UpdateState(ctx, boostID, domain.BoostRevertFailed, attempts, applyErr.Error())
		s.mu.Unlock()
		// ... publish EventBoostRevertFailed under s.mu or unlocked as today ...
		return
	}
	s.mu.Lock()
	_ = s.boosts.UpdateState(ctx, boostID, domain.BoostActive, attempts, applyErr.Error())
	s.timers[boostID] = s.clock.AfterFunc(boostBackoff[attempts-1], func() {
		s.expire(context.Background(), boostID)
	})
	s.mu.Unlock()
}
```

- [ ] **Step 5: Add `sbxLocks` cleanup to `Cancel` and `CleanupForSandbox`**

In `Cancel` (boost.go ~271-276) and `CleanupForSandbox` (~352-357), after `delete(s.timers, boost.BoostID)`, also `delete(s.sbxLocks, boost.SandboxID)` under `s.mu`. (Defense-in-depth per the spec; these paths don't call UpdateResources so the lock isn't strictly required, but deleting the entry on cancel/cleanup keeps the map from leaking.)

- [ ] **Step 6: Run race test + full boost suite**

Run: `go test -race ./internal/service/ -run TestBoostStartVsExpire -v`
Expected: PASS.

Run: `go test -race ./internal/service/...`
Expected: PASS (all existing boost tests must still pass — if any break, the lock re-acquire ordering in Phase 3 or the rollback path is the likely culprit).

- [ ] **Step 7: Commit**

```bash
git add internal/service/boost.go internal/service/boost_race_test.go
git commit -m "fix(service): serialize boost Start/expire UpdateResources per-sandbox (F7)"
```

---

## Task 11: Full-suite verification + cleanup

**Files:**
- None modified (verification only) unless the verification greps surface a missed site.

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 2: Race the affected packages**

Run: `go test -race -tags firecracker ./internal/provider/firecracker/... ./internal/service/... ./internal/api/... ./internal/domain/...`
Expected: PASS.

- [ ] **Step 3: Race the whole repo (informational)**

Run: `go test -race ./... 2>&1 | tail -40`
Expected: no new failures attributable to this batch. (Pre-existing failures in unrelated packages are out of scope; note them but do not fix.)

- [ ] **Step 4: Verification greps**

Run:
```bash
# F1: no vminfo writer should be outside lockFor (except recover()'s documented single-threaded write).
grep -rn "info.Write(\|\.Write(infoPath\|\.Write(p.vmInfoPath" internal/provider/firecracker/*.go | grep -v "_test.go" | grep -v "recover()"
# F12: fcsdk.NewMachine only at the long-lived launch Machine.
grep -rn "fcsdk.NewMachine" internal/provider/firecracker/*.go | grep -v "_test.go"
# F7: sbxLocks deleted on expire/cancel/cleanup.
grep -n "delete(s.sbxLocks" internal/service/boost.go
```

Manually review the F1 output: every `info.Write` site should be inside a `lockFor` block (or be the documented `recover()` write). If any site is missed, add a fix-up commit converting it.

- [ ] **Step 5: Update the design doc's status (optional)**

In `docs/superpowers/specs/2026-08-04-fc-concurrency-batch1-design.md`, change `**Status:** Draft (pending approval)` to `**Status:** Implemented` and commit:

```bash
git add docs/superpowers/specs/2026-08-04-fc-concurrency-batch1-design.md
git commit -m "docs: mark fc-concurrency batch 1 design as implemented"
```

- [ ] **Step 6: Final commit (if any fix-ups from Step 4)**

If Step 4 surfaced missed sites, commit them. Otherwise this step is a no-op.

---

## Self-Review (completed by plan author)

**1. Spec coverage:**
- F1 per-VM lock: Tasks 3 (helper), 4 (port + recover), 5 (stop sentinel + sandbox/resize writers), 6 (snapshot/fork writers). ✅
- F1 fail-fast sentinel + 30s-hold: Task 5 Step 3. ✅
- F1 `ErrVMStopped` + 409 mapping: Task 1. ✅
- F1 `ResizeReasonVMStopped` + 409 in resize path: Task 2 (constant) + Task 5 Step 5 (usage). ✅
- F1 `recover()` carve-out (create entry + documented no-lock write): Task 4 Step 5. ✅
- F12 transient idle-reaping client: Task 7 (helper) + Task 8 (3 call sites). ✅
- F12 socket-path-as-arg (helper takes sockPath): Task 7 Step 3 signature. ✅
- F7 `sbxLocks` + `lockSandbox`: Task 9. ✅
- F7 two-phase `s.mu`→`sbxMu` in Start + expire: Task 10. ✅
- F7 `sbxLocks` cleanup on expire/cancel/cleanup: Task 10 Step 5. ✅
- Worktree + feature branch: Global Constraints. ✅
- Testing gate (`go test -race`): Task 11 Step 2. ✅

**2. Placeholder scan:** No `TBD`/`TODO`/`t.Skip("skeleton...")` placeholders remain — the three test steps that were scaffolds during drafting (Tasks 4, 5, 10) were hardened with concrete, deterministic test code during pre-flight. The `lockFor` design was also corrected during pre-flight to check `stopped` under `vmMu` *before* acquiring `fl.mu` (so fail-fast works without blocking while StopSandbox holds `fl.mu` through its 30s teardown); Task 3 gained `TestLockFor_FailFastWhileStopHoldsLock` to pin that behavior.

**3. Type consistency:**
- `vmFileLock{mu sync.Mutex, stopped bool}` — consistent across Task 3 (def), Task 4 (usage), Task 5 (usage). ✅
- `lockFor(vmID string) (*vmFileLock, error)` — consistent. ✅
- `transientFirecrackerClient(sockPath string, idleTimeout time.Duration) (*client.Firecracker, error)` — consistent across Task 7 (def) + Task 8 (usage). ✅
- `lockSandbox(sandboxID string) *sync.Mutex` — consistent across Task 9 (def) + Task 10 (usage). ✅
- `domain.ErrVMStopped` — consistent across Task 1 (def), Task 3 (usage), Task 5 (usage). ✅
- `domain.ResizeReasonVMStopped` — consistent across Task 2 (def), Task 5 (usage). ✅
- SDK model/string helper: flagged for verification in Task 8 Step 3 (`models.String` vs `firecracker.String`) — the plan instructs the implementer to confirm with a grep. ✅

**4. Scope check:** Plan covers exactly the 3 fixes in the spec. No scope creep. ✅

No issues found that require inline fixes beyond what's already documented. Plan is ready.
