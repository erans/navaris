# Firecracker Concurrency Batch 1 Corrections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct the final-review defects in F1 and F7 so Firecracker VM state transitions and boost operations are fully serialized, then make the complete race gate pass.

**Architecture:** Persisted `vminfo.json` state is authoritative: same-VM operations hold one per-VM lock from fresh disk read through live apply and commit, while an atomic stop sentinel rejects late writers. Boost Start, expiry, Cancel, and lifecycle cleanup use a ref-counted keyed lock whose identity remains stable for every overlapping same-sandbox operation; bookkeeping and live apply occur under that lock in one order.

**Tech Stack:** Go 1.26.1, `sync.Mutex`, `sync/atomic.Bool`, SQLite boost store, Firecracker provider build tag, Go race detector.

## Global Constraints

- Work only in `/home/eran/work/navaris/.worktrees/fc-concurrency-batch1` on branch `fix/fc-concurrency-batch1`.
- Follow TDD for every production change: add a failing deterministic test, run it and observe the expected failure, implement the minimum correction, rerun, then commit.
- `vminfo.json` is authoritative for persisted `VMInfo`; no existing-file writer may pass a stale pointer from `p.vms` to `VMInfo.Write`.
- Lock ordering when locks overlap is `vmFileLock.mu → Provider.vmMu` and `sandboxLock.mu → BoostService.mu`.
- `Provider.vmMu` and `BoostService.mu` must not be held while waiting for a per-resource mutex or while calling iptables, cgroup, Firecracker socket, boost-store, event-bus, or provider operations.
- Same-resource operations serialize completely; different VMs and different sandboxes remain concurrent.
- Keep `//go:build firecracker` on every Firecracker production and test file.
- Do not change F12 production code. The real idle-reap integration-test enhancement remains deferred.
- Do not change public API shapes or the database schema. Preserve the implemented HTTP 409 mapping for `ErrVMStopped` and `ResizeReasonVMStopped`.
- The final gate is:

```bash
go build ./...
go build -tags firecracker ./...
go test -race -tags firecracker \
  ./internal/provider/firecracker/... \
  ./internal/service/... \
  ./internal/api/... \
  ./internal/domain/...
```

---

## File Map

- `internal/provider/firecracker/vminfo.go` — `vmFileLock` definition and atomic stop sentinel.
- `internal/provider/firecracker/vminfo_lock.go` — fail-fast per-VM lock acquisition.
- `internal/provider/firecracker/vminfo_lock_test.go` — sentinel synchronization and lock behavior tests.
- `internal/provider/firecracker/sandbox.go` — StopSandbox sentinel store.
- `internal/provider/firecracker/sandbox_resize.go` — disk-authoritative resize transaction and CPU-apply test seam.
- `internal/provider/firecracker/sandbox_resize_test.go` — fresh-disk, port-preservation, and resize-order tests.
- `internal/provider/firecracker/port.go` — cache synchronization after successful port persistence.
- `internal/provider/firecracker/port_race_test.go` — existing F1 regression coverage and atomic test setup updates.
- `internal/provider/firecracker/network/port_allocator.go` — concurrency-safe `InUse` observation used to verify allocator consistency.
- `internal/provider/firecracker/network/port_allocator_test.go` — `InUse` behavior coverage.
- `internal/service/boost.go` — ref-counted lock registry and linearized Start/expire/Cancel/lifecycle flows.
- `internal/service/boost_internal_test.go` — white-box registry lifetime tests.
- `internal/service/boost_race_test.go` — deterministic Start/Start, Start/expire, Start/Cancel, and cross-sandbox ordering tests.
- `internal/provider/firecracker/boost_listener_test.go` — remove the pre-existing test-only data race.

---

### Task 1: Make the VM stop sentinel atomic

**Files:**
- Modify: `internal/provider/firecracker/vminfo.go:124-135`
- Modify: `internal/provider/firecracker/vminfo_lock.go:14-40`
- Modify: `internal/provider/firecracker/sandbox.go:553-570`
- Modify: `internal/provider/firecracker/vminfo_lock_test.go`
- Modify: `internal/provider/firecracker/port_race_test.go:79-105`

**Interfaces:**
- Consumes: existing `Provider.fileMu map[string]*vmFileLock` and `domain.ErrVMStopped`.
- Produces: `vmFileLock.stopped atomic.Bool`; `lockFor(vmID string) (*vmFileLock, error)` retains its signature.

- [ ] **Step 1: Replace stopped-literal setup and add the failing concurrent re-check test**

In `vminfo_lock_test.go`, replace each `&vmFileLock{stopped: true}` with construction plus `Store(true)`. Add:

```go
func TestLockFor_RechecksStoppedAfterWaiting(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}
	fl := &vmFileLock{}
	p.fileMu["vm-gap"] = fl

	// Force lockFor past its fast check and into the per-VM mutex wait.
	fl.mu.Lock()
	done := make(chan error, 1)
	go func() {
		got, err := p.lockFor("vm-gap")
		if got != nil {
			got.mu.Unlock()
		}
		done <- err
	}()

	// Give the waiter time to complete the map lookup/fast check. The held
	// per-VM mutex keeps it from completing before the sentinel transition.
	time.Sleep(20 * time.Millisecond)
	fl.stopped.Store(true)
	fl.mu.Unlock()

	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrVMStopped) {
			t.Fatalf("lockFor error = %v; want ErrVMStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lockFor did not complete after sentinel transition")
	}
}
```

In `port_race_test.go`, change the fail-fast fixture to:

```go
fl := &vmFileLock{}
fl.stopped.Store(true)
p.fileMu["vm-s"] = fl
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test -race -tags firecracker ./internal/provider/firecracker/ \
  -run 'TestLockFor_(RechecksStoppedAfterWaiting|FailFastWhenStopped|FailFastWhileStopHoldsLock)' -count=1
```

