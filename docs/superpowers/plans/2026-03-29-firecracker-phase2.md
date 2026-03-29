# Firecracker Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the 8 stubbed provider operations (snapshots, images, port forwarding) plus `CreateSandboxFromSnapshot`, giving the Firecracker backend full parity with Incus.

**Architecture:** Three subsystems — snapshots (file copy of rootfs + optional Firecracker memory snapshot), images (rootfs + JSON metadata flat files), port forwarding (iptables DNAT/FORWARD rules) — all using file-based storage. A new `PortAllocator` type manages host port allocation (40000-49999). The existing `VMInfo` struct gets `Ports` and `RestoreFromSnapshot` fields. `StartSandbox` gains a live-snapshot-restore path using the SDK's `WithSnapshot` opt.

**Tech Stack:** Go, firecracker-go-sdk, iptables, ext4 filesystem

**Spec:** `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/provider/firecracker/vminfo.go` | Modify | Add `Ports` and `RestoreFromSnapshot` fields, update `ClearRuntime` |
| `internal/provider/firecracker/vminfo_test.go` | Modify | Add tests for new fields |
| `internal/provider/firecracker/network/port_allocator.go` | Create | `PortAllocator` type with `Allocate()`/`Release()` |
| `internal/provider/firecracker/network/port_allocator_test.go` | Create | Unit tests for port allocator |
| `internal/provider/firecracker/network/dnat.go` | Create | `AddDNAT()`/`RemoveDNAT()` iptables helpers |
| `internal/provider/firecracker/snapshot.go` | Create | `CreateSnapshot`, `RestoreSnapshot`, `DeleteSnapshot`, `snapInfo` type |
| `internal/provider/firecracker/image.go` | Create | `PublishSnapshotAsImage`, `GetImageInfo`, `DeleteImage`, `imageInfo` type |
| `internal/provider/firecracker/port.go` | Create | `PublishPort`, `UnpublishPort` |
| `internal/provider/firecracker/sandbox.go` | Modify | Implement `CreateSandboxFromSnapshot`, add live snapshot restore to `StartSandbox`, add port cleanup to `StopSandbox` |
| `internal/provider/firecracker/firecracker.go` | Modify | Add `SnapshotDir` to `Config`, `portAlloc` to `Provider`, update recovery |
| `internal/provider/firecracker/stubs.go` | Delete | All methods move to their own files |
| `cmd/navarisd/provider_firecracker.go` | Modify | Pass `SnapshotDir` to config |
| `cmd/navarisd/main.go` | Modify | Add `--snapshot-dir` flag |
| `docker-compose.integration-firecracker.yml` | Modify | Add `--snapshot-dir`, remove skip env vars |
| `test/integration/e2e_test.go` | Modify | Remove snapshot skip guard |
| `test/integration/snapshot_test.go` | Modify | Remove snapshot skip guard |
| `test/integration/image_test.go` | Modify | Remove snapshot skip guard |
| `test/integration/port_test.go` | Modify | Remove port skip guard |

---

### Task 1: VMInfo Changes

**Files:**
- Modify: `internal/provider/firecracker/vminfo.go:10-19` (VMInfo struct)
- Modify: `internal/provider/firecracker/vminfo.go:61-66` (ClearRuntime)
- Modify: `internal/provider/firecracker/vminfo_test.go`

- [ ] **Step 1: Add fields to VMInfo struct**

In `internal/provider/firecracker/vminfo.go`, add two fields to the `VMInfo` struct:

```go
type VMInfo struct {
	ID                  string         `json:"id"`
	PID                 int            `json:"pid,omitempty"`
	CID                 uint32         `json:"cid"`
	TapDevice           string         `json:"tap_device,omitempty"`
	SubnetIdx           int            `json:"subnet_idx"`
	UID                 int            `json:"uid"`
	NetworkMode         string         `json:"network_mode,omitempty"`
	Stopping            bool           `json:"stopping,omitempty"`
	Ports               map[int]int    `json:"ports,omitempty"`
	RestoreFromSnapshot bool           `json:"restore_from_snapshot,omitempty"`
}
```

- [ ] **Step 2: Update ClearRuntime to clear Ports**

```go
func (v *VMInfo) ClearRuntime() {
	v.PID = 0
	v.TapDevice = ""
	v.SubnetIdx = 0
	v.Stopping = false
	v.Ports = nil
}
```

- [ ] **Step 3: Add unit tests for new fields**

Add to `internal/provider/firecracker/vminfo_test.go`:

