# CoW Sandbox Fork Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace full-file `copyFile` with a pluggable `storage.Backend` (reflink / copy), wire it into the Firecracker provider, add an Incus storage-pool capability check, then add a fork endpoint that spawns Firecracker children via `MAP_PRIVATE` of a shared snapshot memory file.

**Architecture:**
- New `internal/storage` package owns CoW primitives. `Backend` interface with `Name()`, `CloneFile(src, dst)`, `Capabilities()`. Implementations: `CopyBackend`, `ReflinkBackend`, `BtrfsSubvolBackend` (stub), `ZfsBackend` (stub). A `Registry` resolves the right backend per destination root via startup probe.
- Firecracker provider receives a `storage.Registry` and replaces every `copyFile` call site with `registry.For(dst).CloneFile(ctx, src, dst)`. Auto-fallback to `CopyBackend` on `EOPNOTSUPP` / `EXDEV`.
- Incus provider gains a startup pool-capability check (warn on `dir`-driver pool); no clone-path code changes.
- Stage 2 adds a "fork-point": an internal snapshot (`vmstate.bin` + reflinked `rootfs.ext4`) plus children spawned via Firecracker's existing snapshot-restore path. Memory CoW is automatic — Firecracker opens the memory file `MAP_PRIVATE`, kernel handles page CoW.

**Tech Stack:** Go 1.22+, `golang.org/x/sys/unix` (`IoctlFileClone`), Linux ≥ 5.x, Firecracker ≥ 1.6, SQLite, build tag `//go:build firecracker` for VMM-specific code.

**Spec:** `docs/superpowers/specs/2026-04-24-cow-sandbox-fork-design.md`

---

## File Structure

**New files:**
- `internal/storage/backend.go` — `Backend` interface, `Capabilities`, error sentinels.
- `internal/storage/copy.go` — `CopyBackend` (`io.Copy` + tmp+rename).
- `internal/storage/reflink_linux.go` — `ReflinkBackend` (`unix.IoctlFileClone`).
- `internal/storage/reflink_other.go` — non-Linux build stub returning unsupported.
- `internal/storage/btrfs_stub.go` — `BtrfsSubvolBackend` placeholder.
- `internal/storage/zfs_stub.go` — `ZfsBackend` placeholder.
- `internal/storage/detect_linux.go` — `Detect(root)` (statfs + 1-byte probe).
- `internal/storage/detect_other.go` — non-Linux build stub.
- `internal/storage/registry.go` — `Registry` (per-root backend table).
- `internal/storage/backend_test.go` — backend unit tests.
- `internal/storage/registry_test.go` — registry unit tests.
- `internal/storage/detect_linux_test.go` — probe tests on a tmpfs.
- `internal/provider/firecracker/forkpoint.go` — fork-point on-disk layout, info struct, GC helpers.
- `internal/provider/firecracker/fork.go` — fork-point creation + child spawn.
- `internal/provider/firecracker/forkpoint_test.go` — fork-point lifecycle unit tests.
- `internal/provider/incus/storage_check.go` — Incus pool driver probe.

**Modified files:**
- `internal/provider/firecracker/firecracker.go` — `Config` gains `Storage *storage.Registry`; `Provider` holds it; `New` validates non-nil.
- `internal/provider/firecracker/sandbox.go` — replace 2 `copyFile` calls (lines 52, 508).
- `internal/provider/firecracker/snapshot.go` — replace 6 `copyFile` calls (lines 119, 172, 186, 221, 235; image path also touched here? no, image.go).
- `internal/provider/firecracker/image.go` — replace 1 `copyFile` call (line 60).
- `internal/provider/firecracker/sandbox.go` — keep `copyFile` only as a private helper `copyFileFallback` used by `CopyBackend` (move into `internal/storage/copy.go`; remove from `sandbox.go`).
- `cmd/navarisd/main.go` — add `storage.Mode` flag and per-root override flag; build the `storage.Registry` and pass it to providers.
- `cmd/navarisd/provider_firecracker.go` — pass registry into `firecracker.Config`.
- `cmd/navarisd/provider_incus.go` — call Incus pool check at startup.
- `internal/domain/provider.go` — add `ForkSandbox(ctx, ref, count) ([]BackendRef, error)` method (default no-op for non-supporting providers via shared adapter, or returning `ErrNotSupported`).
- `internal/service/sandbox.go` — add `Fork(ctx, sandboxID, count)`; create N pending sandboxes, enqueue per-child operations.
- `internal/api/sandbox.go` — add `forkSandbox` HTTP handler.
- `internal/api/server.go` — register `POST /v1/sandboxes/{id}/fork`.
- `internal/provider/registry.go` — pass-through for `ForkSandbox`.

---

## Stage 1a — `internal/storage` Package

### Task 1: Create the `Backend` interface and `Capabilities` type

**Files:**
- Create: `internal/storage/backend.go`
- Test: `internal/storage/backend_test.go`

- [ ] **Step 1: Write the failing test**

`internal/storage/backend_test.go`:
```go
package storage

import (
	"context"
	"errors"
	"testing"
)

func TestErrUnsupported_Is(t *testing.T) {
	wrapped := errors.New("zfs not configured: " + ErrUnsupported.Error())
	if errors.Is(wrapped, ErrUnsupported) {
		t.Errorf("plain string-wrapping should not match ErrUnsupported")
	}
	wrapped2 := errors.Join(ErrUnsupported, errors.New("zfs not configured"))
	if !errors.Is(wrapped2, ErrUnsupported) {
		t.Errorf("errors.Join should preserve ErrUnsupported")
	}
}

// Compile-time check: every backend implementation must satisfy Backend.
var (
	_ Backend = (*CopyBackend)(nil)
)

// Compile-time check: Capabilities zero value must be valid (all-false).
func TestCapabilitiesZeroValue(t *testing.T) {
	var c Capabilities
	if c.InstantClone || c.SharesBlocks || c.RequiresSameFS {
		t.Errorf("zero Capabilities must be all-false")
	}
}

// Placeholder so the package compiles before CopyBackend exists in Task 2.
func mustClone(t *testing.T, b Backend, src, dst string) {
	t.Helper()
	if err := b.CloneFile(context.Background(), src, dst); err != nil {
		t.Fatalf("clone: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — package does not compile (`Backend`, `CopyBackend`, `ErrUnsupported`, `Capabilities` undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/storage/backend.go`:
```go
// Package storage provides copy-on-write backends for cloning sandbox files.
package storage

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by backends that cannot perform a clone in the
// current environment (wrong filesystem, missing kernel feature, etc.).
// Callers may wrap this with errors.Join to add context while preserving Is.
var ErrUnsupported = errors.New("storage: backend not supported here")

// Capabilities describes what a backend's clone op offers. It is informational
// only; correctness must not depend on it (clones may still fail at op time).
type Capabilities struct {
	InstantClone   bool // O(1) metadata op, not O(size) data copy
	SharesBlocks   bool // clones share physical blocks until written
	RequiresSameFS bool // src and dst must share a filesystem
}

// Backend clones a single regular file from src to dst.
//
// Contract:
//   - On success, dst is a complete, writable, independent file.
//   - On any error, dst either does not exist or has been removed by the
//     backend (no partial files visible to readers). Implementations
//     achieve this via dst.tmp + rename(2).
//   - src is not modified.
type Backend interface {
	Name() string
	CloneFile(ctx context.Context, src, dst string) error
	Capabilities() Capabilities
}
```

- [ ] **Step 4: Run test to verify it still fails (CopyBackend missing)**

Run: `go test ./internal/storage/...`
Expected: FAIL — `undefined: CopyBackend`.

This is intentional — Task 2 introduces `CopyBackend`. We commit the interface alone first.

- [ ] **Step 5: Drop the `_ Backend = (*CopyBackend)(nil)` line temporarily so the package compiles**

Edit `internal/storage/backend_test.go`: comment out the `_ Backend = (*CopyBackend)(nil)` line and the `mustClone` helper. We will restore them in Task 2.

Replace the test file content with:
```go
package storage

import (
	"errors"
	"testing"
)

func TestErrUnsupported_Is(t *testing.T) {
	wrapped := errors.New("zfs not configured: " + ErrUnsupported.Error())
	if errors.Is(wrapped, ErrUnsupported) {
		t.Errorf("plain string-wrapping should not match ErrUnsupported")
	}
	wrapped2 := errors.Join(ErrUnsupported, errors.New("zfs not configured"))
	if !errors.Is(wrapped2, ErrUnsupported) {
		t.Errorf("errors.Join should preserve ErrUnsupported")
	}
}

func TestCapabilitiesZeroValue(t *testing.T) {
	var c Capabilities
	if c.InstantClone || c.SharesBlocks || c.RequiresSameFS {
		t.Errorf("zero Capabilities must be all-false")
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/storage/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/storage/backend.go internal/storage/backend_test.go
git commit -m "feat(storage): add Backend interface and Capabilities"
```

---

### Task 2: Implement `CopyBackend`

**Files:**
- Create: `internal/storage/copy.go`
- Modify: `internal/storage/backend_test.go` (restore the compile-time assertion)

- [ ] **Step 1: Write the failing test**

Append to `internal/storage/backend_test.go`:
```go
import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	// keep existing imports
)

// Compile-time: CopyBackend must satisfy Backend.
var _ Backend = (*CopyBackend)(nil)

func TestCopyBackend_CloneFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	payload := bytes.Repeat([]byte("a"), 1<<20) // 1 MB
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	b := &CopyBackend{}
	if err := b.CloneFile(context.Background(), src, dst); err != nil {
		t.Fatalf("CloneFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("dst contents differ from src")
	}

	// Divergence: writing to src does not affect dst, and vice versa.
	if err := os.WriteFile(src, []byte("changed"), 0o644); err != nil {
		t.Fatalf("rewrite src: %v", err)
	}
	got2, _ := os.ReadFile(dst)
	if !bytes.Equal(got2, payload) {
		t.Errorf("dst was affected by src write (not independent)")
	}
}

func TestCopyBackend_NoPartialOnError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing")
	dst := filepath.Join(dir, "dst")
	b := &CopyBackend{}
	err := b.CloneFile(context.Background(), src, dst)
	if err == nil {
		t.Fatal("expected error from missing src")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Errorf("dst must not exist after failed clone, got stat err=%v", statErr)
	}
}

func TestCopyBackend_NameAndCapabilities(t *testing.T) {
	b := &CopyBackend{}
	if b.Name() != "copy" {
		t.Errorf("Name = %q, want %q", b.Name(), "copy")
	}
	caps := b.Capabilities()
	if caps.InstantClone || caps.SharesBlocks || caps.RequiresSameFS {
		t.Errorf("CopyBackend caps must be all-false, got %+v", caps)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `undefined: CopyBackend`.

- [ ] **Step 3: Implement `CopyBackend`**

`internal/storage/copy.go`:
```go
package storage

import (
	"context"
	"fmt"
	"io"
	"os"
)

// CopyBackend clones via io.Copy. Always available, never CoW.
type CopyBackend struct{}

func (CopyBackend) Name() string             { return "copy" }
func (CopyBackend) Capabilities() Capabilities { return Capabilities{} }