Expected: compile failure because the current plain `bool` has no `Store` method.

- [ ] **Step 3: Implement the atomic sentinel**

In `vminfo.go`, import `sync/atomic` and change the type:

```go
type vmFileLock struct {
	mu      sync.Mutex
	stopped atomic.Bool
}
```

In `vminfo_lock.go`, use atomic loads for both checks:

```go
if fl.stopped.Load() {
	p.vmMu.Unlock()
	return nil, fmt.Errorf("firecracker: vm %s is stopping: %w", vmID, domain.ErrVMStopped)
}
p.vmMu.Unlock()

fl.mu.Lock()
if fl.stopped.Load() {
	fl.mu.Unlock()
	return nil, fmt.Errorf("firecracker: vm %s is stopping: %w", vmID, domain.ErrVMStopped)
}
```

In `StopSandbox`, replace:

```go
fl.stopped = true
```

with:

```go
fl.stopped.Store(true)
```

Run this audit and convert any remaining direct accesses:

```bash
rg -n '\.stopped|stopped:' internal/provider/firecracker
```

Every production read must use `Load`; every production write must use `Store`.

- [ ] **Step 4: Run focused and package tests**

Run:

```bash
go test -race -tags firecracker ./internal/provider/firecracker/ \
  -run 'TestLockFor_|TestPublishPort_AfterStopSentinel' -count=1
go test -tags firecracker ./internal/provider/firecracker/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/vminfo.go \
  internal/provider/firecracker/vminfo_lock.go \
  internal/provider/firecracker/vminfo_lock_test.go \
  internal/provider/firecracker/sandbox.go \
  internal/provider/firecracker/port_race_test.go
git commit -m "fix(firecracker): make VM stop sentinel atomic"
```

---

### Task 2: Make resize a disk-authoritative per-VM transaction

**Files:**
- Modify: `internal/provider/firecracker/sandbox_resize.go:29-181`
- Modify: `internal/provider/firecracker/sandbox_resize_test.go`
- Modify: `internal/provider/firecracker/port.go:21-118`
- Modify: `internal/provider/firecracker/network/port_allocator.go`
- Modify: `internal/provider/firecracker/network/port_allocator_test.go`

**Interfaces:**
- Consumes: atomic `lockFor` from Task 1, `ReadVMInfo`, `VMInfo.Write`, and existing DNAT test seams.
- Produces: `network.(*PortAllocator).InUse(port int) bool`; package variable `resizeWriteCPUMax`; port and limit cache synchronization after successful persistence.

- [ ] **Step 1: Add allocator-observation tests**

Append to `network/port_allocator_test.go`:

```go
func TestPortAllocator_InUse(t *testing.T) {
	a := NewPortAllocator()
	port, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if !a.InUse(port) {
		t.Fatalf("InUse(%d) = false after Allocate", port)
	}
	a.Release(port)
	if a.InUse(port) {
		t.Fatalf("InUse(%d) = true after Release", port)
	}
}
```

- [ ] **Step 2: Add a reusable disk-seeding helper and port-preservation regression**

In `sandbox_resize_test.go`, import `time` and
`github.com/navaris/navaris/internal/provider/firecracker/network`. Add:

```go
func seedResizeVM(t *testing.T, p *Provider, info *VMInfo) {
	t.Helper()
	if p.vms == nil {
		p.vms = make(map[string]*VMInfo)
	}
	if p.fileMu == nil {
		p.fileMu = make(map[string]*vmFileLock)
	}
	if err := os.MkdirAll(p.vmDir(info.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := info.Write(p.vmInfoPath(info.ID)); err != nil {
		t.Fatal(err)
	}
	p.vms[info.ID] = info
	p.fileMu[info.ID] = &vmFileLock{}
}
```

Update every existing `UpdateResources` test fixture so `Config.ChrootBase` is
a real `t.TempDir()` and call `seedResizeVM` for each VM expected to exist.
For `TestUpdateResources_FC_VMNotFound`, use empty initialized maps and no disk
file:

```go
p := &Provider{
	config: Config{ChrootBase: t.TempDir()},
	vms:    map[string]*VMInfo{},
	fileMu: map[string]*vmFileLock{},
}
```

Add this regression test:

```go
func TestUpdateResources_PreservesPublishedPortState(t *testing.T) {
	oldAdd, oldRemove := addDNATFn, removeDNATFn
	defer func() { addDNATFn, removeDNATFn = oldAdd, oldRemove }()

	var added, removed []int
	addDNATFn = func(port int, _ string, _ int) error {
		added = append(added, port)
		return nil
	}
	removeDNATFn = func(port int, _ string, _ int) {
		removed = append(removed, port)
	}

	tmp := t.TempDir()
	p := &Provider{
		config: Config{CgroupRoot: tmp, ChrootBase: tmp},
		cgroupVersion: "2", cgroupSkipFSCheck: true,
		subnets: network.NewAllocator(), portAlloc: network.NewPortAllocator(),
	}
	seed := &VMInfo{
		ID: "vm-port-resize", PID: os.Getpid(), TapDevice: "tap-test",
		LimitCPU: 1, CeilingCPU: 4, CgroupActive: true, Ports: map[int]int{},
	}
	seedResizeVM(t, p, seed)

	ep, err := p.PublishPort(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: seed.ID}, 8080,
		domain.PublishPortOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.portAlloc.InUse(ep.PublishedPort) {
		t.Fatalf("allocated port %d is not owned", ep.PublishedPort)
	}

	cpu := 2
	if err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
		domain.UpdateResourcesRequest{CPULimit: &cpu}); err != nil {
		t.Fatal(err)
	}

	disk, err := ReadVMInfo(p.vmInfoPath(seed.ID))
	if err != nil {
		t.Fatal(err)
	}
	if got := disk.Ports[ep.PublishedPort]; got != 8080 {
		t.Fatalf("disk port mapping = %d; want 8080", got)
	}
	if got := p.vms[seed.ID].Ports[ep.PublishedPort]; got != 8080 {
		t.Fatalf("cache port mapping = %d; want 8080", got)
	}

	if err := p.UnpublishPort(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
		ep.PublishedPort); err != nil {
		t.Fatal(err)
	}
	if p.portAlloc.InUse(ep.PublishedPort) {
		t.Fatalf("port %d still owned after UnpublishPort", ep.PublishedPort)
	}
	if len(added) != 1 || len(removed) != 1 || added[0] != removed[0] {
		t.Fatalf("DNAT add/remove mismatch: added=%v removed=%v", added, removed)
	}
}
```