```go
func TestVMInfoPortsPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	info := &VMInfo{
		ID:    "nvrs-fc-porttest",
		CID:   100,
		UID:   10000,
		Ports: map[int]int{40000: 8080, 40001: 3000},
	}
	if err := info.Write(path); err != nil {
		t.Fatal(err)
	}

	got, err := ReadVMInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ports) != 2 || got.Ports[40000] != 8080 || got.Ports[40001] != 3000 {
		t.Errorf("Ports mismatch: got %v", got.Ports)
	}
}

func TestVMInfoRestoreFromSnapshotPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	info := &VMInfo{
		ID:                  "nvrs-fc-snaprest",
		CID:                 100,
		UID:                 10000,
		RestoreFromSnapshot: true,
	}
	if err := info.Write(path); err != nil {
		t.Fatal(err)
	}

	got, err := ReadVMInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RestoreFromSnapshot {
		t.Error("RestoreFromSnapshot not persisted")
	}
}

func TestClearRuntimeClearsPorts(t *testing.T) {
	info := &VMInfo{
		ID:    "nvrs-fc-clear",
		CID:   100,
		UID:   10000,
		PID:   12345,
		Ports: map[int]int{40000: 8080},
	}
	info.ClearRuntime()
	if info.Ports != nil {
		t.Errorf("expected Ports cleared, got %v", info.Ports)
	}
	if info.PID != 0 {
		t.Error("expected PID cleared")
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/ -run TestVMInfo -v`
Expected: All tests pass including the new ones.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/vminfo.go internal/provider/firecracker/vminfo_test.go
git commit -m "feat(firecracker): add Ports and RestoreFromSnapshot fields to VMInfo"
```

---

### Task 2: Port Allocator

**Files:**
- Create: `internal/provider/firecracker/network/port_allocator.go`
- Create: `internal/provider/firecracker/network/port_allocator_test.go`

**Reference:** `internal/provider/firecracker/network/allocator.go` — existing Allocator pattern to follow. `internal/domain/errors.go` — `ErrCapacityExceeded`.

- [ ] **Step 1: Create port allocator**

Create `internal/provider/firecracker/network/port_allocator.go`:

```go
package network

import (
	"sync"

	"github.com/navaris/navaris/internal/domain"
)

const (
	portMin = 40000
	portMax = 49999
)

// PortAllocator manages host port allocation for port forwarding.
type PortAllocator struct {
	mu   sync.Mutex
	used map[int]bool
	next int
}

// NewPortAllocator creates a PortAllocator starting at portMin.
func NewPortAllocator() *PortAllocator {
	return &PortAllocator{
		used: make(map[int]bool),
		next: portMin,
	}
}

// Allocate returns the next available host port.
func (a *PortAllocator) Allocate() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Scan from next through the range, wrapping once.
	start := a.next
	for {
		if a.next > portMax {
			a.next = portMin
		}
		if !a.used[a.next] {
			port := a.next
			a.used[port] = true
			a.next++
			return port, nil
		}
		a.next++
		if a.next > portMax {
			a.next = portMin
		}
		if a.next == start {
			return 0, domain.ErrCapacityExceeded
		}
	}
}

// Release returns a port to the pool.
func (a *PortAllocator) Release(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, port)
}

// MarkUsed marks a port as in use (for recovery).
func (a *PortAllocator) MarkUsed(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.used[port] = true
}
```

- [ ] **Step 2: Create unit tests**

Create `internal/provider/firecracker/network/port_allocator_test.go`:

```go
package network