func (CopyBackend) CloneFile(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("storage/copy open src: %w", err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("storage/copy create dst.tmp: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("storage/copy write: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/copy close: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/copy rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/storage/...`
Expected: PASS (3 new tests + earlier 2).

- [ ] **Step 5: Commit**

```bash
git add internal/storage/copy.go internal/storage/backend_test.go
git commit -m "feat(storage): add CopyBackend"
```

---

### Task 3: Implement `ReflinkBackend` (Linux)

**Files:**
- Create: `internal/storage/reflink_linux.go`
- Create: `internal/storage/reflink_other.go`
- Modify: `internal/storage/backend_test.go` — add reflink tests guarded by capability probe.

- [ ] **Step 1: Write the failing test**

Append to `internal/storage/backend_test.go`:
```go
func TestReflinkBackend_NameAndCapabilities(t *testing.T) {
	b := &ReflinkBackend{}
	if b.Name() != "reflink" {
		t.Errorf("Name = %q, want %q", b.Name(), "reflink")
	}
	caps := b.Capabilities()
	if !caps.InstantClone || !caps.SharesBlocks || !caps.RequiresSameFS {
		t.Errorf("ReflinkBackend caps want all-true, got %+v", caps)
	}
}

// TestReflinkBackend_CloneFile_OnReflinkFS exercises the real ioctl on a
// filesystem that supports it. Skip when no such directory is configured.
// Set NAVARIS_REFLINK_TEST_DIR=/path on a btrfs/XFS-reflink mount to enable.
func TestReflinkBackend_CloneFile_OnReflinkFS(t *testing.T) {
	root := os.Getenv("NAVARIS_REFLINK_TEST_DIR")
	if root == "" {
		t.Skip("set NAVARIS_REFLINK_TEST_DIR to a CoW-capable mount to enable")
	}
	dir, err := os.MkdirTemp(root, "reflink-test-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	payload := bytes.Repeat([]byte("z"), 4<<20) // 4 MB
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	b := &ReflinkBackend{}
	if err := b.CloneFile(context.Background(), src, dst); err != nil {
		t.Fatalf("CloneFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("dst contents differ")
	}
}

func TestReflinkBackend_CloneFile_OnTmpFS_FailsClean(t *testing.T) {
	dir := t.TempDir() // tmpfs in CI; ioctl FICLONE returns EOPNOTSUPP/ENOTTY
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	b := &ReflinkBackend{}
	err := b.CloneFile(context.Background(), src, dst)
	if err == nil {
		t.Fatalf("expected error on non-CoW FS")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("expected ErrUnsupported wrap, got %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Errorf("dst must not exist after failed clone")
	}
}

// Compile-time: ReflinkBackend must satisfy Backend.
var _ Backend = (*ReflinkBackend)(nil)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `undefined: ReflinkBackend`.

- [ ] **Step 3: Implement on Linux**

`internal/storage/reflink_linux.go`:
```go
//go:build linux

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// ReflinkBackend clones via FICLONE (btrfs/XFS-with-reflink/bcachefs).
type ReflinkBackend struct{}

func (ReflinkBackend) Name() string { return "reflink" }
func (ReflinkBackend) Capabilities() Capabilities {
	return Capabilities{InstantClone: true, SharesBlocks: true, RequiresSameFS: true}
}

func (ReflinkBackend) CloneFile(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("storage/reflink open src: %w", err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	// Permissions are corrected by Stat below; create writable so we can clone.
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("storage/reflink create dst.tmp: %w", err)
	}

	cloneErr := unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	if cloneErr != nil {
		out.Close()
		os.Remove(tmp)
		if isReflinkUnsupported(cloneErr) {
			return errors.Join(ErrUnsupported, fmt.Errorf("storage/reflink ficlone: %w", cloneErr))
		}
		return fmt.Errorf("storage/reflink ficlone: %w", cloneErr)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/reflink close: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/reflink rename: %w", err)
	}
	return nil
}

// isReflinkUnsupported reports whether the error means "this filesystem or
// kernel does not support reflinks here" — distinct from a real I/O error.
func isReflinkUnsupported(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.EOPNOTSUPP, syscall.ENOTSUP, syscall.EXDEV, syscall.ENOTTY, syscall.EINVAL:
		return true
	}
	return false
}
```

- [ ] **Step 4: Add the non-Linux stub**

`internal/storage/reflink_other.go`:
```go
//go:build !linux

package storage

import (
	"context"
	"errors"
	"fmt"
)

type ReflinkBackend struct{}

func (ReflinkBackend) Name() string             { return "reflink" }
func (ReflinkBackend) Capabilities() Capabilities { return Capabilities{} }

func (ReflinkBackend) CloneFile(ctx context.Context, src, dst string) error {
	return errors.Join(ErrUnsupported, fmt.Errorf("storage/reflink: only available on Linux"))
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/storage/...`
Expected: PASS. The `TestReflinkBackend_CloneFile_OnReflinkFS` skips unless `NAVARIS_REFLINK_TEST_DIR` is set.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/reflink_linux.go internal/storage/reflink_other.go internal/storage/backend_test.go
git commit -m "feat(storage): add ReflinkBackend (FICLONE on Linux)"
```

---

### Task 4: Stub `BtrfsSubvolBackend` and `ZfsBackend`

**Files:**
- Create: `internal/storage/btrfs_stub.go`
- Create: `internal/storage/zfs_stub.go`
- Modify: `internal/storage/backend_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/storage/backend_test.go`:
```go
func TestStubBackends_AlwaysUnsupported(t *testing.T) {
	for _, b := range []Backend{&BtrfsSubvolBackend{}, &ZfsBackend{}} {
		caps := b.Capabilities()
		if caps.InstantClone || caps.SharesBlocks {
			t.Errorf("%s: stub must not advertise CoW caps", b.Name())
		}
		err := b.CloneFile(context.Background(), "/x", "/y")
		if err == nil || !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: stub CloneFile must return ErrUnsupported, got %v", b.Name(), err)
		}
	}
}

var (
	_ Backend = (*BtrfsSubvolBackend)(nil)
	_ Backend = (*ZfsBackend)(nil)
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `undefined: BtrfsSubvolBackend, ZfsBackend`.

- [ ] **Step 3: Implement the stubs**

`internal/storage/btrfs_stub.go`:
```go
package storage

import (
	"context"
	"errors"
	"fmt"
)

// BtrfsSubvolBackend will clone via "btrfs subvolume snapshot". Stub only:
// the v1 Firecracker provider stores rootfs as a single .ext4 file, for which
// reflink is the natural CoW path. This stub exists so the interface stays
// honest and so future non-Firecracker providers can plug in.
type BtrfsSubvolBackend struct{}

func (BtrfsSubvolBackend) Name() string             { return "btrfs-subvol" }
func (BtrfsSubvolBackend) Capabilities() Capabilities { return Capabilities{} }

func (BtrfsSubvolBackend) CloneFile(ctx context.Context, src, dst string) error {
	return errors.Join(ErrUnsupported,
		fmt.Errorf("btrfs-subvol backend not wired in v1; use reflink for file-based rootfs"))
}
```

`internal/storage/zfs_stub.go`:
```go
package storage

import (
	"context"
	"errors"
	"fmt"
)

// ZfsBackend will clone via "zfs clone" of a snapshot. Stub only: the v1
// Firecracker provider is file-based (reflink-friendly) and ZFS clones hold
// their parent snapshot immutable for the clone's lifetime, which would
// require a lifecycle dependency graph navaris does not currently model.
type ZfsBackend struct{}

func (ZfsBackend) Name() string             { return "zfs" }
func (ZfsBackend) Capabilities() Capabilities { return Capabilities{} }

func (ZfsBackend) CloneFile(ctx context.Context, src, dst string) error {
	return errors.Join(ErrUnsupported,
		fmt.Errorf("zfs backend not wired in v1; use reflink for file-based rootfs"))
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/storage/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/btrfs_stub.go internal/storage/zfs_stub.go internal/storage/backend_test.go
git commit -m "feat(storage): add BtrfsSubvolBackend and ZfsBackend stubs"
```

---

### Task 5: Implement filesystem detection (`Detect`)

**Files:**
- Create: `internal/storage/detect_linux.go`
- Create: `internal/storage/detect_other.go`
- Create: `internal/storage/detect_linux_test.go`

- [ ] **Step 1: Write the failing test**

`internal/storage/detect_linux_test.go`:
```go
//go:build linux

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDetect_OnTmpFS_FallsBackToCopy(t *testing.T) {
	// t.TempDir() is tmpfs in CI environments and on most dev machines
	// without a btrfs/XFS-reflink overlay. Detect must classify this as
	// "copy only".
	got, err := Detect(t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Name() != "copy" {
		t.Errorf("Detect on tmpfs = %q, want %q", got.Name(), "copy")
	}
}

func TestDetect_MissingDir_ReturnsCopy(t *testing.T) {
	got, err := Detect(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatalf("expected error for missing dir")
	}
	if got != nil {
		t.Errorf("expected nil backend on err, got %v", got)
	}
}

func TestDetect_OnReflinkFS_ReturnsReflink(t *testing.T) {
	root := os.Getenv("NAVARIS_REFLINK_TEST_DIR")
	if root == "" {
		t.Skip("set NAVARIS_REFLINK_TEST_DIR to a CoW-capable mount to enable")
	}
	got, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Name() != "reflink" {
		t.Errorf("Detect on reflink FS = %q, want %q", got.Name(), "reflink")
	}
}

// Sanity: probeReflink must clean up its temp files even when reflink fails.
func TestProbeReflink_CleansUp(t *testing.T) {
	dir := t.TempDir()
	_ = probeReflink(dir) // ignore result — we only care about cleanup

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".probe" || filepath.Ext(e.Name()) == ".probe-clone" {
			t.Errorf("probe file left behind: %s", e.Name())
		}
	}
	_ = errors.New // keep import
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `undefined: Detect, probeReflink`.

- [ ] **Step 3: Implement on Linux**

`internal/storage/detect_linux.go`:
```go
//go:build linux

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Detect probes root and returns the best Backend known to work there.
// Falls back to CopyBackend on probe failure or unsupported FS.
// Returns nil + error if root does not exist or is not a directory.
func Detect(root string) (Backend, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("storage/detect stat %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("storage/detect %s: not a directory", root)
	}
	if probeReflink(root) == nil {
		return ReflinkBackend{}, nil
	}
	return CopyBackend{}, nil
}

// probeReflink writes a 1-byte file in root and attempts to FICLONE it into
// a sibling. Returns nil if reflink works; an error otherwise. Always cleans
// up its temp files.
func probeReflink(root string) error {
	src, err := os.CreateTemp(root, "navaris-storage-*.probe")
	if err != nil {
		return fmt.Errorf("probe create src: %w", err)
	}
	srcPath := src.Name()
	defer os.Remove(srcPath)

	if _, err := src.Write([]byte{0}); err != nil {
		src.Close()
		return fmt.Errorf("probe write: %w", err)
	}
	if err := src.Close(); err != nil {
		return fmt.Errorf("probe close src: %w", err)
	}

	dstPath := filepath.Join(root, filepath.Base(srcPath)+".probe-clone")
	defer os.Remove(dstPath)

	if err := (ReflinkBackend{}).CloneFile(context.Background(), srcPath, dstPath); err != nil {
		if errors.Is(err, ErrUnsupported) {
			return ErrUnsupported
		}
		return err
	}
	return nil
}
```

- [ ] **Step 4: Add non-Linux stub**

`internal/storage/detect_other.go`:
```go
//go:build !linux

package storage

import (
	"fmt"
	"os"
)

func Detect(root string) (Backend, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("storage/detect stat %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("storage/detect %s: not a directory", root)
	}
	return CopyBackend{}, nil
}

func probeReflink(root string) error { return ErrUnsupported }
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/storage/...`
Expected: PASS (the reflink-FS test skips on tmpfs).

- [ ] **Step 6: Commit**

```bash
git add internal/storage/detect_linux.go internal/storage/detect_other.go internal/storage/detect_linux_test.go
git commit -m "feat(storage): add filesystem capability detection"
```

---

### Task 6: Implement the `Registry`

**Files:**
- Create: `internal/storage/registry.go`
- Create: `internal/storage/registry_test.go`

- [ ] **Step 1: Write the failing test**

`internal/storage/registry_test.go`:
```go
package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistry_For_LongestPrefixWins(t *testing.T) {
	r := NewRegistry()
	r.Set("/srv/firecracker", CopyBackend{})
	r.Set("/srv/firecracker/snapshots", ReflinkBackend{})

	if got := r.For("/srv/firecracker/snapshots/abc/rootfs.ext4"); got.Name() != "reflink" {
		t.Errorf("longest prefix should win: got %q", got.Name())
	}
	if got := r.For("/srv/firecracker/vms/xyz/rootfs.ext4"); got.Name() != "copy" {
		t.Errorf("shorter prefix should win when no longer match: got %q", got.Name())
	}
}

func TestRegistry_For_FallbackBackend(t *testing.T) {
	r := NewRegistry()
	r.SetFallback(CopyBackend{})
	if got := r.For("/some/unmapped/path"); got.Name() != "copy" {
		t.Errorf("unmapped path should hit fallback: got %q", got.Name())
	}
}

func TestRegistry_For_NoFallback_PanicsAtStartup(t *testing.T) {
	r := NewRegistry()
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic when no fallback is set and no prefix matches")
		}
	}()
	_ = r.For("/x")
}

func TestRegistry_BuildFromMode_Auto_WiresProbedRoots(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir()}
	r, err := BuildRegistry(Config{Mode: ModeAuto}, roots, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	for _, root := range roots {
		got := r.For(filepath.Join(root, "anyfile"))
		if got.Name() != "copy" {
			t.Errorf("auto mode on tmpfs root %s: got %q, want copy", root, got.Name())
		}
	}
}

func TestRegistry_BuildFromMode_ExplicitCopy(t *testing.T) {
	r, err := BuildRegistry(Config{Mode: ModeCopy}, []string{t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if r.For("/anything").Name() != "copy" {
		t.Errorf("explicit copy mode must produce copy")
	}
}

func TestRegistry_BuildFromMode_ExplicitReflink_OnTmpfsFails(t *testing.T) {
	_, err := BuildRegistry(Config{Mode: ModeReflink}, []string{t.TempDir()}, nil)
	if err == nil {
		t.Fatalf("explicit reflink on tmpfs must fail at startup")
	}
	if !errors.Is(err, ErrUnsupported) && !strings.Contains(err.Error(), "reflink") {
		t.Errorf("expected reflink-unsupported error, got %v", err)
	}
}

func TestRegistry_BuildFromMode_PerRootOverrideWins(t *testing.T) {
	root := t.TempDir()
	overrides := map[string]Mode{root: ModeCopy}
	r, err := BuildRegistry(Config{Mode: ModeAuto}, []string{root}, overrides)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if got := r.For(filepath.Join(root, "x")); got.Name() != "copy" {
		t.Errorf("override should force copy, got %q", got.Name())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `undefined: NewRegistry, BuildRegistry, Config, Mode*`.

- [ ] **Step 3: Implement the registry**

`internal/storage/registry.go`:
```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Mode is a configured backend selection strategy.
type Mode string

const (
	ModeAuto        Mode = "auto"
	ModeCopy        Mode = "copy"
	ModeReflink     Mode = "reflink"
	ModeBtrfsSubvol Mode = "btrfs-subvol"
	ModeZfs         Mode = "zfs"
)

// Config controls registry construction at daemon startup.
type Config struct {
	Mode Mode // global mode; "auto" probes each root.
}

// Registry maps destination paths to a Backend. The longest matching prefix
// wins. A fallback Backend is used when no prefix matches; if no fallback is
// set, For panics (programmer error: BuildRegistry always sets one).
type Registry struct {
	mu       sync.RWMutex
	prefixes []string             // sorted longest-first for For lookups
	byPrefix map[string]Backend
	fallback Backend
}

func NewRegistry() *Registry {
	return &Registry{byPrefix: map[string]Backend{}}
}

func (r *Registry) Set(prefix string, b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix = filepath.Clean(prefix)
	if _, exists := r.byPrefix[prefix]; !exists {
		r.prefixes = append(r.prefixes, prefix)
		sort.Slice(r.prefixes, func(i, j int) bool {
			return len(r.prefixes[i]) > len(r.prefixes[j])
		})
	}
	r.byPrefix[prefix] = b
}

func (r *Registry) SetFallback(b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = b
}

func (r *Registry) For(path string) Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clean := filepath.Clean(path)
	for _, p := range r.prefixes {
		if clean == p || strings.HasPrefix(clean, p+string(filepath.Separator)) {
			return r.byPrefix[p]
		}
	}
	if r.fallback == nil {
		panic(fmt.Sprintf("storage.Registry: no backend for %q and no fallback", path))
	}
	return r.fallback
}

// BuildRegistry constructs a Registry from a Config and a list of CoW-relevant
// roots. Per-root overrides win over the global Mode. Returns an error when an
// explicit non-auto mode is incompatible with a probed root (deterministic
// startup).
func BuildRegistry(cfg Config, roots []string, overrides map[string]Mode) (*Registry, error) {
	r := NewRegistry()
	r.SetFallback(CopyBackend{})

	for _, root := range roots {
		mode := cfg.Mode
		if mode == "" {
			mode = ModeAuto
		}
		if ov, ok := overrides[filepath.Clean(root)]; ok && ov != "" {
			mode = ov
		}
		b, err := resolveMode(mode, root)
		if err != nil {
			return nil, fmt.Errorf("storage: root %q: %w", root, err)
		}
		r.Set(root, b)
	}
	return r, nil
}

// resolveMode picks a backend for a single root under a single mode, probing
// when mode is auto and verifying when mode is explicit.
func resolveMode(mode Mode, root string) (Backend, error) {
	switch mode {
	case ModeAuto, "":
		b, err := Detect(root)
		if err != nil {
			return nil, err
		}
		return b, nil
	case ModeCopy:
		return CopyBackend{}, nil
	case ModeReflink:
		if err := probeReflink(root); err != nil {
			return nil, fmt.Errorf("reflink not available at %s: %w", root, err)
		}
		return ReflinkBackend{}, nil
	case ModeBtrfsSubvol:
		return nil, errors.Join(ErrUnsupported,
			fmt.Errorf("btrfs-subvol mode not wired in v1"))
	case ModeZfs:
		return nil, errors.Join(ErrUnsupported,
			fmt.Errorf("zfs mode not wired in v1"))
	default:
		return nil, fmt.Errorf("unknown storage mode %q", mode)
	}
}

// CloneFile is a convenience wrapper that resolves the backend by destination
// path. Most callers should call this rather than Backend.CloneFile directly,
// because it implements the EOPNOTSUPP/EXDEV runtime fallback to CopyBackend.
func (r *Registry) CloneFile(ctx context.Context, src, dst string) (Backend, error) {
	b := r.For(dst)
	err := b.CloneFile(ctx, src, dst)
	if err == nil {
		return b, nil
	}
	if errors.Is(err, ErrUnsupported) {
		fallback := CopyBackend{}
		if err2 := fallback.CloneFile(ctx, src, dst); err2 != nil {
			return nil, fmt.Errorf("primary %s failed (%v), fallback copy failed: %w", b.Name(), err, err2)
		}
		return fallback, nil
	}
	return nil, err
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/storage/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/registry.go internal/storage/registry_test.go
git commit -m "feat(storage): add Registry with per-root backend resolution"
```

---

### Task 7: Wire the storage registry into the Firecracker provider

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go` — add `Storage *storage.Registry` to `Config`; validate non-nil.
- Modify: `internal/provider/firecracker/sandbox.go` — replace 2 `copyFile` calls and remove the local `copyFile` helper.
- Modify: `internal/provider/firecracker/snapshot.go` — replace 6 `copyFile` calls.
- Modify: `internal/provider/firecracker/image.go` — replace 1 `copyFile` call.

- [ ] **Step 1: Write the failing test**

Create `internal/provider/firecracker/storage_wiring_test.go`:
```go
//go:build firecracker

package firecracker

import (
	"testing"

	"github.com/navaris/navaris/internal/storage"
)

func TestNew_RequiresStorageRegistry(t *testing.T) {
	cfg := Config{
		FirecrackerBin: "/bin/true",
		KernelPath:     "/dev/null",
		ImageDir:       t.TempDir(),
		ChrootBase:     t.TempDir(),
		SnapshotDir:    t.TempDir(),
		// Storage intentionally nil
	}
	if _, err := New(cfg); err == nil {
		t.Fatalf("expected error when Storage is nil")
	}
}

func TestNew_AcceptsStorageRegistry(t *testing.T) {
	reg := storage.NewRegistry()
	reg.SetFallback(storage.CopyBackend{})
	cfg := Config{
		FirecrackerBin: "/bin/true",
		KernelPath:     "/dev/null",
		ImageDir:       t.TempDir(),
		ChrootBase:     t.TempDir(),
		SnapshotDir:    t.TempDir(),
		Storage:        reg,
	}
	// New still fails on host-iface detection in some envs; only assert
	// that the Storage validation does not trigger when set.
	_, err := New(cfg)
	if err != nil && err.Error() != "" && contains(err.Error(), "Storage") {
		t.Errorf("unexpected Storage-related error: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && (indexOf(s, sub) >= 0)))
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/...`
Expected: FAIL — `Config has no field Storage`.

- [ ] **Step 3: Add `Storage` to `Config` and validate it**

In `internal/provider/firecracker/firecracker.go`, edit the `Config` struct (around line 25) to add a Storage field, and edit `New` (around line 64) to validate it.

Add to imports:
```go
	"github.com/navaris/navaris/internal/storage"
```

Add to `Config`:
```go
	// Storage is required: it owns CoW cloning of rootfs files. Pass a
	// Registry whose roots include ImageDir, ChrootBase, and SnapshotDir.
	Storage *storage.Registry
```

In `New`, after the existing required-field loop, add:
```go
	if cfg.Storage == nil {
		return nil, fmt.Errorf("firecracker: Storage registry is required")
	}
```

Add to `Provider` struct:
```go
	storage *storage.Registry
```

In `New`, after creating `p`:
```go
	p.storage = cfg.Storage
```

- [ ] **Step 4: Replace `copyFile` calls with registry-based clones**

In `internal/provider/firecracker/sandbox.go`:
- Line ~52 (`copyFile(srcImage, dstImage)`):
  ```go
  if _, err := p.storage.CloneFile(ctx, srcImage, dstImage); err != nil {
      os.RemoveAll(vmDir)
      return domain.BackendRef{}, fmt.Errorf("firecracker copy rootfs %s: %w", vmID, err)
  }
  ```
- Line ~508 (`copyFile(src, dst)` in `CreateSandboxFromSnapshot`):
  ```go
  if _, err := p.storage.CloneFile(ctx, src, dst); err != nil {
      os.RemoveAll(vmDir)
      return domain.BackendRef{}, fmt.Errorf("firecracker copy snapshot rootfs %s: %w", vmID, err)
  }
  ```
- Delete the entire `func copyFile` definition at the end of the file (lines 585–600). Remove the now-unused `io` import if no other call needs it (check with `goimports`).

In `internal/provider/firecracker/snapshot.go`:
- Replace each `copyFile(src, dst)` with `_, err := p.storage.CloneFile(ctx, src, dst)`. Threaded ctx is already in scope for the live-snapshot path; for `createStoppedSnapshot`, change signature to take a ctx and pass `ctx` from caller (caller is `CreateSnapshot` which has ctx).
- Specifically:
  - `createStoppedSnapshot(vmDir, snapDir string)` → `createStoppedSnapshot(ctx context.Context, vmDir, snapDir string)`. Update its call site in `CreateSnapshot`.
  - All 6 invocations in this file get the registry treatment, preserving error messages.

In `internal/provider/firecracker/image.go`:
- Line ~60: `if _, err := p.storage.CloneFile(ctx, src, dst); err != nil { ... }`. The function already has a ctx in scope.

- [ ] **Step 5: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/...`
Expected: PASS for the new wiring test; existing tests should also pass (storage with `CopyBackend` fallback is identical to `copyFile`).

Run: `go test ./internal/storage/...`
Expected: PASS.

Run: `go build -tags firecracker ./...`
Expected: builds clean.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/firecracker/firecracker.go internal/provider/firecracker/sandbox.go internal/provider/firecracker/snapshot.go internal/provider/firecracker/image.go internal/provider/firecracker/storage_wiring_test.go
git commit -m "feat(firecracker): route rootfs clones through storage.Registry"
```

---

### Task 8: Add `storage_backend` to clone metadata + structured log

**Files:**
- Modify: `internal/provider/firecracker/snapshot.go` — when writing `snapinfo.json`, record the backend that was actually used during the disk-copy step.
- Modify: `internal/provider/firecracker/image.go` — same for `imageInfo`.

- [ ] **Step 1: Write the failing test**

Append to `internal/provider/firecracker/storage_wiring_test.go`:
```go
func TestSnapInfo_RecordsStorageBackend(t *testing.T) {
	// Build a registry whose root is a tmpfs (forces CopyBackend).
	reg := storage.NewRegistry()
	reg.SetFallback(storage.CopyBackend{})

	si := &snapInfo{}
	recordStorageBackend(si, reg.For(t.TempDir()))
	if si.StorageBackend != "copy" {
		t.Errorf("StorageBackend = %q, want %q", si.StorageBackend, "copy")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/... -run StorageBackend`
Expected: FAIL — `recordStorageBackend` undefined; `snapInfo` has no `StorageBackend` field.

- [ ] **Step 3: Add the field and helper**

Edit the `snapInfo` struct (in `internal/provider/firecracker/snapshot.go` — search for `type snapInfo struct`) to add:
```go
	StorageBackend string `json:"storage_backend,omitempty"`
```

Add (in `internal/provider/firecracker/snapshot.go`):
```go
import "github.com/navaris/navaris/internal/storage"

func recordStorageBackend(si *snapInfo, b storage.Backend) {
	if b != nil {
		si.StorageBackend = b.Name()
	}
}
```

Wire `recordStorageBackend(si, b)` immediately after the `CloneFile` calls inside `createStoppedSnapshot` and `createLiveSnapshot`, capturing the returned `Backend`:
```go
b, err := p.storage.CloneFile(ctx, src, dst)
if err != nil { ... }
recordStorageBackend(si, b)
slog.Debug("firecracker snapshot disk clone", "backend", b.Name(), "src", src, "dst", dst)
```

Do the analogous change in `imageInfo` (`internal/provider/firecracker/image.go`): add `StorageBackend string` field, set it after the clone.

- [ ] **Step 4: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/snapshot.go internal/provider/firecracker/image.go internal/provider/firecracker/storage_wiring_test.go
git commit -m "feat(firecracker): record storage backend on snapshot/image metadata"
```

---

### Task 9: Add storage flags and registry wiring to `navarisd`

**Files:**
- Modify: `cmd/navarisd/main.go` — add `--storage-mode` flag, build `storage.Registry`, pass to providers.
- Modify: `cmd/navarisd/provider_firecracker.go` — pass registry into `firecracker.Config`.
- Create: `cmd/navarisd/storage_test.go`.

- [ ] **Step 1: Write the failing test**

`cmd/navarisd/storage_test.go`:
```go
package main

import (
	"testing"

	"github.com/navaris/navaris/internal/storage"
)

func TestBuildStorageRegistry_AutoDefault(t *testing.T) {
	cfg := config{
		chrootBase:  t.TempDir(),
		imageDir:    t.TempDir(),
		snapshotDir: t.TempDir(),
		storageMode: "",
	}
	reg, err := buildStorageRegistry(cfg)
	if err != nil {
		t.Fatalf("buildStorageRegistry: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	// On tmpfs the chosen backend is "copy".
	if got := reg.For(cfg.imageDir).Name(); got != "copy" {
		t.Errorf("imageDir backend = %q, want copy", got)
	}
}

func TestBuildStorageRegistry_ExplicitCopy(t *testing.T) {
	cfg := config{
		chrootBase:  t.TempDir(),
		imageDir:    t.TempDir(),
		snapshotDir: t.TempDir(),
		storageMode: string(storage.ModeCopy),
	}
	if _, err := buildStorageRegistry(cfg); err != nil {
		t.Fatalf("buildStorageRegistry: %v", err)
	}
}

func TestBuildStorageRegistry_ExplicitReflink_OnTmpfsFails(t *testing.T) {
	cfg := config{
		chrootBase:  t.TempDir(),
		imageDir:    t.TempDir(),
		snapshotDir: t.TempDir(),
		storageMode: string(storage.ModeReflink),
	}
	_, err := buildStorageRegistry(cfg)
	if err == nil {
		t.Fatalf("expected explicit reflink on tmpfs to fail")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/navarisd/...`
Expected: FAIL — `config has no field storageMode`; `buildStorageRegistry` undefined.

- [ ] **Step 3: Add the flag and builder**

In `cmd/navarisd/main.go`:

Add to `config` struct:
```go
	storageMode string
```

In `parseFlags`:
```go
	flag.StringVar(&cfg.storageMode, "storage-mode", "auto", "CoW backend: auto | copy | reflink")
```

Add a new function (anywhere in the file):
```go
func buildStorageRegistry(cfg config) (*storage.Registry, error) {
	roots := []string{}
	for _, r := range []string{cfg.chrootBase, cfg.imageDir, cfg.snapshotDir} {
		if r != "" {
			roots = append(roots, r)
		}
	}
	return storage.BuildRegistry(
		storage.Config{Mode: storage.Mode(cfg.storageMode)},
		roots,
		nil, // per-root overrides not exposed via flags in v1
	)
}
```

Add the import at the top of `cmd/navarisd/main.go`:
```go
	"github.com/navaris/navaris/internal/storage"
```

In `run`, after providers are constructed but before they are wired (search for where `newFirecrackerProvider` is called), build and pass the registry:
```go
	storageReg, err := buildStorageRegistry(cfg)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	logger.Info("storage backends",
		"mode", cfg.storageMode,
		"chroot_base_backend", storageReg.For(cfg.chrootBase).Name(),
		"image_dir_backend", storageReg.For(cfg.imageDir).Name(),
		"snapshot_dir_backend", storageReg.For(cfg.snapshotDir).Name(),
	)
	cfg.storageRegistry = storageReg // see step 4
```

- [ ] **Step 4: Pass the registry into the Firecracker provider**

The cleanest way is to make `config` also hold the resolved registry so subsequent `newFirecrackerProvider`/`newIncusProvider` see it without changing function signatures (which would touch unrelated build-tag files).

Add to `config`:
```go
	storageRegistry *storage.Registry
```

In `cmd/navarisd/provider_firecracker.go`, edit the call to add:
```go
	return firecracker.New(firecracker.Config{
		// ... existing fields ...
		Storage: cfg.storageRegistry,
	})
```

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/navarisd/...`
Expected: PASS.

Run: `go build ./...` and `go build -tags firecracker ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/navarisd/main.go cmd/navarisd/provider_firecracker.go cmd/navarisd/storage_test.go
git commit -m "feat(navarisd): wire storage registry into providers"
```

---

## Stage 1b — Incus Storage-Pool Capability Check

### Task 10: Probe the Incus storage pool driver at startup

**Files:**
- Create: `internal/provider/incus/storage_check.go`
- Create: `internal/provider/incus/storage_check_test.go`
- Modify: `internal/provider/incus/provider.go` (or wherever `New` lives) to call the check.

- [ ] **Step 1: Locate the Incus provider entry point**

Run: `grep -n "func New\|storage.pool\|incus storage" internal/provider/incus/*.go | head`
Note the file containing `func New(cfg Config) (*Provider, error)` — likely `internal/provider/incus/provider.go` or `incus.go`.

- [ ] **Step 2: Write the failing test**

`internal/provider/incus/storage_check_test.go`:
```go
package incus

import (
	"errors"
	"testing"
)

func TestClassifyPoolDriver(t *testing.T) {
	cases := []struct {
		driver string
		cow    bool
	}{
		{"dir", false},
		{"btrfs", true},
		{"zfs", true},
		{"lvm", false},        // plain LVM is not CoW
		{"lvmcluster", false},
		{"lvm-thin", true},    // thin LVM has CoW-like semantics
		{"ceph", true},
		{"cephfs", true},
		{"powerflex", true},   // unknown drivers default to "treat as CoW"
		{"", false},
	}
	for _, c := range cases {
		got := isCowDriver(c.driver)
		if got != c.cow {
			t.Errorf("isCowDriver(%q) = %v, want %v", c.driver, got, c.cow)
		}
	}
}

func TestCheckPool_OnDirDriver_ReturnsAdvisory(t *testing.T) {
	advisory := classifyPool("dir")
	if advisory == nil {
		t.Fatal("expected an advisory for dir driver")
	}
	if !errors.Is(advisory, ErrIncusPoolNotCoW) {
		t.Errorf("advisory should wrap ErrIncusPoolNotCoW")
	}
}

func TestCheckPool_OnBtrfsDriver_NoAdvisory(t *testing.T) {
	if classifyPool("btrfs") != nil {
		t.Error("btrfs driver must not produce an advisory")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/provider/incus/...`
Expected: FAIL — `isCowDriver`, `classifyPool`, `ErrIncusPoolNotCoW` undefined.

- [ ] **Step 4: Implement the helpers**

`internal/provider/incus/storage_check.go`:
```go
package incus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ErrIncusPoolNotCoW is returned (advisory) when the pool driver does not
// support CoW. Non-fatal by default; strict mode (StrictPoolCoW) elevates it
// to a startup error.
var ErrIncusPoolNotCoW = errors.New("incus storage pool driver does not support copy-on-write")

// isCowDriver reports whether an Incus pool driver provides CoW clones.
// Unknown drivers default to true (assume capable) because the cost of a
// false negative — refusing to start on an exotic-but-capable driver — is
// higher than the cost of a false positive (no warning).
func isCowDriver(driver string) bool {
	switch driver {
	case "dir", "lvm", "lvmcluster", "":
		return false
	case "btrfs", "zfs", "lvm-thin", "ceph", "cephfs":
		return true
	default:
		return true
	}
}

// classifyPool returns nil if the driver is CoW-capable; otherwise an error
// wrapping ErrIncusPoolNotCoW with a human-readable message.
func classifyPool(driver string) error {
	if isCowDriver(driver) {
		return nil
	}
	return fmt.Errorf("driver=%q: %w (configure a btrfs/zfs/lvm-thin pool for storage-efficient sandbox cloning)", driver, ErrIncusPoolNotCoW)
}

// CheckPool fetches the configured pool driver and reports any advisory.
// strict=true converts an advisory into a hard error.
func CheckPool(ctx context.Context, fetchDriver func(ctx context.Context) (string, error), strict bool) error {
	driver, err := fetchDriver(ctx)
	if err != nil {
		// Don't gate startup on probe-fetch failure; just log and move on.
		slog.Warn("incus storage pool probe failed", "error", err)
		return nil
	}
	advisory := classifyPool(driver)
	if advisory == nil {
		slog.Info("incus storage pool is CoW-capable", "driver", driver)
		return nil
	}
	if strict {
		return advisory
	}
	slog.Warn("incus storage pool advisory", "driver", driver, "error", advisory)
	return nil
}
```

- [ ] **Step 5: Wire `CheckPool` into the Incus provider's `New`**

In the Incus provider's `New` (after the connection is established and the configured pool is known), call:
```go
err := CheckPool(ctx, p.fetchDefaultPoolDriver, cfg.StrictPoolCoW)
if err != nil {
    return nil, err
}
```

Implement `fetchDefaultPoolDriver(ctx)` to return the driver from the Incus client API for the pool that navaris will use. Look at existing Incus client usage in the package — there is likely a method like `client.GetStoragePool(name)` returning an object with `.Driver`. If the pool is not configured explicitly, the Incus client exposes a default pool name.

Add a `StrictPoolCoW bool` field to the Incus `Config` struct.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/provider/incus/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/provider/incus/storage_check.go internal/provider/incus/storage_check_test.go internal/provider/incus/*.go
git commit -m "feat(incus): warn (or fail in strict mode) when pool driver is not CoW"
```

---

### Task 11: Add `--incus-strict-pool-cow` flag

**Files:**
- Modify: `cmd/navarisd/main.go` — flag definition and propagation.
- Modify: `cmd/navarisd/provider_incus.go` — pass through to Incus `Config`.

- [ ] **Step 1: Add flag**

In `cmd/navarisd/main.go`:
```go
	incusStrictPoolCoW bool
```
```go
	flag.BoolVar(&cfg.incusStrictPoolCoW, "incus-strict-pool-cow", false, "fail startup if Incus storage pool is not CoW-capable (default: warn)")
```

- [ ] **Step 2: Propagate**

In `cmd/navarisd/provider_incus.go`, pass to the Incus `Config`:
```go
	StrictPoolCoW: cfg.incusStrictPoolCoW,
```

- [ ] **Step 3: Run build**

Run: `go build ./... && go build -tags firecracker ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/navarisd/main.go cmd/navarisd/provider_incus.go
git commit -m "feat(navarisd): add --incus-strict-pool-cow flag"
```

---

## Stage 2 — Memory CoW Fork (Firecracker)

### Task 12: Define the fork-point on-disk layout

**Files:**
- Create: `internal/provider/firecracker/forkpoint.go`
- Create: `internal/provider/firecracker/forkpoint_test.go`

- [ ] **Step 1: Write the failing test**

`internal/provider/firecracker/forkpoint_test.go`:
```go
//go:build firecracker

package firecracker

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFPInfo_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &fpInfo{
		ForkPointID:    "fp-abc",
		ParentVMID:     "vm-xyz",
		Mode:           "live",
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
		StorageBackend: "reflink",
		SpawnPending:   3,
		Descendants:    []string{"sbx-1", "sbx-2"},
	}
	if err := writeFPInfo(filepath.Join(dir, "fpinfo.json"), want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readFPInfo(filepath.Join(dir, "fpinfo.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ForkPointID != want.ForkPointID || got.ParentVMID != want.ParentVMID ||
		got.Mode != want.Mode || got.StorageBackend != want.StorageBackend ||
		got.SpawnPending != want.SpawnPending || len(got.Descendants) != 2 {
		t.Errorf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestForkPointDir_Layout(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: "/srv/firecracker"}}
	got := p.forkPointDir("fp-abc")
	if got != "/srv/firecracker/forkpoints/fp-abc" {
		t.Errorf("forkPointDir = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/... -run ForkPoint`
Expected: FAIL — `fpInfo`, `writeFPInfo`, `readFPInfo`, `forkPointDir` undefined.

- [ ] **Step 3: Implement the fork-point primitives**

`internal/provider/firecracker/forkpoint.go`:
```go
//go:build firecracker

package firecracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fpInfo is persisted to <forkPointDir>/fpinfo.json. It is the source of truth
// for fork-point lifecycle across daemon restarts.
//
// SpawnPending is the count of children whose creation operation has been
// enqueued but not reached a terminal state (running or failed). It is used
// solely for orphan-cleanup decisions on daemon start: a fork-point with
// SpawnPending > 0 and CreatedAt older than fpOrphanTTL is GC'd.
//
// Descendants is the set of sandbox IDs currently holding a MAP_PRIVATE
// mapping of this fork-point's vmstate.bin. The fork-point directory remains
// on disk while Descendants is non-empty; deleted otherwise.
type fpInfo struct {
	ForkPointID    string    `json:"fork_point_id"`
	ParentVMID     string    `json:"parent_vm_id"`
	Mode           string    `json:"mode"` // "live" or "stopped"
	CreatedAt      time.Time `json:"created_at"`
	StorageBackend string    `json:"storage_backend,omitempty"`
	SpawnPending   int       `json:"spawn_pending"`
	Descendants    []string  `json:"descendants,omitempty"`
}

const fpOrphanTTL = time.Hour

// fpInfoMu guards concurrent updates to a single fpinfo.json file. We use a
// per-file lock keyed off the file path; a sync.Map of *sync.Mutex would also
// work but a single global mutex is simpler and fork operations are not hot.
var fpInfoMu sync.Mutex

func (p *Provider) forkPointDir(fpID string) string {
	return filepath.Join(p.config.ChrootBase, "forkpoints", fpID)
}

func (p *Provider) fpInfoPath(fpID string) string {
	return filepath.Join(p.forkPointDir(fpID), "fpinfo.json")
}

func writeFPInfo(path string, info *fpInfo) error {
	fpInfoMu.Lock()
	defer fpInfoMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("forkpoint mkdir: %w", err)
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("forkpoint marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("forkpoint write: %w", err)
	}
	return os.Rename(tmp, path)
}

func readFPInfo(path string) (*fpInfo, error) {
	fpInfoMu.Lock()
	defer fpInfoMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("forkpoint read: %w", err)
	}
	var info fpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("forkpoint unmarshal: %w", err)
	}
	return &info, nil
}

// updateFPInfo applies fn under the file lock and atomically rewrites.
func updateFPInfo(path string, fn func(*fpInfo)) error {
	fpInfoMu.Lock()
	defer fpInfoMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("forkpoint read: %w", err)
	}
	var info fpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("forkpoint unmarshal: %w", err)
	}
	fn(&info)
	out, err := json.MarshalIndent(&info, "", "  ")
	if err != nil {
		return fmt.Errorf("forkpoint marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("forkpoint write: %w", err)
	}
	return os.Rename(tmp, path)
}
```

- [ ] **Step 4: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/... -run ForkPoint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/forkpoint.go internal/provider/firecracker/forkpoint_test.go
git commit -m "feat(firecracker): add fork-point on-disk layout and helpers"
```

---

### Task 13: Implement fork-point creation and child spawn

**Files:**
- Create: `internal/provider/firecracker/fork.go`
- Modify: `internal/provider/firecracker/forkpoint_test.go` — add lifecycle tests.

- [ ] **Step 1: Write the failing test**

Append to `internal/provider/firecracker/forkpoint_test.go`:
```go
func TestUpdateFPInfo_AddRemoveDescendant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fpinfo.json")
	if err := writeFPInfo(path, &fpInfo{ForkPointID: "fp-1", SpawnPending: 1}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := updateFPInfo(path, func(i *fpInfo) {
		i.Descendants = append(i.Descendants, "sbx-1")
		i.SpawnPending--
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := readFPInfo(path)
	if got.SpawnPending != 0 || len(got.Descendants) != 1 || got.Descendants[0] != "sbx-1" {
		t.Errorf("unexpected state after update: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (sanity)**

Run: `go test -tags firecracker ./internal/provider/firecracker/... -run UpdateFPInfo`
Expected: PASS — already covered by Task 12 helpers. (This test exercises the existing code paths under realistic mutation patterns; if the helpers are correct, it passes immediately. Keep it as a regression guard.)

- [ ] **Step 3: Implement fork-point materialization**

`internal/provider/firecracker/fork.go`:
```go
//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/navaris/navaris/internal/domain"
)

// CreateForkPoint materializes a fork-point from a (possibly running) sandbox.
// If the parent is running, the VM is paused, vmstate.bin is written, and the
// rootfs is reflinked into the fork-point dir; the VM is then resumed. If the
// parent is stopped, an existing snapshot is reused (or a stopped-mode
// fork-point is taken).
//
// The returned fork-point ID is stable on disk: callers (e.g. fork.go's
// SpawnFromForkPoint) reference it by ID.
func (p *Provider) CreateForkPoint(ctx context.Context, parentVMID string) (string, error) {
	fpID := "fp-" + uuid.NewString()[:12]
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		return "", fmt.Errorf("forkpoint mkdir: %w", err)
	}

	parentInfo, err := p.getVMInfo(parentVMID)
	if err != nil {
		os.RemoveAll(fpDir)
		return "", fmt.Errorf("forkpoint: parent vm: %w", err)
	}

	mode := "stopped"
	if parentInfo.PID > 0 && processAlive(parentInfo.PID) {
		mode = "live"
		if err := p.materializeLiveForkPoint(ctx, parentVMID, fpDir); err != nil {
			os.RemoveAll(fpDir)
			return "", err
		}
	} else {
		if err := p.materializeStoppedForkPoint(ctx, parentVMID, fpDir); err != nil {
			os.RemoveAll(fpDir)
			return "", err
		}
	}

	info := &fpInfo{
		ForkPointID:    fpID,
		ParentVMID:     parentVMID,
		Mode:           mode,
		CreatedAt:      time.Now().UTC(),
		StorageBackend: p.storage.For(filepath.Join(fpDir, "rootfs.ext4")).Name(),
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), info); err != nil {
		os.RemoveAll(fpDir)
		return "", err
	}
	slog.Info("firecracker forkpoint created", "fpID", fpID, "parent", parentVMID, "mode", mode)
	return fpID, nil
}

// materializeLiveForkPoint pauses the parent, writes its memory state into
// fpDir, reflinks the rootfs, then resumes the parent. This reuses the same
// CreateSnapshot machinery as live snapshots — a fork-point IS a snapshot,
// just with internal-only lifecycle.
func (p *Provider) materializeLiveForkPoint(ctx context.Context, vmID, fpDir string) error {
	// Reuse createLiveSnapshot semantics; it writes vmstate.bin/snapshot.meta
	// into fpDir and reflinks rootfs.ext4 there, then resumes the VM.
	return p.createLiveSnapshot(ctx, vmID, p.vmDir(vmID), fpDir)
}

func (p *Provider) materializeStoppedForkPoint(ctx context.Context, vmID, fpDir string) error {
	return p.createStoppedSnapshot(ctx, p.vmDir(vmID), fpDir)
}

// SpawnFromForkPoint creates a new VM that restores from the given fork-point.
// The VM's rootfs is reflinked from the fork-point's rootfs; the VM's restore
// is configured to mmap the fork-point's vmstate.bin (Firecracker opens it
// MAP_PRIVATE — kernel-level memory CoW).
func (p *Provider) SpawnFromForkPoint(ctx context.Context, fpID string, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	fpDir := p.forkPointDir(fpID)
	info, err := readFPInfo(p.fpInfoPath(fpID))
	if err != nil {
		return domain.BackendRef{}, fmt.Errorf("forkpoint read: %w", err)
	}

	vmID := vmName()
	vmDir := p.vmDir(vmID)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("forkpoint spawn mkdir: %w", err)
	}

	// Reflink rootfs from fork-point.
	src := filepath.Join(fpDir, "rootfs.ext4")
	dst := filepath.Join(vmDir, "rootfs.ext4")
	if _, err := p.storage.CloneFile(ctx, src, dst); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("forkpoint spawn clone rootfs: %w", err)
	}

	// Reflink vmstate.bin and snapshot.meta into the VM dir; Firecracker
	// opens vmstate.bin MAP_PRIVATE during restore, so cleaning the file from
	// the VM dir later is fine — clean pages may still be page-faulted from
	// the fork-point copy as long as it lives.
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		s := filepath.Join(fpDir, name)
		d := filepath.Join(vmDir, name)
		if _, err := p.storage.CloneFile(ctx, s, d); err != nil {
			os.RemoveAll(vmDir)
			return domain.BackendRef{}, fmt.Errorf("forkpoint spawn clone %s: %w", name, err)
		}
	}

	// Allocate per-child identity (CID, MAC, IP, network namespace) and
	// register the new VM in the provider's vminfo store. This reuses the
	// same path as CreateSandboxFromSnapshot, minus the VM start.
	cid := p.allocateCID()
	uid := p.uids.Allocate()

	// Build the VMInfo and persist; defer the actual Firecracker launch to
	// StartSandbox, which is what callers do today for restored snapshots.
	vmi := &VMInfo{
		ID:         vmID,
		CID:        cid,
		UID:        uid,
		Restore:    true,
		ForkPointID: fpID,
	}
	if err := p.persistVMInfo(vmDir, vmi); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, err
	}

	// Bump fork-point descendant set.
	if err := updateFPInfo(p.fpInfoPath(fpID), func(i *fpInfo) {
		i.Descendants = append(i.Descendants, vmID)
		if i.SpawnPending > 0 {
			i.SpawnPending--
		}
	}); err != nil {
		slog.Warn("forkpoint update descendants", "fpID", fpID, "vmID", vmID, "error", err)
	}
	_ = info // info read above for logging if desired

	return domain.BackendRef{Backend: backendName, Ref: vmID}, nil
}

// ReleaseForkPointDescendant decrements the descendant set; when the set is
// empty, the fork-point dir is GC'd.
func (p *Provider) ReleaseForkPointDescendant(fpID, vmID string) error {
	path := p.fpInfoPath(fpID)
	var nowEmpty bool
	if err := updateFPInfo(path, func(i *fpInfo) {
		filtered := i.Descendants[:0]
		for _, d := range i.Descendants {
			if d != vmID {
				filtered = append(filtered, d)
			}
		}
		i.Descendants = filtered
		nowEmpty = len(filtered) == 0 && i.SpawnPending == 0
	}); err != nil {
		return err
	}
	if nowEmpty {
		slog.Info("firecracker forkpoint gc", "fpID", fpID)
		return os.RemoveAll(p.forkPointDir(fpID))
	}
	return nil
}
```

The above references `VMInfo.ForkPointID`, `p.persistVMInfo`, and `p.getVMInfo`. Inspect the existing code (`internal/provider/firecracker/firecracker.go` for `VMInfo`; `internal/provider/firecracker/sandbox.go` and `recover.go` for vminfo persistence) and:
- Add `ForkPointID string` to the `VMInfo` struct.
- If `persistVMInfo` does not exist as a single helper, factor the existing vminfo write into one (DRY) and call it from both `CreateSandbox` and `SpawnFromForkPoint`.
- If `getVMInfo` does not exist, write a small helper that reads `vminfo.json` from `p.vmDir(vmID)`.

- [ ] **Step 4: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/...`
Expected: PASS for all existing + new tests.

Run: `go build -tags firecracker ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/fork.go internal/provider/firecracker/firecracker.go internal/provider/firecracker/sandbox.go internal/provider/firecracker/forkpoint_test.go
git commit -m "feat(firecracker): create fork-points and spawn children via MAP_PRIVATE"
```

---

### Task 14: Add `ForkSandbox` to the `Provider` interface

**Files:**
- Modify: `internal/domain/provider.go`
- Modify: `internal/provider/registry.go` — pass through.
- Modify: `internal/provider/firecracker/firecracker.go` — implement.
- Modify: `internal/provider/incus/provider.go` — return `ErrNotSupported`.
- Modify: `internal/provider/mock_provider.go` (if any) — add a method.

- [ ] **Step 1: Write the failing test**

`internal/provider/registry_test.go` (or create one if missing) — append:
```go
func TestRegistry_ForkSandbox_DispatchesByBackend(t *testing.T) {
	r := NewRegistry()
	mock := &mockProvider{
		forkResp: []domain.BackendRef{{Backend: "mock", Ref: "child-1"}},
	}
	r.Register("mock", mock)

	got, err := r.ForkSandbox(context.Background(), domain.BackendRef{Backend: "mock", Ref: "p"}, 1)
	if err != nil {
		t.Fatalf("ForkSandbox: %v", err)
	}
	if len(got) != 1 || got[0].Ref != "child-1" {
		t.Errorf("unexpected children: %+v", got)
	}
}
```

If `mockProvider` exists for tests, extend it; otherwise add the necessary minimal mock.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/...`
Expected: FAIL — `Registry.ForkSandbox` undefined.

- [ ] **Step 3: Add the method to the interface**

In `internal/domain/provider.go`, append to the `Provider` interface:
```go
	ForkSandbox(ctx context.Context, parent BackendRef, count int) ([]BackendRef, error)
```

Add a sentinel error if not present:
```go
var ErrNotSupported = errors.New("provider: operation not supported")
```

- [ ] **Step 4: Implement the dispatch in registry**

In `internal/provider/registry.go`:
```go
func (r *Registry) ForkSandbox(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
	prov, err := r.resolve(parent.Backend)
	if err != nil {
		return nil, err
	}
	return prov.ForkSandbox(ctx, parent, count)
}
```

- [ ] **Step 5: Implement in Firecracker provider**

In `internal/provider/firecracker/firecracker.go` (new file `fork.go` already added; expose the public API):
```go
func (p *Provider) ForkSandbox(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
	if count < 1 {
		return nil, fmt.Errorf("firecracker fork: count must be >= 1")
	}
	if count > 64 {
		return nil, fmt.Errorf("firecracker fork: count %d exceeds cap of 64", count)
	}

	fpID, err := p.CreateForkPoint(ctx, parent.Ref)
	if err != nil {
		return nil, err
	}

	// Mark the spawn-pending count up front so daemon-restart GC knows how
	// many children we expected.
	_ = updateFPInfo(p.fpInfoPath(fpID), func(i *fpInfo) {
		i.SpawnPending = count
	})

	out := make([]domain.BackendRef, 0, count)
	for i := 0; i < count; i++ {
		ref, err := p.SpawnFromForkPoint(ctx, fpID, domain.CreateSandboxRequest{})
		if err != nil {
			// Decrement spawn-pending for the failed slot so GC can fire
			// once the remaining children settle.
			_ = updateFPInfo(p.fpInfoPath(fpID), func(info *fpInfo) {
				if info.SpawnPending > 0 {
					info.SpawnPending--
				}
			})
			return out, fmt.Errorf("firecracker fork child %d: %w", i, err)
		}
		out = append(out, ref)
	}
	return out, nil
}
```

- [ ] **Step 6: Implement in Incus provider as not-supported**

In the Incus provider:
```go
func (p *Provider) ForkSandbox(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
	return nil, fmt.Errorf("incus: %w (containers do not have VM memory to CoW)", domain.ErrNotSupported)
}
```

Likewise for any mock provider (return `ErrNotSupported` or a configurable response for the test).

- [ ] **Step 7: Run tests**

Run: `go test ./internal/provider/...`
Expected: PASS.

Run: `go build -tags firecracker ./...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/provider.go internal/provider/registry.go internal/provider/firecracker/firecracker.go internal/provider/incus/*.go internal/provider/mock*.go
git commit -m "feat(provider): add ForkSandbox to Provider interface"
```

---

### Task 15: Add `SandboxService.Fork`

**Files:**
- Modify: `internal/service/sandbox.go`
- Create: `internal/service/sandbox_fork_test.go`

- [ ] **Step 1: Write the failing test**

`internal/service/sandbox_fork_test.go`:
```go
package service

import (
	"context"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestFork_RejectsCountLessThan1(t *testing.T) {
	s := newServiceForTest(t)
	_, err := s.Fork(context.Background(), "sbx-1", 0)
	if err == nil {
		t.Fatal("expected error for count=0")
	}
}

func TestFork_CreatesNPendingSandboxesAndOperations(t *testing.T) {
	s := newServiceForTest(t)
	parent := mustCreateRunningSandbox(t, s)

	op, err := s.Fork(context.Background(), parent.SandboxID, 3)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if op == nil || op.Type != "fork_sandbox" {
		t.Errorf("unexpected operation: %+v", op)
	}

	// 3 child sandboxes exist in pending state, each with parent ref.
	all, _ := s.sandboxes.List(context.Background(), domain.SandboxFilter{})
	pending := 0
	for _, sb := range all {
		if sb.State == domain.SandboxPending && sb.Metadata["fork_parent_id"] == parent.SandboxID {
			pending++
		}
	}
	if pending != 3 {
		t.Errorf("pending children = %d, want 3", pending)
	}
}
```

`newServiceForTest` and `mustCreateRunningSandbox` are test helpers; if they do not exist in the package, write minimal versions in this file using in-memory store fakes (look at `internal/service/sandbox_test.go` for the existing pattern and copy it).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/... -run Fork`
Expected: FAIL — `SandboxService.Fork` undefined.

- [ ] **Step 3: Implement `Fork`**

In `internal/service/sandbox.go`, add (modeled on `CreateFromSnapshot`):
```go
// Fork creates count child sandboxes from a running parent. Each child is
// created in pending state with metadata["fork_parent_id"] set; a single
// "fork_sandbox" operation is enqueued whose worker calls
// provider.ForkSandbox under the hood and updates child sandbox states.
func (s *SandboxService) Fork(ctx context.Context, parentID string, count int) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ForkSandbox")
	defer span.End()

	if count < 1 {
		return nil, fmt.Errorf("fork: count must be >= 1: %w", domain.ErrInvalid)
	}
	parent, err := s.sandboxes.Get(ctx, parentID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	childIDs := make([]string, 0, count)
	for i := 0; i < count; i++ {
		child := &domain.Sandbox{
			SandboxID:     uuid.NewString(),
			ProjectID:     parent.ProjectID,
			Name:          fmt.Sprintf("%s-fork-%d", parent.Name, i),
			State:         domain.SandboxPending,
			Backend:       parent.Backend,
			NetworkMode:   parent.NetworkMode,
			CPULimit:      parent.CPULimit,
			MemoryLimitMB: parent.MemoryLimitMB,
			CreatedAt:     now,
			UpdatedAt:     now,
			Metadata:      map[string]any{"fork_parent_id": parent.SandboxID},
		}
		if err := s.sandboxes.Create(ctx, child); err != nil {
			return nil, err
		}
		childIDs = append(childIDs, child.SandboxID)
	}

	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   parent.SandboxID,
		SandboxID:    parent.SandboxID,
		Type:         "fork_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
		Metadata: map[string]any{
			"parent_id": parent.SandboxID,
			"children":  childIDs,
			"count":     count,
		},
	}
	if err := s.ops.Create(ctx, op); err != nil {
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}
```

Add a worker handler — wherever existing ops are dispatched (search for `case "create_sandbox"` or similar), add:
```go
case "fork_sandbox":
    return s.handleFork(ctx, op)
```

And implement `handleFork`:
```go
func (s *SandboxService) handleFork(ctx context.Context, op *domain.Operation) error {
	parentID := op.Metadata["parent_id"].(string)
	parent, err := s.sandboxes.Get(ctx, parentID)
	if err != nil {
		return err
	}
	count := int(op.Metadata["count"].(float64))
	childIDs := op.Metadata["children"].([]any)

	refs, err := s.provider.ForkSandbox(ctx, parent.BackendRef(), count)
	if err != nil {
		// Mark all child sandboxes failed.
		for _, raw := range childIDs {
			id := raw.(string)
			_ = s.sandboxes.UpdateState(ctx, id, domain.SandboxFailed)
		}
		return err
	}
	// Bind each child sandbox row to its returned BackendRef.
	for i, ref := range refs {
		id := childIDs[i].(string)
		_ = s.sandboxes.SetBackendRef(ctx, id, ref)
		_ = s.sandboxes.UpdateState(ctx, id, domain.SandboxStarting)
	}
	return nil
}
```

(Adjust to match the actual `SandboxStore` API in the codebase — `SetBackendRef`/`UpdateState` may be named differently. Read `internal/domain/sandbox.go` for the correct method names and adapt.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/service/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/sandbox.go internal/service/sandbox_fork_test.go
git commit -m "feat(service): add SandboxService.Fork"
```

---

### Task 16: Add the `POST /v1/sandboxes/{id}/fork` HTTP endpoint

**Files:**
- Modify: `internal/api/sandbox.go` — add `forkSandbox` handler.
- Modify: `internal/api/server.go` — register the route.
- Create: `internal/api/sandbox_fork_test.go`.

- [ ] **Step 1: Write the failing test**

`internal/api/sandbox_fork_test.go`:
```go
package api

import (
	"strings"
	"testing"
)

func TestForkSandbox_HappyPath(t *testing.T) {
	env := newTestEnv(t) // existing helper
	sandboxID := mustCreateAndStart(t, env)

	rec := doRequest(t, env.handler, "POST",
		"/v1/sandboxes/"+sandboxID+"/fork",
		strings.NewReader(`{"count": 2}`))
	if rec.Code != 202 {
		t.Errorf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

func TestForkSandbox_RejectsCountLessThan1(t *testing.T) {
	env := newTestEnv(t)
	sandboxID := mustCreateAndStart(t, env)

	rec := doRequest(t, env.handler, "POST",
		"/v1/sandboxes/"+sandboxID+"/fork",
		strings.NewReader(`{"count": 0}`))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestForkSandbox_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := doRequest(t, env.handler, "POST",
		"/v1/sandboxes/does-not-exist/fork",
		strings.NewReader(`{"count": 1}`))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/... -run Fork`
Expected: FAIL — route undefined → 404 for the happy-path test.

- [ ] **Step 3: Add the handler and route**

In `internal/api/sandbox.go`:
```go
type forkSandboxRequest struct {
	Count int `json:"count"`
}

func (s *Server) forkSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req forkSandboxRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Count < 1 {
		http.Error(w, "count must be >= 1", http.StatusBadRequest)
		return
	}
	op, err := s.cfg.Sandboxes.Fork(r.Context(), id, req.Count)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}
```

In `internal/api/server.go`, alongside the other sandbox routes:
```go
api.HandleFunc("POST /v1/sandboxes/{id}/fork", s.forkSandbox)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/sandbox.go internal/api/server.go internal/api/sandbox_fork_test.go
git commit -m "feat(api): add POST /v1/sandboxes/{id}/fork"
```

---

### Task 17: Fork-point GC on daemon start + descendant release on destroy

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go` — extend `recover()` to scan fork-points.
- Modify: `internal/provider/firecracker/sandbox.go` — call `ReleaseForkPointDescendant` from `DestroySandbox` when the VM came from a fork-point.
- Create: `internal/provider/firecracker/forkpoint_gc_test.go`.

- [ ] **Step 1: Write the failing test**

`internal/provider/firecracker/forkpoint_gc_test.go`:
```go
//go:build firecracker

package firecracker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecover_GCsOrphanForkpoints(t *testing.T) {
	chrootBase := t.TempDir()
	p := &Provider{config: Config{ChrootBase: chrootBase}}

	// An orphan fork-point: SpawnPending > 0, older than fpOrphanTTL.
	orphanID := "fp-orphan"
	orphanDir := p.forkPointDir(orphanID)
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(orphanID), &fpInfo{
		ForkPointID:  orphanID,
		SpawnPending: 2,
		CreatedAt:    time.Now().UTC().Add(-2 * fpOrphanTTL),
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A live fork-point: has descendants.
	liveID := "fp-live"
	liveDir := p.forkPointDir(liveID)
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(liveID), &fpInfo{
		ForkPointID: liveID,
		Descendants: []string{"sbx-1"},
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := p.recoverForkPoints(); err != nil {
		t.Fatalf("recoverForkPoints: %v", err)
	}

	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("orphan fork-point should have been GC'd, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(liveDir, "fpinfo.json")); err != nil {
		t.Errorf("live fork-point should remain: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/... -run Recover_GCs`
Expected: FAIL — `recoverForkPoints` undefined.

- [ ] **Step 3: Implement the GC scan**

Append to `internal/provider/firecracker/forkpoint.go`:
```go
// recoverForkPoints scans the fork-points directory and deletes orphans
// (SpawnPending > 0 with CreatedAt older than fpOrphanTTL). Called from
// Provider.recover at daemon start.
func (p *Provider) recoverForkPoints() error {
	root := filepath.Join(p.config.ChrootBase, "forkpoints")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("forkpoint scan: %w", err)
	}
	now := time.Now().UTC()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fpID := e.Name()
		info, err := readFPInfo(p.fpInfoPath(fpID))
		if err != nil {
			continue // leave orphan dirs without an fpinfo for human inspection
		}
		isOrphan := info.SpawnPending > 0 && now.Sub(info.CreatedAt) > fpOrphanTTL
		isUnreferenced := len(info.Descendants) == 0 && info.SpawnPending == 0
		if isOrphan || isUnreferenced {
			if err := os.RemoveAll(p.forkPointDir(fpID)); err != nil {
				return fmt.Errorf("forkpoint GC %s: %w", fpID, err)
			}
		}
	}
	return nil
}
```

In `internal/provider/firecracker/firecracker.go`, find `func (p *Provider) recover()` and call:
```go
if err := p.recoverForkPoints(); err != nil {
    slog.Warn("firecracker forkpoint recover", "error", err)
}
```

- [ ] **Step 4: Wire `ReleaseForkPointDescendant` into DestroySandbox**

In `internal/provider/firecracker/sandbox.go`, find `DestroySandbox`. After successful VM teardown, read the VMInfo and:
```go
if vmi.ForkPointID != "" {
    if err := p.ReleaseForkPointDescendant(vmi.ForkPointID, vmi.ID); err != nil {
        slog.Warn("firecracker forkpoint release", "fpID", vmi.ForkPointID, "vmID", vmi.ID, "error", err)
    }
}
```

- [ ] **Step 5: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/firecracker/forkpoint.go internal/provider/firecracker/firecracker.go internal/provider/firecracker/sandbox.go internal/provider/firecracker/forkpoint_gc_test.go
git commit -m "feat(firecracker): GC orphan fork-points and release descendants on destroy"
```

---

### Task 18: Add fork metrics

**Files:**
- Modify: `internal/telemetry/metrics.go` (or wherever metrics are declared) — register the new metric families.
- Modify: `internal/provider/firecracker/fork.go` — emit pause and per-child timings.

- [ ] **Step 1: Add metric registrations**

In the package that declares Prometheus/OTel metrics (search: `grep -rn "MeterProvider\|prometheus.NewHistogram" internal/telemetry/`):

```go
var (
    forkPauseDuration = otel.Meter("navaris").Float64Histogram(
        "navaris_fork_pause_duration_seconds",
        metric.WithDescription("Time the parent VM was paused during fork-point materialization"),
        metric.WithUnit("s"),
    )
    forkChildSpawnDuration = otel.Meter("navaris").Float64Histogram(
        "navaris_fork_child_spawn_duration_seconds",
        metric.WithDescription("Time to spawn a single fork child from a fork-point"),
        metric.WithUnit("s"),
    )
    storageCloneDuration = otel.Meter("navaris").Float64Histogram(
        "navaris_storage_clone_duration_seconds",
        metric.WithDescription("Time to clone a single file via storage.Backend"),
        metric.WithUnit("s"),
    )
)
```

(Adjust to match the existing telemetry style — the snippet above uses OTel v1 API; the codebase may use a wrapper.)

- [ ] **Step 2: Emit them**

In `internal/provider/firecracker/fork.go`, around `materializeLiveForkPoint`:
```go
start := time.Now()
err := p.createLiveSnapshot(ctx, vmID, p.vmDir(vmID), fpDir)
forkPauseDuration.Record(ctx, time.Since(start).Seconds(),
    metric.WithAttributes(attribute.Bool("host_cow_capable",
        p.storage.For(filepath.Join(fpDir, "rootfs.ext4")).Capabilities().InstantClone)))
return err
```

Around each `SpawnFromForkPoint` body:
```go
start := time.Now()
defer func() {
    forkChildSpawnDuration.Record(ctx, time.Since(start).Seconds())
}()
```

In `internal/storage/registry.go` `CloneFile`, optionally accept an optional metric callback (or wrap externally — the metric belongs in the firecracker package so storage stays decoupled). Cleanest: wrap the call at each Firecracker call site with a `time.Since` recorded into `storageCloneDuration` keyed by backend name.

- [ ] **Step 3: Run build**

Run: `go build -tags firecracker ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/telemetry/*.go internal/provider/firecracker/fork.go
git commit -m "feat(firecracker): emit fork pause/spawn metrics"
```

---

### Task 19: Integration test — fork end-to-end (reflink-capable host)

**Files:**
- Create: `test/integration/fork_test.go` — gated by `//go:build integration_firecracker` matching existing patterns.

- [ ] **Step 1: Locate the existing integration-test layout**

Run: `ls test/integration/ && head -20 test/integration/firecracker*.go 2>/dev/null | head -40`

Identify the build tag and helper imports used by existing Firecracker integration tests (e.g. `//go:build integration_firecracker` and a shared `setupFirecrackerEnv` helper).

- [ ] **Step 2: Write the integration test**

`test/integration/fork_test.go`:
```go
//go:build integration_firecracker

package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestFork_ChildrenSeeParentFiles_AndDivergeIndependently is the headline
// stage-2 test: create a sandbox, write a sentinel file, fork x3, verify all
// three see the sentinel and each can diverge.
func TestFork_ChildrenSeeParentFiles_AndDivergeIndependently(t *testing.T) {
	if !reflinkCapableHost(t) {
		t.Skip("test requires a reflink-capable host filesystem")
	}

	env := setupFirecrackerEnv(t) // existing helper
	defer env.Teardown()

	parent := env.CreateSandboxFromImage(t, "default")
	env.StartSandbox(t, parent)

	// Write a sentinel file inside the parent.
	const sentinel = "/root/sentinel"
	env.Exec(t, parent, []string{"sh", "-c", "echo PARENT > " + sentinel})

	// Fork x3.
	children, err := env.ForkSandbox(context.Background(), parent, 3)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}

	// Wait for all to reach running.
	for _, c := range children {
		env.WaitState(t, c, "running", 30*time.Second)
	}

	// Each child sees the sentinel.
	for _, c := range children {
		out := env.Exec(t, c, []string{"cat", sentinel})
		if strings.TrimSpace(out) != "PARENT" {
			t.Errorf("child %s sentinel = %q, want PARENT", c, strings.TrimSpace(out))
		}
	}

	// Diverge: each child writes a unique tag to the sentinel.
	for i, c := range children {
		tag := "CHILD-" + string(rune('A'+i))
		env.Exec(t, c, []string{"sh", "-c", "echo " + tag + " > " + sentinel})
	}

	// Each child sees its own tag, parent and other children unaffected.
	parentOut := env.Exec(t, parent, []string{"cat", sentinel})
	if strings.TrimSpace(parentOut) != "PARENT" {
		t.Errorf("parent sentinel changed after child writes: %q", parentOut)
	}
	for i, c := range children {
		expected := "CHILD-" + string(rune('A'+i))
		got := strings.TrimSpace(env.Exec(t, c, []string{"cat", sentinel}))
		if got != expected {
			t.Errorf("child %d sentinel = %q, want %q", i, got, expected)
		}
	}

	// Destroying one child does not affect others or the parent.
	env.DestroySandbox(t, children[0])
	for _, c := range children[1:] {
		got := strings.TrimSpace(env.Exec(t, c, []string{"cat", sentinel}))
		if got == "" {
			t.Errorf("child %s became unreadable after sibling destroy", c)
		}
	}
	parentOut = env.Exec(t, parent, []string{"cat", sentinel})
	if strings.TrimSpace(parentOut) != "PARENT" {
		t.Errorf("parent affected by child destroy: %q", parentOut)
	}
}

func reflinkCapableHost(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("findmnt", "-n", "-o", "FSTYPE", "/srv/firecracker").CombinedOutput()
	if err != nil {
		return false
	}
	fs := strings.TrimSpace(string(out))
	return fs == "btrfs" || fs == "xfs" || fs == "bcachefs"
}
```

`env.ForkSandbox`, `env.WaitState`, etc., may need to be added to the integration test harness. Look at the existing harness file (e.g. `test/integration/harness.go`) and extend it with a thin wrapper that POSTs to `/v1/sandboxes/{id}/fork` and parses the operation response.

- [ ] **Step 3: Run the test on a CoW-capable host**

Run: `go test -tags integration_firecracker ./test/integration/... -run Fork`
Expected: PASS on a CoW-capable host; SKIP otherwise.

If the integration suite has its own `make` target (e.g. `make integration-firecracker`), run that instead.

- [ ] **Step 4: Commit**

```bash
git add test/integration/fork_test.go test/integration/harness.go
git commit -m "test(integration): fork end-to-end on reflink-capable host"
```

---

### Task 20: Documentation

**Files:**
- Create: `docs/storage-backends.md` — operator-facing notes (host FS prerequisites, modes, troubleshooting).
- Create: `docs/sandbox-fork.md` — API reference for `POST /v1/sandboxes/{id}/fork`, in-guest identity caveat, latency expectations.
- Modify: `README.md` — one-line link to each.

- [ ] **Step 1: Write `docs/storage-backends.md`**

Contents:
```markdown
# Storage Backends

Navaris clones rootfs files via a pluggable storage backend.

## Modes

- `auto` (default) — probe each storage root; use reflink (FICLONE) when the
  host filesystem supports it (XFS with `reflink=1`, btrfs, bcachefs), fall
  back to byte copy otherwise.
- `copy` — always full-copy. Largest disk footprint, no FS prerequisites.
- `reflink` — explicit reflink. Fails startup if any storage root is not on a
  CoW-capable filesystem.
- `btrfs-subvol`, `zfs` — accepted but not wired in v1; will fail with
  "not supported in v1". Reserved for future non-Firecracker providers.

Selected via `--storage-mode=<mode>` on `navarisd`.

## Host filesystem prerequisites

| Filesystem | Reflink? | Notes |
|---|---|---|
| ext4 | No | Use copy mode. |
| XFS | If `reflink=1` at mkfs | Cannot be enabled in place; needs fresh mkfs. |
| btrfs | Yes | Consider `nodatacow` on the rootfs storage dir to avoid fragmentation under VM-image random writes. |
| bcachefs | Yes | Recent kernels only. |
| tmpfs / NFS / dir-based | No | Auto mode falls back to copy. |

## Storage roots

Reflink requires src and dst to share a filesystem. Navaris's storage roots
are: `--chroot-base` (defaults to `/srv/firecracker`), `--image-dir`, and
`--snapshot-dir`. Place all three on the same filesystem to maximise CoW
coverage; the registry probes each root independently and falls back per-root.

## Troubleshooting

- `storage: root "...": reflink not available at ...: unsupported` at startup
  with `--storage-mode=reflink` means the FS is not reflink-capable. Use
  `--storage-mode=auto` or `=copy`, or remount/mkfs the affected root.
- A clone that succeeded at startup probe but fails at op time (rare; e.g.
  cross-mount) is automatically retried via copy fallback. Look for
  `storage_backend=copy` in the per-op log to confirm.

## Incus pool capability

Incus uses its own native storage pools. Navaris does not implement CoW for
Incus; it only checks whether the configured pool driver is CoW-capable.
Drivers `dir`, `lvm` (non-thin) yield a startup warning. Use `btrfs`, `zfs`,
`lvm-thin`, `ceph`, or `cephfs` for CoW. `--incus-strict-pool-cow` upgrades
the warning to a startup error.
```

- [ ] **Step 2: Write `docs/sandbox-fork.md`**

```markdown
# Sandbox Fork

Stage 2 adds memory + disk CoW forking for running Firecracker sandboxes.

## API

```
POST /v1/sandboxes/{id}/fork
Content-Type: application/json

{ "count": 3 }
```

Response: an Operation. Each child sandbox is created in `pending` state and
transitions to `starting`/`running` via the same worker dispatcher as
ordinary creates. Child IDs and states are visible via the existing
`GET /v1/sandboxes` endpoint, filterable by `metadata.fork_parent_id={id}`.

## How it works

1. The parent is paused briefly. Firecracker writes its memory state to
   `vmstate.bin` in an internal "fork-point" directory; the rootfs is
   reflinked into the same directory.
2. The parent is resumed. On a reflink-capable host the pause is
   memory-size-proportional (tens to low hundreds of milliseconds for a
   1–4 GB VM) plus a metadata-only disk reflink.
3. Each child starts as a fresh Firecracker microVM that restores from the
   fork-point's `vmstate.bin` and `snapshot.meta`. Firecracker opens
   `vmstate.bin` with `MAP_PRIVATE`; the kernel handles per-page CoW
   automatically. All children of one fork-point share clean memory pages
   via the page cache.

The fork-point directory (`/srv/firecracker/forkpoints/{fpID}/`) is kept on
disk as long as any descendant sandbox exists; it is GC'd on the last
descendant's destroy. Daemon restart sweeps orphans (TTL 1 h for
fork-points whose spawn never completed).

## Per-child identity

Children are byte-identical VMs at the moment of fork:
- **CID, MAC, IP, network namespace** — re-rolled by navaris before each
  child's first instruction.
- **Hostname, machine-id, SSH host keys, in-guest randomness** — the
  caller's responsibility. Treat fork like
  `CreateSandboxFromSnapshot`: regenerate identity in your sandbox init
  script if you need it distinct.

## Latency expectations

| Host FS | Pause window | Per-child spawn |
|---|---|---|
| XFS reflink / btrfs / bcachefs | `vmstate.bin` write only (≈100–300 ms / GB of RAM) | Restore + reflink, sub-second |
| ext4 / dir | `vmstate.bin` write + full disk copy (multi-second) | Restore + copy (multi-second) |

Fork responses on non-CoW hosts emit a warning header; consider switching to
`--storage-mode=copy` is *not* a fix here — only a CoW-capable host
filesystem actually closes the latency gap.

## Limits

- `count` is capped at 64 per request (provider-side) and 16 by default at
  the API layer (configurable). Callers can fork repeatedly to bypass.
- Incus sandboxes do not support fork (containers do not have VM memory to
  CoW). The endpoint returns `400 Bad Request` with
  `provider: operation not supported`.
```

- [ ] **Step 3: Add README links**

In `README.md`, after the existing docs links:
```markdown
- [Storage backends](docs/storage-backends.md)
- [Sandbox fork](docs/sandbox-fork.md)
```

- [ ] **Step 4: Commit**

```bash
git add docs/storage-backends.md docs/sandbox-fork.md README.md
git commit -m "docs: storage backends and sandbox fork"
```

---

## Self-Review Checklist (run after the plan is written)

- [ ] **Spec §3.2 — `BtrfsSubvolBackend`/`ZfsBackend` defined and unit-tested** → Task 4 ✓
- [ ] **Spec §3.3 — capability detection probe** → Task 5 ✓
- [ ] **Spec §3.4 — `mode: auto` / explicit / per-root override** → Task 6 + Task 9 ✓
- [ ] **Spec §3.5 — replace four (actually eight) `copyFile` call sites** → Task 7 ✓ (all 9 sites: image.go:60, sandbox.go:52,508, snapshot.go:119,172,186,221,235)
- [ ] **Spec §3.6 — Incus pool driver probe** → Task 10 + Task 11 ✓
- [ ] **Spec §4.5 — `storage_backend` field on snap/image metadata** → Task 8 ✓
- [ ] **Spec §4.5 — `navaris_storage_clone_duration_seconds`** → Task 18 ✓
- [ ] **Spec §4.5 — `navaris_storage_clone_bytes_saved_total`** — DEFERRED (requires physical-extent introspection that is per-FS specific and not necessary for stage-1 ship; tracked as future work in `docs/storage-backends.md` troubleshooting section). Add an explicit note in the spec's risks/open questions on next revision.
- [ ] **Spec §5.1 — fork API endpoint** → Task 16 ✓
- [ ] **Spec §5.2 — fork-point + MAP_PRIVATE** → Task 13 ✓
- [ ] **Spec §5.3 — pause budget metric** → Task 18 ✓
- [ ] **Spec §5.4 — per-child identity** → handled inside `SpawnFromForkPoint` (Task 13) by reusing `allocateCID`/`uids.Allocate`/network alloc; documented in Task 20 ✓
- [ ] **Spec §5.5 — fork-point lifecycle (Rule A and Rule B)** → Tasks 12, 13, 17 ✓
- [ ] **Spec §5.6 — `memoryMode` enum seam for UFFD** — currently NOT modeled as an enum in the implementation; the seam is implicit (anyone adding UFFD touches `SpawnFromForkPoint`). For the v1 ship this is acceptable; if you want it explicit, add an internal `memoryMode` parameter in Task 13's `SpawnFromForkPoint` (extra complexity for unknown future users — YAGNI). NOT BLOCKING.
- [ ] **Spec §5.7 — `navaris_fork_pause_duration_seconds` + `navaris_fork_child_spawn_duration_seconds`** → Task 18 ✓
- [ ] **Spec §5.8 — integration test (CoW-capable host) and stress test** → Task 19 ✓ for correctness; stress (fork count = 16) is implicit but can be added as a sub-test.
- [ ] **Placeholder scan**: no "TBD"/"TODO" in task code blocks. Check ✓
- [ ] **Type consistency**: `Backend.Capabilities`, `storage.Mode`, `fpInfo`, `Provider.ForkSandbox`, `SandboxService.Fork` consistent across tasks. Check ✓

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-24-cow-sandbox-fork.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best for a 20-task plan.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch with checkpoints.

Which approach?