- [ ] **Step 3: Add deterministic live-apply ordering tests**

Add a package-level CPU gate type and two tests in `sandbox_resize_test.go`:

```go
type cpuApplyGate struct {
	quota   int64
	release chan struct{}
}

func TestUpdateResources_WaitsForPortMutationBeforeLiveApply(t *testing.T) {
	oldAdd, oldRemove := addDNATFn, removeDNATFn
	defer func() { addDNATFn, removeDNATFn = oldAdd, oldRemove }()

	addStarted := make(chan struct{})
	releaseAdd := make(chan struct{})
	addDNATFn = func(int, string, int) error {
		close(addStarted)
		<-releaseAdd
		return nil
	}
	removeDNATFn = func(int, string, int) {}

	tmp := t.TempDir()
	p := &Provider{
		config: Config{CgroupRoot: tmp, ChrootBase: tmp},
		cgroupVersion: "2", cgroupSkipFSCheck: true,
		subnets: network.NewAllocator(), portAlloc: network.NewPortAllocator(),
	}
	seed := &VMInfo{ID: "vm-port-lock", PID: os.Getpid(), TapDevice: "tap-test",
		LimitCPU: 1, CeilingCPU: 4, CgroupActive: true, Ports: map[int]int{}}
	seedResizeVM(t, p, seed)

	publishDone := make(chan error, 1)
	go func() {
		_, err := p.PublishPort(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID}, 8080,
			domain.PublishPortOptions{})
		publishDone <- err
	}()
	<-addStarted // PublishPort holds fl.mu here.

	cpu := 2
	resizeDone := make(chan error, 1)
	go func() {
		resizeDone <- p.UpdateResources(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
			domain.UpdateResourcesRequest{CPULimit: &cpu})
	}()

	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(tmp, seed.ID, "cpu.max")); !os.IsNotExist(err) {
		t.Fatalf("resize applied before acquiring per-VM lock; cpu.max err=%v", err)
	}
	close(releaseAdd)
	if err := <-publishDone; err != nil {
		t.Fatal(err)
	}
	if err := <-resizeDone; err != nil {
		t.Fatal(err)
	}
}

func TestUpdateResources_SerializesLiveApplyAndCommit(t *testing.T) {
	oldWrite := resizeWriteCPUMax
	defer func() { resizeWriteCPUMax = oldWrite }()

	entered := make(chan cpuApplyGate, 2)
	resizeWriteCPUMax = func(_ *Provider, _ string, quota, _ int64) error {
		gate := cpuApplyGate{quota: quota, release: make(chan struct{})}
		entered <- gate
		<-gate.release
		return nil
	}

	tmp := t.TempDir()
	p := &Provider{config: Config{CgroupRoot: tmp, ChrootBase: tmp},
		cgroupVersion: "2", cgroupSkipFSCheck: true}
	seed := &VMInfo{ID: "vm-resize-order", PID: os.Getpid(), LimitCPU: 1,
		CeilingCPU: 4, CgroupActive: true}
	seedResizeVM(t, p, seed)

	cpuA, cpuB := 2, 3
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() {
		doneA <- p.UpdateResources(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
			domain.UpdateResourcesRequest{CPULimit: &cpuA})
	}()
	gateA := <-entered

	go func() {
		doneB <- p.UpdateResources(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
			domain.UpdateResourcesRequest{CPULimit: &cpuB})
	}()
	select {
	case gateB := <-entered:
		close(gateB.release)
		close(gateA.release)
		t.Fatal("second resize entered live apply before first committed")
	case <-time.After(50 * time.Millisecond):
	}

	close(gateA.release)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	gateB := <-entered
	close(gateB.release)
	if err := <-doneB; err != nil {
		t.Fatal(err)
	}

	disk, err := ReadVMInfo(p.vmInfoPath(seed.ID))
	if err != nil {
		t.Fatal(err)
	}
	if disk.LimitCPU != 3 || p.vms[seed.ID].LimitCPU != 3 {
		t.Fatalf("final limits disk=%d cache=%d; want 3",
			disk.LimitCPU, p.vms[seed.ID].LimitCPU)
	}
}
```

- [ ] **Step 4: Run the new tests and verify RED**

Run:

```bash
go test -race -tags firecracker ./internal/provider/firecracker/network/ \
  -run TestPortAllocator_InUse -count=1
go test -race -tags firecracker ./internal/provider/firecracker/ \
  -run 'TestUpdateResources_(PreservesPublishedPortState|WaitsForPortMutationBeforeLiveApply|SerializesLiveApplyAndCommit)' -count=1
```

Expected failures:

- allocator test does not compile because `InUse` is absent;
- provider test does not compile because `resizeWriteCPUMax` is absent;
- after adding only the seams, port preservation and ordering fail against the current stale-cache/late-lock implementation.

- [ ] **Step 5: Add the allocator and CPU-apply seams**

In `network/port_allocator.go` add:

```go
// InUse reports whether port is currently allocated or recovery-reserved.
func (a *PortAllocator) InUse(port int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used[port]
}
```

In `sandbox_resize.go`, add near `UpdateResources`:

```go
var resizeWriteCPUMax = func(p *Provider, dir string, quota, period int64) error {
	return p.writeCPUMax(dir, quota, period)
}
```

Replace all three `p.writeCPUMax(...)` calls inside `UpdateResources`—initial
apply and both compensation paths—with `resizeWriteCPUMax(p, ...)`. Do not
change boot-time calls in `sandbox.go` or `cgroup.go`.

- [ ] **Step 6: Move the per-VM lock to the start of the resize transaction**

After the nil-request fast return, acquire and defer the lock:

```go
fl, err := p.lockFor(ref.Ref)
if err != nil {
	return &domain.ProviderResizeError{
		Reason: domain.ResizeReasonVMStopped,
		Detail: err.Error(),
	}
}
defer fl.mu.Unlock()

p.vmMu.RLock()
_, registered := p.vms[ref.Ref]
p.vmMu.RUnlock()
if !registered {
	return fmt.Errorf("firecracker: vm %q not found: %w", ref.Ref, domain.ErrNotFound)
}

info, err := ReadVMInfo(p.vmInfoPath(ref.Ref))
if err != nil {
	return fmt.Errorf("firecracker: read vminfo for resize %q: %w", ref.Ref, err)
}
```

Use this fresh `info` for validation, prior-value calculation, live apply, and
persistence. Remove the old `p.vms` pointer lookup at the top and remove the old
commit-only `lockFor` block near the end.

After live apply succeeds, mutate the fresh object and write it. Use this
complete commit/compensation block:

```go
if req.CPULimit != nil {
	info.LimitCPU = int64(*req.CPULimit)
}
if req.MemoryLimitMB != nil {
	info.LimitMemMib = newMem
}
if err := info.Write(p.vmInfoPath(ref.Ref)); err != nil {
	// Disk and cache remain unchanged. Compensate the live changes.
	if cpuApplied {
		revertQuota := priorCPU * cpuPeriod
		if rerr := resizeWriteCPUMax(p, p.cgroupCPUDir(ref.Ref), revertQuota, cpuPeriod); rerr != nil {
			return fmt.Errorf("firecracker: persist vminfo after resize: %w; cgroup revert ALSO failed: %v", err, rerr)
		}
	}
	if req.MemoryLimitMB != nil {
		if rerr := p.patchBalloon(ctx, ref.Ref, priorBalloon); rerr != nil {
			return fmt.Errorf("firecracker: persist vminfo after resize: %w; balloon revert ALSO failed: %v", err, rerr)
		}
	}
	return fmt.Errorf("firecracker: persist vminfo after resize: %w", err)
}

p.vmMu.Lock()
if cached, ok := p.vms[ref.Ref]; ok {
	if req.CPULimit != nil {
		cached.LimitCPU = info.LimitCPU
	}
	if req.MemoryLimitMB != nil {
		cached.LimitMemMib = info.LimitMemMib
	}
}
p.vmMu.Unlock()
return nil
```

Delete the old pre-write cache mutation and cache rollback: cache changes now
occur only after successful persistence.

- [ ] **Step 7: Synchronize the port cache after successful persistence**

In `port.go`, add:

```go
func clonePorts(ports map[int]int) map[int]int {
	if ports == nil {
		return nil
	}
	clone := make(map[int]int, len(ports))
	for host, target := range ports {
		clone[host] = target
	}
	return clone
}

func (p *Provider) syncPortsCache(vmID string, ports map[int]int) {
	p.vmMu.Lock()
	if cached, ok := p.vms[vmID]; ok {
		cached.Ports = clonePorts(ports)
	}
	p.vmMu.Unlock()
}
```

Call `p.syncPortsCache(vmID, info.Ports)` immediately after each successful
`info.Write(infoPath)` in `PublishPort` and `UnpublishPort`, before returning or
releasing the allocator. Both callers still hold `fl.mu`, so the required
`fl.mu → vmMu` order is maintained.

- [ ] **Step 8: Run F1 tests and verify GREEN**

Run:

```bash
go test -race -tags firecracker ./internal/provider/firecracker/network/ -count=1
go test -race -tags firecracker ./internal/provider/firecracker/ \
  -run 'Test(UpdateResources|PublishPort|UnpublishPort|LockFor|StopSandbox)' -count=1
```

Expected: PASS.

Run the writer audit:

```bash
rg -n 'info\.Write\(' internal/provider/firecracker --glob '*.go'
```

For every result, verify the object came from a fresh `ReadVMInfo` under
`lockFor`, is an initial creation under `lockFor`, or is the documented
single-threaded recovery write. `sandbox_resize.go` must not write the pointer
obtained from `p.vms`.

- [ ] **Step 9: Commit**

```bash
git add internal/provider/firecracker/sandbox_resize.go \
  internal/provider/firecracker/sandbox_resize_test.go \
  internal/provider/firecracker/port.go \
  internal/provider/firecracker/network/port_allocator.go \
  internal/provider/firecracker/network/port_allocator_test.go
git commit -m "fix(firecracker): make resize a disk-authoritative transaction"
```

---

### Task 3: Add a ref-counted per-sandbox lock registry

**Files:**
- Modify: `internal/service/boost.go:12-48,162-174`
- Modify: `internal/service/boost_internal_test.go`

**Interfaces:**
- Consumes: `BoostService.mu` and the existing `sbxLocks` map.
- Produces: `sandboxLock{mu sync.Mutex, refs int}` and `acquireSandbox(sandboxID string) func()`.

- [ ] **Step 1: Replace the old helper test with failing lifetime tests**

Replace `boost_internal_test.go` with:

```go
package service

import (
	"testing"
	"time"
)

func TestAcquireSandbox_ReferenceLifetime(t *testing.T) {
	bs := &BoostService{timers: map[string]Timer{}, sbxLocks: map[string]*sandboxLock{}}
	release1 := bs.acquireSandbox("sbx-1")

	acquired2 := make(chan func(), 1)
	go func() { acquired2 <- bs.acquireSandbox("sbx-1") }()

	deadline := time.Now().Add(time.Second)
	for {
		bs.mu.Lock()
		entry := bs.sbxLocks["sbx-1"]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		bs.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiter was not reference-counted; refs=%d", refs)
		}
		time.Sleep(time.Millisecond)
	}

	release1()
	release2 := <-acquired2
	bs.mu.Lock()
	entry, ok := bs.sbxLocks["sbx-1"]
	if !ok || entry.refs != 1 {
		bs.mu.Unlock()
		t.Fatalf("entry after first release = %#v; want refs=1", entry)
	}
	bs.mu.Unlock()

	release2()
	bs.mu.Lock()
	_, ok = bs.sbxLocks["sbx-1"]
	bs.mu.Unlock()
	if ok {
		t.Fatal("idle sandbox lock entry was not reclaimed")
	}
}

func TestAcquireSandbox_DifferentSandboxesDoNotBlock(t *testing.T) {
	bs := &BoostService{timers: map[string]Timer{}, sbxLocks: map[string]*sandboxLock{}}
	release1 := bs.acquireSandbox("sbx-1")
	defer release1()

	done := make(chan func(), 1)
	go func() { done <- bs.acquireSandbox("sbx-2") }()
	select {
	case release2 := <-done:
		release2()
	case <-time.After(time.Second):
		t.Fatal("different sandbox blocked on sbx-1")
	}
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
go test -race ./internal/service/ -run 'TestAcquireSandbox_' -count=1
```

Expected: compile failure because `sandboxLock` and `acquireSandbox` do not
exist.

- [ ] **Step 3: Implement the registry**

In `boost.go`, replace the map type and initialize it in `NewBoostService`:

```go
type sandboxLock struct {
	mu   sync.Mutex
	refs int // guarded by BoostService.mu; holders and waiters both count
}

// In BoostService:
sbxLocks map[string]*sandboxLock

// In NewBoostService:
sbxLocks: make(map[string]*sandboxLock),
```

Replace `lockSandbox` with:

```go
// acquireSandbox returns with the sandbox's operation lock held. The returned
// release function must be called exactly once. Registration increments refs
// before waiting, so the map entry cannot disappear while a holder or waiter
// retains its pointer.
func (s *BoostService) acquireSandbox(sandboxID string) func() {
	s.mu.Lock()
	entry, ok := s.sbxLocks[sandboxID]
	if !ok {
		entry = &sandboxLock{}
		s.sbxLocks[sandboxID] = entry
	}
	entry.refs++
	s.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 && s.sbxLocks[sandboxID] == entry {
			delete(s.sbxLocks, sandboxID)
		}
		s.mu.Unlock()
	}
}
```

At this intermediate task, migrate the existing Start and expiry apply
boundaries without changing their ordering yet:

- In Start, finish the current bookkeeping, call `s.mu.Unlock()`, then call
  `releaseSandbox := s.acquireSandbox(sbx.SandboxID)` and defer it before the
  provider apply. Remove the old `lockSandbox` lookup and explicit mutex calls.
- In expiry, keep the current timer deletion and `GetByID` while `s.mu` is held,
  call `s.mu.Unlock()`, then call
  `releaseSandbox := s.acquireSandbox(boost.SandboxID)` and defer it before the
  sandbox lookup/provider apply.
- Never call `acquireSandbox` while `s.mu` is held: it registers its reference
  by taking `s.mu` internally.

Remove all manual `delete(s.sbxLocks, ...)` calls, including Cancel and
lifecycle cleanup. Those two paths do not acquire the operation lock until Task
5, but they must not invalidate an entry retained by Start or expiry.
Reference-counted release is the only permitted deletion. Remove the old
`lockSandbox` helper after both call sites compile against `acquireSandbox`.

- [ ] **Step 4: Run registry and existing boost tests**

Run:

```bash
go test -race ./internal/service/ -run 'Test(AcquireSandbox|Boost)' -count=1
```

Expected: PASS. The existing Start-vs-expire test must remain green during the
mechanical migration.

- [ ] **Step 5: Commit**

```bash
git add internal/service/boost.go internal/service/boost_internal_test.go
git commit -m "fix(service): add ref-counted sandbox operation locks"
```

---

### Task 4: Linearize Boost Start bookkeeping and live apply

**Files:**
- Modify: `internal/service/boost.go:63-160`
- Modify: `internal/service/boost_race_test.go`

**Interfaces:**
- Consumes: `acquireSandbox` from Task 3.
- Produces: Start holds one sandbox operation lock from revalidation through replacement, live apply, timer scheduling, and event publication.

- [ ] **Step 1: Add the failing Start-vs-Start test**

Append to `boost_race_test.go`:

```go
func TestBoostStartVsStart_BookkeepingAndApplyShareOrder(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-start-order", domain.SandboxRunning, "mock")

	type applyCall struct {
		cpu     int
		release chan struct{}
	}
	entered := make(chan applyCall, 2)
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef,
		req domain.UpdateResourcesRequest) error {
		call := applyCall{cpu: *req.CPULimit, release: make(chan struct{})}
		entered <- call
		<-call.release
		return nil
	}

	cpuA, cpuB := 4, 8
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
		})
		doneA <- err
	}()
	callA := <-entered
	rowA, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		doneB <- err
	}()

	time.Sleep(50 * time.Millisecond)
	whileA, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if whileA.BoostID != rowA.BoostID {
		close(callA.release)
		t.Fatalf("Start(B) replaced bookkeeping while Start(A) was applying: got %s want %s",
			whileA.BoostID, rowA.BoostID)
	}
	select {
	case callB := <-entered:
		close(callB.release)
		close(callA.release)
		t.Fatal("Start(B) applied while Start(A) held the sandbox operation")
	default:
	}

	close(callA.release)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	callB := <-entered
	if callB.cpu != cpuB {
		t.Fatalf("second apply cpu=%d; want %d", callB.cpu, cpuB)
	}
	close(callB.release)
	if err := <-doneB; err != nil {
		t.Fatal(err)
	}

	final, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if final.BoostedCPULimit == nil || *final.BoostedCPULimit != cpuB {
		t.Fatalf("final boost cpu=%v; want %d", final.BoostedCPULimit, cpuB)
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test -race ./internal/service/ -run TestBoostStartVsStart_BookkeepingAndApplyShareOrder -count=1
```

Expected: FAIL because Start(B) replaces the boost row while Start(A)'s live
apply is blocked.

- [ ] **Step 3: Move Start's sandbox lock before bookkeeping**

Retain cheap argument checks and the initial sandbox lookup for fast rejection.
Immediately after initial state/bounds validation, acquire the operation lock:

```go
releaseSandbox := s.acquireSandbox(opts.SandboxID)
defer releaseSandbox()

// Re-read under the per-sandbox serialization boundary.
sbx, err = s.sandboxes.Get(ctx, opts.SandboxID)
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
```

Replace the old Phase 1/2/3 block with the complete serialized body below.
Only timer-map access takes `s.mu`; boost-store, provider, and event calls do
not:

```go
if prior, getErr := s.boosts.Get(ctx, opts.SandboxID); getErr == nil {
	s.mu.Lock()
	if timer, ok := s.timers[prior.BoostID]; ok {
		timer.Stop()
		delete(s.timers, prior.BoostID)
	}
	s.mu.Unlock()
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
	Source:                source,
}
if err := s.boosts.Upsert(ctx, boost); err != nil {
	return nil, fmt.Errorf("persist boost: %w", err)
}

_, err = s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
	SandboxID:     sbx.SandboxID,
	CPULimit:      opts.CPULimit,
	MemoryLimitMB: opts.MemoryLimitMB,
	ApplyLiveOnly: true,
})
if err != nil {
	if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil {
		return nil, fmt.Errorf("apply boost failed: %v; rollback also failed: %w", err, delErr)
	}
	return nil, err
}

s.mu.Lock()
s.timers[boost.BoostID] = s.clock.AfterFunc(dur, func() {
	s.expire(context.Background(), boost.BoostID)
})
s.mu.Unlock()

_ = s.events.Publish(ctx, domain.Event{
	Type:      domain.EventBoostStarted,
	Timestamp: now,
	Data: map[string]any{
		"boost_id":                boost.BoostID,
		"sandbox_id":              boost.SandboxID,
		"boosted_cpu_limit":       boost.BoostedCPULimit,
		"boosted_memory_limit_mb": boost.BoostedMemoryLimitMB,
		"expires_at":              boost.ExpiresAt.Format(time.RFC3339Nano),
		"source":                  source,
	},
})
return boost, nil
```

- [ ] **Step 4: Add and run the different-sandbox concurrency test**

Append:

```go
func TestBoostStart_DifferentSandboxesApplyConcurrently(t *testing.T) {
	env := newBoostEnv(t)
	a := env.seedSandbox(t, "sbx-a", domain.SandboxRunning, "mock")
	b := env.seedSandbox(t, "sbx-b", domain.SandboxRunning, "mock")

	entered := make(chan chan struct{}, 2)
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef,
		domain.UpdateResourcesRequest) error {
		release := make(chan struct{})
		entered <- release
		<-release
		return nil
	}

	cpuA, cpuB := 4, 8
	done := make(chan error, 2)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: a.SandboxID, CPULimit: &cpuA, DurationSeconds: 60})
		done <- err
	}()
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: b.SandboxID, CPULimit: &cpuB, DurationSeconds: 60})
		done <- err
	}()

	var releases []chan struct{}
	for len(releases) < 2 {
		select {
		case release := <-entered:
			releases = append(releases, release)
		case <-time.After(time.Second):
			t.Fatal("different sandboxes did not enter apply concurrently")
		}
	}
	for _, release := range releases {
		close(release)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
```

Run:

```bash
go test -race ./internal/service/ \
  -run 'TestBoostStart(VsStart_BookkeepingAndApplyShareOrder|_DifferentSandboxesApplyConcurrently|_ReplacesExisting|_ProviderError_RollsBack)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/boost.go internal/service/boost_race_test.go
git commit -m "fix(service): linearize boost Start bookkeeping and apply"
```

---

### Task 5: Serialize expiry, Cancel, and lifecycle cleanup

**Files:**
- Modify: `internal/service/boost.go:190-420`
- Modify: `internal/service/boost_race_test.go`

**Interfaces:**
- Consumes: `acquireSandbox` from Task 3 and linearized Start from Task 4.
- Produces: expiry re-checks boost identity under the sandbox lock; Cancel and lifecycle cleanup share the same operation boundary.

- [ ] **Step 1: Add the failing Start-vs-Cancel test**

Append to `boost_race_test.go`:

```go
func TestBoostStartVsCancel_SerializeBookkeepingAndApply(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-cancel-order", domain.SandboxRunning, "mock")

	cpuA := 4
	boostA, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	type applyCall struct {
		cpu     int
		release chan struct{}
	}
	entered := make(chan applyCall, 2)
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef,
		req domain.UpdateResourcesRequest) error {
		call := applyCall{cpu: *req.CPULimit, release: make(chan struct{})}
		entered <- call
		<-call.release
		return nil
	}

	cancelDone := make(chan error, 1)
	go func() { cancelDone <- env.boost.Cancel(context.Background(), sbx.SandboxID) }()
	cancelCall := <-entered

	cpuB := 8
	startDone := make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		startDone <- err
	}()

	time.Sleep(50 * time.Millisecond)
	row, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if row.BoostID != boostA.BoostID {
		close(cancelCall.release)
		t.Fatalf("Start replaced boost while Cancel revert was applying: got %s want %s",
			row.BoostID, boostA.BoostID)
	}
	select {
	case startCall := <-entered:
		close(startCall.release)
		close(cancelCall.release)
		t.Fatal("Start applied concurrently with Cancel")
	default:
	}

	close(cancelCall.release)
	if err := <-cancelDone; err != nil {
		t.Fatal(err)
	}
	startCall := <-entered
	if startCall.cpu != cpuB {
		t.Fatalf("post-Cancel Start cpu=%d; want %d", startCall.cpu, cpuB)
	}
	close(startCall.release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}

	final, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if final.BoostedCPULimit == nil || *final.BoostedCPULimit != cpuB {
		t.Fatalf("final boost cpu=%v; want %d", final.BoostedCPULimit, cpuB)
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test -race ./internal/service/ -run TestBoostStartVsCancel_SerializeBookkeepingAndApply -count=1
```

Expected: FAIL because current Cancel removes the lock-map entry and performs
its provider revert without holding the same sandbox operation lock.

- [ ] **Step 3: Rewrite expiry around boost-ID revalidation**

Replace `expire` with this complete boost-ID-revalidating implementation:

```go
func (s *BoostService) expire(ctx context.Context, boostID string) {
	candidate, err := s.boosts.GetByID(ctx, boostID)
	if err != nil {
		s.mu.Lock()
		delete(s.timers, boostID)
		s.mu.Unlock()
		return
	}

	releaseSandbox := s.acquireSandbox(candidate.SandboxID)
	defer releaseSandbox()

	boost, err := s.boosts.GetByID(ctx, boostID)
	if err != nil {
		s.mu.Lock()
		delete(s.timers, boostID)
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	delete(s.timers, boostID)
	s.mu.Unlock()

	sbx, err := s.sandboxes.Get(ctx, boost.SandboxID)
	if err != nil {
		_ = s.boosts.Delete(ctx, boostID)
		return
	}
	if sbx.State != domain.SandboxRunning {
		_ = s.boosts.Delete(ctx, boostID)
		s.emitExpired(ctx, boost, "sandbox_not_running", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

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

	attempts := boost.RevertAttempts + 1
	if attempts > len(boostBackoff) {
		_ = s.boosts.UpdateState(ctx, boostID, domain.BoostRevertFailed, attempts, applyErr.Error())
		_ = s.events.Publish(ctx, domain.Event{
			Type:      domain.EventBoostRevertFailed,
			Timestamp: s.clock.Now().UTC(),
			Data: map[string]any{
				"boost_id":   boostID,
				"sandbox_id": boost.SandboxID,
				"attempts":   attempts,
				"last_error": applyErr.Error(),
				"source":     "external",
			},
		})
		return
	}

	_ = s.boosts.UpdateState(ctx, boostID, domain.BoostActive, attempts, applyErr.Error())
	s.mu.Lock()
	s.timers[boostID] = s.clock.AfterFunc(boostBackoff[attempts-1], func() {
		s.expire(context.Background(), boostID)
	})
	s.mu.Unlock()
}
```

- [ ] **Step 4: Rewrite Cancel and lifecycle cleanup around the same lock**

Replace `Cancel` and `cancelOnLifecycle` with the complete implementations
below:

```go
func (s *BoostService) Cancel(ctx context.Context, sandboxID string) error {
	releaseSandbox := s.acquireSandbox(sandboxID)
	defer releaseSandbox()

	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if timer, ok := s.timers[boost.BoostID]; ok {
		timer.Stop()
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
		_ = s.boosts.UpdateState(ctx, boost.BoostID, domain.BoostRevertFailed,
			boost.RevertAttempts+1, applyErr.Error())
		return applyErr
	}

	_ = s.boosts.Delete(ctx, boost.BoostID)
	s.emitExpired(ctx, boost, "cancelled", sbx.CPULimit, sbx.MemoryLimitMB)
	return nil
}

func (s *BoostService) cancelOnLifecycle(ctx context.Context, sandboxID string) {
	releaseSandbox := s.acquireSandbox(sandboxID)
	defer releaseSandbox()

	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		return
	}
	s.mu.Lock()
	if timer, ok := s.timers[boost.BoostID]; ok {
		timer.Stop()
		delete(s.timers, boost.BoostID)
	}
	s.mu.Unlock()
	_ = s.boosts.Delete(ctx, boost.BoostID)
}
```

Remove every manual `delete(s.sbxLocks, ...)` outside the reference-count-zero
branch in `acquireSandbox`.

- [ ] **Step 5: Run all boost ordering and behavior tests**

Run:

```bash
go test -race ./internal/service/ \
  -run 'Test(BoostStartVs|BoostStart_|BoostExpire_|BoostCancel_|SandboxStop_CancelsBoost|AcquireSandbox_)' \
  -count=1
```

Expected: PASS, including the existing Start-vs-expire test.

Audit deletion and slow-call locking:

```bash
rg -n 'delete\(s\.sbxLocks|UpdateResources\(|s\.mu\.(Lock|Unlock)' internal/service/boost.go
```

Expected:

- exactly one `delete(s.sbxLocks, ...)`, in the `refs == 0` release branch;
- every boost `UpdateResources` call occurs while the sandbox operation lock is
  held and while `s.mu` is not held.