import (
	"errors"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestPortAllocatorFirstPort(t *testing.T) {
	a := NewPortAllocator()
	port, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if port != 40000 {
		t.Errorf("got %d, want 40000", port)
	}
}

func TestPortAllocatorSequential(t *testing.T) {
	a := NewPortAllocator()
	p1, _ := a.Allocate()
	p2, _ := a.Allocate()
	p3, _ := a.Allocate()

	if p1 != 40000 || p2 != 40001 || p3 != 40002 {
		t.Errorf("got %d %d %d, want 40000 40001 40002", p1, p2, p3)
	}
}

func TestPortAllocatorRelease(t *testing.T) {
	a := NewPortAllocator()
	port, _ := a.Allocate()
	a.Release(port)

	// Next allocation should still advance (not reuse immediately).
	p2, _ := a.Allocate()
	if p2 != 40001 {
		t.Errorf("got %d, want 40001", p2)
	}
}

func TestPortAllocatorMarkUsed(t *testing.T) {
	a := NewPortAllocator()
	a.MarkUsed(40000)
	port, _ := a.Allocate()
	if port != 40001 {
		t.Errorf("got %d, want 40001 (40000 marked used)", port)
	}
}

func TestPortAllocatorCapacityExceeded(t *testing.T) {
	a := NewPortAllocator()
	// Mark all ports as used.
	for p := portMin; p <= portMax; p++ {
		a.MarkUsed(p)
	}

	_, err := a.Allocate()
	if !errors.Is(err, domain.ErrCapacityExceeded) {
		t.Errorf("got %v, want ErrCapacityExceeded", err)
	}
}

func TestPortAllocatorWrapAround(t *testing.T) {
	a := NewPortAllocator()
	// Allocate up to the last port.
	for i := portMin; i < portMax; i++ {
		a.MarkUsed(i)
	}
	// next is at portMin, all used except portMax.
	port, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if port != portMax {
		t.Errorf("got %d, want %d", port, portMax)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/provider/firecracker/network/ -run TestPortAllocator -v`
Expected: All 6 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/provider/firecracker/network/port_allocator.go internal/provider/firecracker/network/port_allocator_test.go
git commit -m "feat(firecracker): add PortAllocator for host port management"
```

---

### Task 3: DNAT/FORWARD Iptables Helpers

**Files:**
- Create: `internal/provider/firecracker/network/dnat.go`

**Reference:** `internal/provider/firecracker/network/tap.go` — existing iptables helper pattern (direct `exec.Command` calls).

- [ ] **Step 1: Create DNAT helpers**

Create `internal/provider/firecracker/network/dnat.go`:

```go
package network

import (
	"fmt"
	"os/exec"
	"strconv"
)

// AddDNAT adds iptables rules to forward hostPort to guestIP:targetPort.
// Three rules: PREROUTING DNAT (external), OUTPUT DNAT (local), FORWARD ACCEPT.
func AddDNAT(hostPort int, guestIP string, targetPort int) error {
	dest := guestIP + ":" + strconv.Itoa(targetPort)
	hp := strconv.Itoa(hostPort)
	tp := strconv.Itoa(targetPort)

	rules := [][]string{
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", hp, "-j", "DNAT", "--to-destination", dest},
		{"iptables", "-t", "nat", "-A", "OUTPUT", "-p", "tcp", "--dport", hp, "-j", "DNAT", "--to-destination", dest},
		{"iptables", "-A", "FORWARD", "-p", "tcp", "-d", guestIP, "--dport", tp, "-j", "ACCEPT"},
	}

	for _, args := range rules {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			// Best-effort rollback: remove any rules we already added.
			RemoveDNAT(hostPort, guestIP, targetPort)
			return fmt.Errorf("add dnat %s→%s: %w: %s", hp, dest, err, out)
		}
	}
	return nil
}

// RemoveDNAT removes the three iptables rules added by AddDNAT.
// Errors are silently ignored (rules may not exist during cleanup).
func RemoveDNAT(hostPort int, guestIP string, targetPort int) {
	dest := guestIP + ":" + strconv.Itoa(targetPort)
	hp := strconv.Itoa(hostPort)
	tp := strconv.Itoa(targetPort)

	rules := [][]string{
		{"iptables", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "--dport", hp, "-j", "DNAT", "--to-destination", dest},
		{"iptables", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "--dport", hp, "-j", "DNAT", "--to-destination", dest},
		{"iptables", "-D", "FORWARD", "-p", "tcp", "-d", guestIP, "--dport", tp, "-j", "ACCEPT"},
	}

	for _, args := range rules {
		exec.Command(args[0], args[1:]...).CombinedOutput()
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `CGO_ENABLED=0 go build ./internal/provider/firecracker/network/`
Expected: Compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/firecracker/network/dnat.go
git commit -m "feat(firecracker): add iptables DNAT/FORWARD rule helpers"
```

---

### Task 4: Snapshot Operations

**Files:**
- Create: `internal/provider/firecracker/snapshot.go`

**Reference files:**
- `internal/provider/firecracker/sandbox.go` — `copyFile` helper, `processAlive`, jailer path helpers
- `internal/provider/firecracker/vminfo.go` — `ReadVMInfo`, `VMInfo.Write`
- `internal/provider/incus/snapshot.go` — Incus reference implementation
- `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md` — Sections 1 and 4

**Important SDK notes:**
- Connect to running VM: `fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})` — only `SocketPath`, no other fields.
- `machine.PauseVM(ctx)` / `machine.ResumeVM(ctx)` — pause/resume for live snapshots.
- `machine.CreateSnapshot(ctx, memFilePath, snapshotPath)` — paths are relative to jailer chroot root.
- For live snapshot restore: `fcsdk.WithSnapshot(memPath, snapPath)` opt on `NewMachine`. The `Config` should omit `KernelImagePath`, `KernelArgs`, `MachineCfg`.

- [ ] **Step 1: Add SnapshotDir to Config (prerequisite for snapshot paths)**

In `internal/provider/firecracker/firecracker.go`, add `SnapshotDir` to the `Config` struct (after `HostInterface`):

```go
type Config struct {
	FirecrackerBin string
	JailerBin      string
	KernelPath     string
	ImageDir       string
	ChrootBase     string
	VsockCIDBase   uint32
	HostInterface  string
	SnapshotDir    string
}
```

Update `defaults()` to add:

```go
if c.SnapshotDir == "" {
	c.SnapshotDir = "/srv/firecracker/snapshots"
}
```

In `New()`, after `cfg.defaults()`, add:

```go
if err := os.MkdirAll(cfg.SnapshotDir, 0o755); err != nil {
	return nil, fmt.Errorf("firecracker: create snapshot dir: %w", err)
}
```

Add `"os"` to the imports if not already present.

- [ ] **Step 2: Create snapshot.go**

Create `internal/provider/firecracker/snapshot.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
)

type snapInfo struct {
	ID        string               `json:"id"`
	SourceVM  string               `json:"source_vm"`
	Label     string               `json:"label"`
	Mode      domain.ConsistencyMode `json:"mode"`
	CreatedAt time.Time            `json:"created_at"`
}

func (p *Provider) snapshotDir(snapID string) string {
	return filepath.Join(p.config.SnapshotDir, snapID)
}

func (p *Provider) snapInfoPath(snapID string) string {
	return filepath.Join(p.snapshotDir(snapID), "snapinfo.json")
}

func readSnapInfo(path string) (*snapInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var si snapInfo
	if err := json.Unmarshal(data, &si); err != nil {
		return nil, err
	}
	return &si, nil
}

func writeSnapInfo(path string, si *snapInfo) error {
	data, err := json.MarshalIndent(si, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (p *Provider) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	vmID := ref.Ref
	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)

	snapID := "snap-" + uuid.NewString()[:8]
	snapDir := p.snapshotDir(snapID)

	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create snapshot dir: %w", err)
	}

	switch mode {
	case domain.ConsistencyStopped:
		if err := p.createStoppedSnapshot(vmDir, snapDir); err != nil {
			os.RemoveAll(snapDir)
			return domain.BackendRef{}, err
		}

	case domain.ConsistencyLive:
		if err := p.createLiveSnapshot(ctx, vmID, vmDir, snapDir); err != nil {
			os.RemoveAll(snapDir)
			return domain.BackendRef{}, err
		}

	default:
		os.RemoveAll(snapDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker: unsupported consistency mode %q", mode)
	}

	si := &snapInfo{
		ID:        snapID,
		SourceVM:  vmID,
		Label:     label,
		Mode:      mode,
		CreatedAt: time.Now().UTC(),
	}
	if err := writeSnapInfo(p.snapInfoPath(snapID), si); err != nil {
		os.RemoveAll(snapDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker write snapinfo: %w", err)
	}

	return domain.BackendRef{Backend: backendName, Ref: snapID}, nil
}

func (p *Provider) createStoppedSnapshot(vmDir, snapDir string) error {
	src := filepath.Join(vmDir, "rootfs.ext4")
	dst := filepath.Join(snapDir, "rootfs.ext4")
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("firecracker snapshot copy rootfs: %w", err)
	}
	return nil
}

func (p *Provider) createLiveSnapshot(ctx context.Context, vmID, vmDir, snapDir string) error {
	// Connect to running VM via the post-jailer socket path.
	sockPath := filepath.Join(vmDir, "root", "run", "firecracker.socket")
	machine, err := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
	if err != nil {
		return fmt.Errorf("firecracker live snapshot connect %s: %w", vmID, err)
	}

	// Pause → snapshot → copy → resume.
	if err := machine.PauseVM(ctx); err != nil {
		return fmt.Errorf("firecracker pause %s: %w", vmID, err)
	}

	// Ensure we resume even if something fails.
	var snapErr error
	defer func() {
		if snapErr != nil {
			if rerr := machine.ResumeVM(ctx); rerr != nil {
				slog.Error("firecracker: failed to resume after snapshot error", "vm", vmID, "error", rerr)
			}
		}
	}()

	// Create Firecracker memory snapshot. Paths are relative to the jailer chroot root.
	memFile := "/vmstate.bin"
	snapMeta := "/snapshot.meta"
	if snapErr = machine.CreateSnapshot(ctx, memFile, snapMeta); snapErr != nil {
		return fmt.Errorf("firecracker create snapshot %s: %w", vmID, snapErr)
	}

	// Copy rootfs while VM is paused (disk consistent).
	rootfsSrc := filepath.Join(vmDir, "rootfs.ext4")
	rootfsDst := filepath.Join(snapDir, "rootfs.ext4")
	if snapErr = copyFile(rootfsSrc, rootfsDst); snapErr != nil {
		return fmt.Errorf("firecracker snapshot copy rootfs: %w", snapErr)
	}

	// Copy snapshot files from chroot to snapshot dir.
	chrootRoot := filepath.Join(vmDir, "root")
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		src := filepath.Join(chrootRoot, name)
		dst := filepath.Join(snapDir, name)
		if snapErr = copyFile(src, dst); snapErr != nil {
			return fmt.Errorf("firecracker snapshot copy %s: %w", name, snapErr)
		}
	}

	// Resume the VM.
	if err := machine.ResumeVM(ctx); err != nil {
		return fmt.Errorf("firecracker resume %s: %w", vmID, err)
	}
	snapErr = nil // Clear so defer doesn't try to resume again.

	// Clean up snapshot files from chroot dir.
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		os.Remove(filepath.Join(chrootRoot, name))
	}

	return nil
}

func (p *Provider) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	vmID := sandboxRef.Ref
	snapID := snapshotRef.Ref

	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)
	snapDir := p.snapshotDir(snapID)

	si, err := readSnapInfo(p.snapInfoPath(snapID))
	if err != nil {
		return fmt.Errorf("firecracker read snapinfo %s: %w", snapID, err)
	}

	// Copy rootfs from snapshot to VM.
	if err := copyFile(filepath.Join(snapDir, "rootfs.ext4"), filepath.Join(vmDir, "rootfs.ext4")); err != nil {
		return fmt.Errorf("firecracker restore copy rootfs: %w", err)
	}

	if si.Mode == domain.ConsistencyLive {
		// Copy snapshot files to VM's chroot root for live restore.
		chrootRoot := filepath.Join(vmDir, "root")
		os.MkdirAll(chrootRoot, 0o755)
		for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
			if err := copyFile(filepath.Join(snapDir, name), filepath.Join(chrootRoot, name)); err != nil {
				return fmt.Errorf("firecracker restore copy %s: %w", name, err)
			}
		}

		// Set restore flag in vminfo.
		infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
		info, err := ReadVMInfo(infoPath)
		if err != nil {
			return fmt.Errorf("firecracker restore read vminfo: %w", err)
		}
		info.RestoreFromSnapshot = true
		if err := info.Write(infoPath); err != nil {
			return fmt.Errorf("firecracker restore write vminfo: %w", err)
		}
	}

	return nil
}

func (p *Provider) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	snapDir := p.snapshotDir(snapshotRef.Ref)
	if err := os.RemoveAll(snapDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("firecracker delete snapshot %s: %w", snapshotRef.Ref, err)
	}
	return nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `CGO_ENABLED=0 go build -tags firecracker ./internal/provider/firecracker/`
Expected: Compiles successfully (the stubs.go file still exists but defines the same methods — this will fail). Before compiling, check if the stub methods conflict. If so, delete the corresponding stubs from `stubs.go` first:

Delete `CreateSnapshot`, `RestoreSnapshot`, and `DeleteSnapshot` method stubs from `internal/provider/firecracker/stubs.go`. Keep the remaining 5 stubs.

Run: `CGO_ENABLED=0 go build -tags firecracker ./internal/provider/firecracker/`
Expected: Compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/firecracker/snapshot.go internal/provider/firecracker/stubs.go internal/provider/firecracker/firecracker.go
git commit -m "feat(firecracker): implement CreateSnapshot, RestoreSnapshot, DeleteSnapshot"
```

---

### Task 5: Image Operations

**Files:**
- Create: `internal/provider/firecracker/image.go`

**Reference files:**
- `internal/provider/firecracker/snapshot.go` — snapshot directory and `readSnapInfo` helper
- `internal/provider/incus/image.go` — Incus reference implementation
- `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md` — Section 2

- [ ] **Step 1: Create image.go**

Create `internal/provider/firecracker/image.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

type imageInfo struct {
	Ref            string `json:"ref"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Architecture   string `json:"architecture"`
	Size           int64  `json:"size"`
	SourceSnapshot string `json:"source_snapshot"`
}

func (p *Provider) imageExtPath(ref string) string {
	return filepath.Join(p.config.ImageDir, ref+".ext4")
}

func (p *Provider) imageMetaPath(ref string) string {
	return filepath.Join(p.config.ImageDir, ref+".json")
}

func (p *Provider) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	snapID := snapshotRef.Ref
	snapDir := p.snapshotDir(snapID)

	imgRef := "img-" + uuid.NewString()[:8]

	// Copy rootfs from snapshot to image directory.
	src := filepath.Join(snapDir, "rootfs.ext4")
	dst := p.imageExtPath(imgRef)
	if err := copyFile(src, dst); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image copy: %w", err)
	}

	// Get file size.
	fi, err := os.Stat(dst)
	if err != nil {
		os.Remove(dst)
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image stat: %w", err)
	}

	// Write metadata.
	meta := &imageInfo{
		Ref:            imgRef,
		Name:           req.Name,
		Version:        req.Version,
		Architecture:   runtime.GOARCH,
		Size:           fi.Size(),
		SourceSnapshot: snapID,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.Remove(dst)
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image marshal: %w", err)
	}
	if err := os.WriteFile(p.imageMetaPath(imgRef), data, 0o644); err != nil {
		os.Remove(dst)
		return domain.BackendRef{}, fmt.Errorf("firecracker publish image write meta: %w", err)
	}

	return domain.BackendRef{Backend: backendName, Ref: imgRef}, nil
}

func (p *Provider) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
	data, err := os.ReadFile(p.imageMetaPath(imageRef.Ref))
	if err != nil {
		return domain.ImageInfo{}, fmt.Errorf("firecracker get image info %s: %w", imageRef.Ref, err)
	}
	var meta imageInfo
	if err := json.Unmarshal(data, &meta); err != nil {
		return domain.ImageInfo{}, fmt.Errorf("firecracker parse image info %s: %w", imageRef.Ref, err)
	}
	return domain.ImageInfo{
		Architecture: meta.Architecture,
		Size:         meta.Size,
	}, nil
}

func (p *Provider) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	ref := imageRef.Ref
	os.Remove(p.imageExtPath(ref))
	os.Remove(p.imageMetaPath(ref))
	return nil
}
```

- [ ] **Step 2: Remove image stubs from stubs.go**

Delete `PublishSnapshotAsImage`, `DeleteImage`, and `GetImageInfo` method stubs from `internal/provider/firecracker/stubs.go`.

- [ ] **Step 3: Verify compilation**

Run: `CGO_ENABLED=0 go build -tags firecracker ./internal/provider/firecracker/`
Expected: Compiles successfully.

- [ ] **Step 4: Commit**

```bash
git add internal/provider/firecracker/image.go internal/provider/firecracker/stubs.go
git commit -m "feat(firecracker): implement PublishSnapshotAsImage, GetImageInfo, DeleteImage"
```

---

### Task 6: Port Forwarding

**Files:**
- Create: `internal/provider/firecracker/port.go`

**Reference files:**
- `internal/provider/firecracker/network/dnat.go` — `AddDNAT`/`RemoveDNAT` helpers
- `internal/provider/firecracker/network/port_allocator.go` — `PortAllocator`
- `internal/provider/firecracker/vminfo.go` — `VMInfo.Ports` field
- `internal/provider/incus/network.go` — Incus reference implementation
- `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md` — Section 3

- [ ] **Step 1: Create port.go**

Create `internal/provider/firecracker/port.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"fmt"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
)