- [ ] **Step 6: Commit**

```bash
git add internal/service/boost.go internal/service/boost_race_test.go
git commit -m "fix(service): serialize boost expiry and cancellation"
```

---

### Task 6: Remove the pre-existing boost-listener test race

**Files:**
- Modify: `internal/provider/firecracker/boost_listener_test.go:12-59`

**Interfaces:**
- Consumes: asynchronous `provider.BoostServer.Serve` callback.
- Produces: channel-synchronized fake server; no production-code changes.

- [ ] **Step 1: Confirm the existing test race before editing**

Run:

```bash
go test -race -tags firecracker ./internal/provider/firecracker/ \
  -run TestBoostListener_AcceptsAndDispatches -count=1
```

Expected: FAIL with a race between `fakeBoostServer.Serve` appending to
`calls` and the test reading `calls`.

- [ ] **Step 2: Replace shared slice polling with channel completion**

Change the fake and test setup to:

```go
type fakeBoostServer struct {
	calls chan string
}

func (f *fakeBoostServer) Serve(_ context.Context, conn net.Conn, sandboxID string) {
	defer conn.Close()
	f.calls <- sandboxID
}
```

Construct it with a buffer:

```go
server := &fakeBoostServer{calls: make(chan string, 1)}
```

Replace the deadline/polling and final slice assertion with:

```go
select {
case got := <-server.calls:
	if got != "sbx-1" {
		t.Fatalf("sandboxID = %q; want sbx-1", got)
	}
case <-time.After(time.Second):
	t.Fatal("boost listener did not dispatch connection")
}
```

Keep the existing `time` import for the timeout.

- [ ] **Step 3: Run focused and full Firecracker race tests**

Run:

```bash
go test -race -tags firecracker ./internal/provider/firecracker/ \
  -run TestBoostListener_AcceptsAndDispatches -count=1
go test -race -tags firecracker ./internal/provider/firecracker/... -count=1
```

Expected: PASS. The previously acknowledged race must no longer appear.

- [ ] **Step 4: Commit**

```bash
git add internal/provider/firecracker/boost_listener_test.go
git commit -m "test(firecracker): synchronize boost listener dispatch"
```

---

### Task 7: Controller verification and branch audit

**Files:**
- Verify only; modify documentation only if verification uncovers line-number or status drift.

**Interfaces:**
- Consumes: Tasks 1–6.
- Produces: fresh evidence that the corrected branch satisfies the revised spec and is ready for a new whole-branch review.

- [ ] **Step 1: Run formatting and static diff checks**

```bash
gofmt -w \
  internal/provider/firecracker/vminfo.go \
  internal/provider/firecracker/vminfo_lock.go \
  internal/provider/firecracker/vminfo_lock_test.go \
  internal/provider/firecracker/sandbox.go \
  internal/provider/firecracker/sandbox_resize.go \
  internal/provider/firecracker/sandbox_resize_test.go \
  internal/provider/firecracker/port.go \
  internal/provider/firecracker/port_race_test.go \
  internal/provider/firecracker/network/port_allocator.go \
  internal/provider/firecracker/network/port_allocator_test.go \
  internal/service/boost.go \
  internal/service/boost_internal_test.go \
  internal/service/boost_race_test.go \
  internal/provider/firecracker/boost_listener_test.go
git diff --check
```

Expected: no output from `git diff --check` and no uncommitted formatting-only changes. If `gofmt` changes a committed file, commit only those formatting changes before continuing.

- [ ] **Step 2: Run plain and Firecracker-tagged builds**

```bash
go build ./...
go build -tags firecracker ./...
```

Expected: both commands exit 0.

- [ ] **Step 3: Run the complete race gate**

```bash
go test -race -tags firecracker \
  ./internal/provider/firecracker/... \
  ./internal/service/... \
  ./internal/api/... \
  ./internal/domain/... \
  -count=1
```

Expected: all packages report `ok`; there is no boost-listener exception.

- [ ] **Step 4: Run invariant audits**

```bash
# F1: inspect every persistent writer.
rg -n 'info\.Write\(' internal/provider/firecracker --glob '*.go'

# Atomic stop sentinel: no plain reads/writes.
rg -n '\.stopped|stopped:' internal/provider/firecracker

# F12: only two long-lived launch machines.
rg -n 'fcsdk\.NewMachine' internal/provider/firecracker --glob '*.go'

# F7: exactly one deletion, in acquireSandbox release.
rg -n 'delete\(s\.sbxLocks' internal/service/boost.go

# F7: inspect every live boost apply against lock boundaries.
rg -n 'UpdateResources\(' internal/service/boost.go
```

Expected:

- every existing-file `info.Write` is based on a fresh disk read under
  `lockFor`; the only unlocked mutation write is the documented single-threaded
  recovery path;
- all sentinel accesses use `Load` or `Store`;
- exactly two `fcsdk.NewMachine` results, both long-lived launch paths;
- exactly one `sbxLocks` deletion, inside reference-count-zero release;
- Start, expire, and Cancel provider applies are each inside an acquired
  sandbox operation lock and outside `s.mu`.

- [ ] **Step 5: Record evidence and request final whole-branch review**

Append exact command results and the audited line list to:

```text
.superpowers/sdd/2026-08-04-fc-concurrency-batch1/progress.md
```

Generate a review package from base `d7ac3c5f1403f971cfd46eaff416347f89dc910a`
to the new HEAD and dispatch one fresh whole-branch reviewer. Explicitly ask it
to re-check the formerly load-bearing findings:

- stale port loss through resize;
- stop-sentinel data race;
- Start-vs-Start ordering;
- stale mutex identity;
- Cancel serialization.

Do not merge until the reviewer returns no Critical or Important findings and
the verification evidence remains current.