func (p *Provider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	vmID := ref.Ref

	// Allocate host port.
	hostPort, err := p.portAlloc.Allocate()
	if err != nil {
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port %s: %w", vmID, err)
	}

	// Read vminfo to get guest IP.
	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port read vminfo %s: %w", vmID, err)
	}
	guestIP := p.subnets.GuestIP(info.SubnetIdx).String()

	// Add iptables rules.
	if err := network.AddDNAT(hostPort, guestIP, targetPort); err != nil {
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port dnat %s: %w", vmID, err)
	}

	// Update vminfo with port mapping.
	if info.Ports == nil {
		info.Ports = make(map[int]int)
	}
	info.Ports[hostPort] = targetPort
	if err := info.Write(infoPath); err != nil {
		network.RemoveDNAT(hostPort, guestIP, targetPort)
		p.portAlloc.Release(hostPort)
		return domain.PublishedEndpoint{}, fmt.Errorf("firecracker publish port write vminfo %s: %w", vmID, err)
	}

	return domain.PublishedEndpoint{
		HostAddress:   "0.0.0.0",
		PublishedPort: hostPort,
	}, nil
}

func (p *Provider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	vmID := ref.Ref

	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		return fmt.Errorf("firecracker unpublish port read vminfo %s: %w", vmID, err)
	}

	targetPort, ok := info.Ports[publishedPort]
	if !ok {
		return nil // Port not found, nothing to do.
	}

	guestIP := p.subnets.GuestIP(info.SubnetIdx).String()

	// Remove iptables rules.
	network.RemoveDNAT(publishedPort, guestIP, targetPort)

	// Update vminfo.
	delete(info.Ports, publishedPort)
	if err := info.Write(infoPath); err != nil {
		return fmt.Errorf("firecracker unpublish port write vminfo %s: %w", vmID, err)
	}

	p.portAlloc.Release(publishedPort)
	return nil
}
```

- [ ] **Step 2: Remove port stubs from stubs.go**

Delete `PublishPort` and `UnpublishPort` method stubs from `internal/provider/firecracker/stubs.go`. At this point `stubs.go` should be empty (no methods left). Delete the file entirely.

- [ ] **Step 3: Verify compilation**

Note: This will fail because `p.portAlloc` doesn't exist yet on the `Provider` struct. That's added in Task 8. For now, add a temporary field to verify the port.go code compiles.

Actually, skip this step — `portAlloc` is added in Task 8. The code is correct; it just depends on Task 8 for the Provider field. The implementer should note this dependency and may need to temporarily stub the field or combine with Task 8.

Alternative: Add `portAlloc *network.PortAllocator` to the `Provider` struct now (in `firecracker.go` line 49) so the port code compiles. This is a small change that makes Task 6 self-contained.

In `internal/provider/firecracker/firecracker.go`, add to the Provider struct (after line 49):

```go
type Provider struct {
	config    Config
	subnets   *network.Allocator
	uids      *jailer.UIDAllocator
	portAlloc *network.PortAllocator
	cidNext   uint32
	cidMu     sync.Mutex
	vms       map[string]*VMInfo
	vmMu      sync.RWMutex
	hostIface string
}
```

And in `New()` (after line 86), add initialization:

```go
portAlloc: network.NewPortAllocator(),
```

Run: `CGO_ENABLED=0 go build -tags firecracker ./internal/provider/firecracker/`
Expected: Compiles successfully.

- [ ] **Step 4: Commit**

```bash
git add internal/provider/firecracker/port.go internal/provider/firecracker/firecracker.go
git rm internal/provider/firecracker/stubs.go
git commit -m "feat(firecracker): implement PublishPort, UnpublishPort; delete stubs"
```

---

### Task 7: CreateSandboxFromSnapshot and StartSandbox Live Restore

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go:299-301` (CreateSandboxFromSnapshot stub)
- Modify: `internal/provider/firecracker/sandbox.go:68-178` (StartSandbox — add live restore path)

**Reference files:**
- `internal/provider/firecracker/snapshot.go` — `snapshotDir`, `readSnapInfo`
- `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md` — Sections 2 (CreateSandboxFromSnapshot) and 4 (StartSandbox changes)

**Important:** `CreateSandboxFromSnapshot` always boots fresh (copies rootfs, allocates new resources). Live restore is only for `RestoreSnapshot` on the same VM, handled by the `RestoreFromSnapshot` flag in `StartSandbox`.

- [ ] **Step 1: Implement CreateSandboxFromSnapshot**

Replace the stub at `internal/provider/firecracker/sandbox.go:299-301` with:

```go
func (p *Provider) CreateSandboxFromSnapshot(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	snapID := snapshotRef.Ref
	snapDir := p.snapshotDir(snapID)

	vmID := vmName()
	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)

	// Create VM directory.
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create dir %s: %w", vmID, err)
	}

	// Copy rootfs from snapshot.
	src := filepath.Join(snapDir, "rootfs.ext4")
	dst := filepath.Join(vmDir, "rootfs.ext4")
	if err := copyFile(src, dst); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker copy snapshot rootfs %s: %w", vmID, err)
	}

	// Allocate resources.
	cid := p.allocateCID()
	uid := p.uids.Allocate()

	// Write vminfo.json.
	info := &VMInfo{ID: vmID, CID: cid, UID: uid, NetworkMode: string(req.NetworkMode)}
	if err := info.Write(jailer.VMInfoPath(p.config.ChrootBase, vmID)); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker write vminfo %s: %w", vmID, err)
	}

	return domain.BackendRef{Backend: backendName, Ref: vmID}, nil
}
```

Note: This also requires `snapshot.go`'s `snapshotDir` method. Since `CreateSandboxFromSnapshot` is in `sandbox.go` and `snapshotDir` is defined on `*Provider` in `snapshot.go`, it's accessible.

- [ ] **Step 2: Add live snapshot restore path to StartSandbox**

In `internal/provider/firecracker/sandbox.go`, after reading vminfo and checking "already running" (after line 81), add the live snapshot restore check:

```go
// Check for live snapshot restore.
if info.RestoreFromSnapshot {
	return p.startFromSnapshot(ctx, vmID, vmDir, info, infoPath)
}
```

Then add the `startFromSnapshot` method anywhere in `sandbox.go`:

```go
func (p *Provider) startFromSnapshot(ctx context.Context, vmID, vmDir string, info *VMInfo, infoPath string) error {
	// Allocate networking.
	subnetIdx := p.subnets.Allocate()
	tapName := network.TapName(vmID)
	hostIP := p.subnets.HostIP(subnetIdx).String()

	if err := network.CreateTap(tapName, hostIP); err != nil {
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker snapshot restore create tap %s: %w", vmID, err)
	}

	rootfsPath := filepath.Join(vmDir, "rootfs.ext4")
	chrootRoot := filepath.Join(vmDir, "root")
	memPath := filepath.Join(chrootRoot, "vmstate.bin")
	snapPath := filepath.Join(chrootRoot, "snapshot.meta")

	// Build config for snapshot restore — omit KernelImagePath, KernelArgs, MachineCfg.
	fcCfg := fcsdk.Config{
		SocketPath: filepath.Join(vmDir, "firecracker.sock"),
		Drives: []models.Drive{
			{
				DriveID:      fcsdk.String("rootfs"),
				PathOnHost:   fcsdk.String(rootfsPath),
				IsRootDevice: fcsdk.Bool(true),
				IsReadOnly:   fcsdk.Bool(false),
			},
		},
		NetworkInterfaces: fcsdk.NetworkInterfaces{
			{
				StaticConfiguration: &fcsdk.StaticNetworkConfiguration{
					MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", subnetIdx>>8, subnetIdx&0xFF),
					HostDevName: tapName,
				},
			},
		},
		VsockDevices: []fcsdk.VsockDevice{
			{Path: "vsock", CID: uint32(info.CID)},
		},
		JailerCfg: &fcsdk.JailerConfig{
			GID:            fcsdk.Int(info.UID),
			UID:            fcsdk.Int(info.UID),
			ID:             vmID,
			NumaNode:       fcsdk.Int(0),
			ExecFile:       p.config.FirecrackerBin,
			JailerBinary:   p.config.JailerBin,
			ChrootBaseDir:  p.config.ChrootBase,
			ChrootStrategy: fcsdk.NewNaiveChrootStrategy(p.config.KernelPath),
		},
	}

	machine, err := fcsdk.NewMachine(ctx, fcCfg, fcsdk.WithSnapshot(memPath, snapPath, func(cfg *fcsdk.SnapshotConfig) {
		cfg.ResumeVM = true
	}))
	if err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker snapshot restore new machine %s: %w", vmID, err)
	}

	if err := machine.Start(ctx); err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker snapshot restore start %s: %w", vmID, err)
	}

	// Update vminfo with runtime state.
	pid, pidErr := machine.PID()
	if pidErr != nil {
		slog.Warn("firecracker: could not get PID", "vm", vmID, "error", pidErr)
	}
	info.PID = pid
	info.TapDevice = tapName
	info.SubnetIdx = subnetIdx
	info.RestoreFromSnapshot = false // Clear the flag.
	info.Write(infoPath)

	// Register in memory.
	p.vmMu.Lock()
	p.vms[vmID] = info
	p.vmMu.Unlock()

	// Add masquerade for published mode.
	if info.NetworkMode == string(domain.NetworkPublished) {
		guestIP := p.subnets.GuestIP(subnetIdx).String()
		if err := network.AddMasquerade(guestIP, p.hostIface); err != nil {
			slog.Warn("firecracker: masquerade failed", "vm", vmID, "error", err)
		}
	}

	// Clean up snapshot files from chroot.
	os.Remove(memPath)
	os.Remove(snapPath)

	// Wait for agent health check.
	if err := p.waitForAgent(ctx, info.CID, 30*time.Second); err != nil {
		return fmt.Errorf("firecracker agent timeout %s: %w", vmID, err)
	}

	return nil
}
```

- [ ] **Step 3: Remove ErrNotImplemented reference**

The `ErrNotImplemented` variable was defined in `stubs.go` (now deleted). If any code still references it, remove the reference. The `CreateSandboxFromSnapshot` stub was the last user. Verify no remaining references:

Run: `grep -rn ErrNotImplemented internal/provider/firecracker/`
Expected: No output (all references removed).

- [ ] **Step 4: Verify compilation**

Run: `CGO_ENABLED=0 go build -tags firecracker ./internal/provider/firecracker/`
Expected: Compiles successfully.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/sandbox.go
git commit -m "feat(firecracker): implement CreateSandboxFromSnapshot and live snapshot restore"
```

---

### Task 8: Provider Recovery and CLI Flag

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go` — update `recover()` for port recovery
- Modify: `cmd/navarisd/provider_firecracker.go` — pass `SnapshotDir`
- Modify: `cmd/navarisd/main.go` — add `--snapshot-dir` flag

**Reference:** `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md` — Sections 1 (Configuration), 3 (Recovery)

**Note:** `SnapshotDir` was added to `Config` in Task 4. `portAlloc` was added to `Provider` in Task 6.

- [ ] **Step 1: Update recovery for port cleanup**

In `recover()`, after the existing `slog.Info` line (line 120), add port recovery logic:

```go
// Port recovery: re-establish or clean up port rules.
if len(info.Ports) > 0 {
	alive := info.PID > 0 && processAlive(info.PID)
	if alive && info.SubnetIdx > 0 {
		// Running VM — re-establish iptables rules.
		guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
		for hp, tp := range info.Ports {
			p.portAlloc.MarkUsed(hp)
			if err := network.AddDNAT(hp, guestIP, tp); err != nil {
				slog.Warn("firecracker: recovery re-add dnat", "vm", info.ID, "port", hp, "error", err)
			}
		}
	} else {
		// Dead VM — best-effort remove stale rules, clear ports.
		if info.SubnetIdx > 0 {
			guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
			for hp, tp := range info.Ports {
				network.RemoveDNAT(hp, guestIP, tp)
			}
		}
		info.Ports = nil
		infoPath := jailer.VMInfoPath(p.config.ChrootBase, info.ID)
		info.Write(infoPath)
	}
}
```

- [ ] **Step 2: Add --snapshot-dir CLI flag**

In `cmd/navarisd/main.go`, add to the config struct (after `hostInterface`):

```go
snapshotDir string
```

Add the flag registration (after the `hostInterface` flag):

```go
flag.StringVar(&cfg.snapshotDir, "snapshot-dir", "/srv/firecracker/snapshots", "directory for Firecracker snapshots")
```

- [ ] **Step 3: Pass SnapshotDir in provider_firecracker.go**

In `cmd/navarisd/provider_firecracker.go`, add `SnapshotDir` to the config:

```go
func newFirecrackerProvider(cfg config) (domain.Provider, error) {
	return firecracker.New(firecracker.Config{
		FirecrackerBin: cfg.firecrackerBin,
		JailerBin:      cfg.jailerBin,
		KernelPath:     cfg.kernelPath,
		ImageDir:       cfg.imageDir,
		ChrootBase:     cfg.chrootBase,
		HostInterface:  cfg.hostInterface,
		SnapshotDir:    cfg.snapshotDir,
	})
}
```

- [ ] **Step 4: Verify compilation**

Run: `CGO_ENABLED=0 go build -tags firecracker ./cmd/navarisd/`
Expected: Compiles successfully.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/firecracker.go cmd/navarisd/provider_firecracker.go cmd/navarisd/main.go
git commit -m "feat(firecracker): add port recovery and --snapshot-dir CLI flag"
```

---

### Task 9: StopSandbox Port Cleanup

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go:216-226` (StopSandbox cleanup section)

**Reference:** `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md` — Section 3 (Cleanup)

StopSandbox must remove iptables DNAT rules for published ports before clearing runtime state. Without this, restarting a VM would leave stale DNAT rules pointing to old guest IPs.

- [ ] **Step 1: Add port cleanup to StopSandbox**

In `internal/provider/firecracker/sandbox.go`, after the `stopped:` label (line 216) and before the "Clean up networking" comment (line 218), add port cleanup:

```go
stopped:

	// Clean up port forwarding rules.
	if len(info.Ports) > 0 && info.SubnetIdx > 0 {
		guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
		for hp, tp := range info.Ports {
			network.RemoveDNAT(hp, guestIP, tp)
			p.portAlloc.Release(hp)
		}
	}

	// Clean up networking.
```

- [ ] **Step 2: Verify compilation**

Run: `CGO_ENABLED=0 go build -tags firecracker ./internal/provider/firecracker/`
Expected: Compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/firecracker/sandbox.go
git commit -m "feat(firecracker): clean up port forwarding rules on StopSandbox"
```

---

### Task 10: Integration Test Changes

**Files:**
- Modify: `docker-compose.integration-firecracker.yml`
- Modify: `test/integration/e2e_test.go`
- Modify: `test/integration/snapshot_test.go`
- Modify: `test/integration/image_test.go`
- Modify: `test/integration/port_test.go`

**Reference:** `docs/superpowers/specs/2026-03-29-firecracker-phase2-design.md` — Success Criteria

- [ ] **Step 1: Update docker-compose to remove skip env vars and add snapshot-dir**

In `docker-compose.integration-firecracker.yml`:

1. Add `--snapshot-dir=/srv/firecracker/snapshots` to the navarisd command list.
2. Remove `NAVARIS_SKIP_SNAPSHOTS: "1"` and `NAVARIS_SKIP_PORTS: "1"` from the test-runner environment.

The test-runner environment section should look like:

```yaml
    environment:
      NAVARIS_API_URL: http://navarisd:8080
      NAVARIS_TOKEN: test-token
      NAVARIS_BASE_IMAGE: alpine-3.21
      NAVARIS_CLI: /usr/local/bin/navaris
```

- [ ] **Step 2: Remove snapshot skip guard from snapshot_test.go**

In `test/integration/snapshot_test.go`, remove these lines from the top of `TestSnapshotRestoreToSandbox`:

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Skip("snapshots not supported by this backend")
}
```

Also remove `"os"` from the imports if it's no longer used by the file. Check if any other code uses `os` — if not, remove the import.

- [ ] **Step 3: Remove snapshot skip guard from e2e_test.go**

In `test/integration/e2e_test.go`, remove these lines (after `t.Logf("sandbox stopped")`):

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Log("end-to-end lifecycle test passed (snapshot section skipped)")
    return
}
```

Check if `"os"` is used elsewhere in the file — it likely is (other env var reads or `os` calls). Only remove the import if no other references exist.

- [ ] **Step 4: Remove snapshot skip guard from image_test.go**

In `test/integration/image_test.go`, remove these lines from the top of `TestImagePromoteFromSnapshot`:

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Skip("snapshots not supported by this backend")
}
```

Check if `"os"` is still needed by the file.

- [ ] **Step 5: Remove port skip guard from port_test.go**

In `test/integration/port_test.go`, remove these lines from the top of `TestPortPublishListDelete`:

```go
if os.Getenv("NAVARIS_SKIP_PORTS") == "1" {
    t.Skip("port forwarding not supported by this backend")
}
```

Check if `"os"` is still needed by the file.

- [ ] **Step 6: Verify tests compile**

Run: `CGO_ENABLED=0 go test -tags integration -c -o /dev/null ./test/integration/`
Expected: Compiles successfully.

- [ ] **Step 7: Commit**

```bash
git add docker-compose.integration-firecracker.yml test/integration/e2e_test.go test/integration/snapshot_test.go test/integration/image_test.go test/integration/port_test.go
git commit -m "feat(firecracker): enable snapshot and port tests for Firecracker backend"
```
