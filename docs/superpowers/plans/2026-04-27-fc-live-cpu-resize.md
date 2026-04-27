# Firecracker Live CPU Resize Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `PATCH /v1/sandboxes/{id}/resources` accept `cpu_limit` on running Firecracker sandboxes by enforcing the limit through Linux cgroup CPU bandwidth (`cpu.max` v2 / `cfs_quota_us` v1).

**Architecture:** Per-VM cgroup created at sandbox start (jailer mode: via `JailerCfg.CgroupArgs`; non-jailer mode: navarisd creates `<CgroupRoot>/<vmID>/` and moves the FC PID into it). Live resize writes the new quota to the cgroup file. The boot vCPU count stays at `CeilingCPU`; the cgroup throttles to `LimitCPU * 100ms`.

**Tech Stack:** Go (`os` package for cgroup file operations), Firecracker Go SDK v1.0.0 (`JailerCfg.CgroupArgs`).

**Spec:** [docs/superpowers/specs/2026-04-27-firecracker-live-cpu-resize-design.md](../specs/2026-04-27-firecracker-live-cpu-resize-design.md)

---

## File Plan

### Created
- `internal/provider/firecracker/cgroup.go` — `cgroupCPUDir(vmID) string`, `setupCgroup(pid, vmID, limitCPU) error`, `writeCPUMax(dir, quota, period int64) error`, `removeCgroup(vmID) error`. All methods on `*Provider`. Build-tagged `firecracker`.
- `internal/provider/firecracker/cgroup_test.go` — unit tests using `t.TempDir()` as fake cgroup root.
- `test/integration/limits_cpu_test.go` — `TestSandbox_HonorsRequestedCPULimit` (FC only) reading `/sys/fs/cgroup/cpu.max` from inside the guest.

### Modified
- `internal/domain/provider.go` — add `ResizeReasonCgroupUnavailable` and `ResizeReasonCgroupWriteFailed` constants.
- `internal/api/response.go` — `mapErrorCode` branches on `ProviderResizeError.Reason` for the new codes (503, 500).
- `internal/api/sandbox_resize_test.go` — assert the new mappings for new reasons.
- `internal/provider/firecracker/vminfo.go` — add `CgroupActive bool` JSON field.
- `internal/provider/firecracker/firecracker.go` — add `Config.CgroupRoot` (default `/sys/fs/cgroup/navaris-fc`); validate / default in `Config.defaults()`.
- `internal/provider/firecracker/sandbox.go` — boot-time cgroup setup after `machine.Start()` in both `StartSandbox` and `startFromSnapshot`; jailer `CgroupArgs` plumbing; cleanup in destroy path.
- `internal/provider/firecracker/sandbox_resize.go` — replace CPU rejection with cgroup write; add ceiling check.
- `internal/provider/firecracker/sandbox_resize_test.go` — flip the existing CPU-rejected test, add ceiling-rejection + cgroup-unavailable cases.
- `cmd/navarisd/provider_firecracker.go` — pass `CgroupRoot: cfg.firecrackerCgroupRoot` into `firecracker.Config{}`.
- `cmd/navarisd/main.go` — `--firecracker-cgroup-root` flag.
- `test/integration/boost_e2e_test.go` — new `TestBoost_E2E_FC_CPU_AppliesToGuest`.
- `README.md` — boost/limits feature lines updated.
- `docs/superpowers/specs/2026-04-25-runtime-resource-resize-design.md` — one-line cross-reference at §3.5.

---

## Conventions

- All work on a fresh feature branch off `main` (use `superpowers:using-git-worktrees`).
- Each task ends with a commit. Match existing commit prefixes (`feat:`, `feat(...)`, `test(...)`, `refactor(...)`, `fix(...)`, `chore(...)`, `docs:`).
- Build tags: Firecracker code is gated by `//go:build firecracker`. Domain, service, API, store, navarisd code: untagged.
- After every implementation task: `gofmt -l <files>` clean, all four builds green (`./...`, `-tags firecracker ./...`, `-tags incus ./...`, `-tags 'incus firecracker' ./...`).

---

## Task 1: Add domain reason constants

**Files:**
- Modify: `internal/domain/provider.go`

- [ ] **Step 1: Add the two new constants**

In `internal/domain/provider.go`, in the existing `const ( ... ResizeReason... )` block (around line 71), add two new entries:

```go
const (
	ResizeReasonExceedsCeiling          = "exceeds_ceiling"
	ResizeReasonCPUUnsupportedByBackend = "cpu_resize_unsupported_by_backend"
	ResizeReasonBackendRejected         = "backend_rejected"
	ResizeReasonCgroupUnavailable       = "cgroup_unavailable"
	ResizeReasonCgroupWriteFailed       = "cgroup_write_failed"
)
```

- [ ] **Step 2: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 3: gofmt and commit**

```bash
gofmt -l internal/domain/provider.go
git add internal/domain/provider.go
git commit -m "feat(domain): add cgroup_unavailable + cgroup_write_failed resize reasons"
```

---

## Task 2: Wire status code branching for new reasons

**Files:**
- Modify: `internal/api/response.go`
- Modify: `internal/api/sandbox_resize_test.go`

- [ ] **Step 1: Inspect the existing `mapErrorCode`**

Read `internal/api/response.go` lines 60-92 to confirm the current shape:

```go
var prErr *domain.ProviderResizeError
if errors.As(err, &prErr) {
    return http.StatusConflict
}
```

- [ ] **Step 2: Add a failing test for the new mappings**

Append to `internal/api/sandbox_resize_test.go`:

```go
func TestSandboxResize_CgroupUnavailable_Returns503(t *testing.T) {
	env := newSandboxResizeTestEnv(t)
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, _ domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonCgroupUnavailable,
			Detail: "boot-time cgroup setup failed",
		}
	}
	sbx := env.seedRunningSandbox(t, "rc-cgroup-503")
	body := `{"cpu_limit": 2}`
	resp := env.patch(t, sbx.SandboxID, body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSandboxResize_CgroupWriteFailed_Returns500(t *testing.T) {
	env := newSandboxResizeTestEnv(t)
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, _ domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonCgroupWriteFailed,
			Detail: "EIO",
		}
	}
	sbx := env.seedRunningSandbox(t, "rc-cgroup-500")
	body := `{"cpu_limit": 2}`
	resp := env.patch(t, sbx.SandboxID, body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}
```

> **Helpers `newSandboxResizeTestEnv`, `seedRunningSandbox`, `patch`** are assumed to exist from spec #2's resize tests. If their names differ in this codebase, read `internal/api/sandbox_resize_test.go` for the existing pattern and adapt.

- [ ] **Step 3: Run the tests; expect failure**

```bash
go test ./internal/api/ -run "TestSandboxResize_Cgroup" -v
```

Expected: both fail with status 409 instead of 503/500.

- [ ] **Step 4: Update `mapErrorCode`**

In `internal/api/response.go`, replace the existing `ProviderResizeError` branch with a switch on `Reason`:

```go
var prErr *domain.ProviderResizeError
if errors.As(err, &prErr) {
	switch prErr.Reason {
	case domain.ResizeReasonCgroupUnavailable:
		return http.StatusServiceUnavailable
	case domain.ResizeReasonCgroupWriteFailed:
		return http.StatusInternalServerError
	default:
		return http.StatusConflict
	}
}
```

- [ ] **Step 5: Run tests; expect pass**

```bash
go test ./internal/api/
```

All green.

- [ ] **Step 6: gofmt and commit**

```bash
gofmt -l internal/api/response.go internal/api/sandbox_resize_test.go
git add internal/api/response.go internal/api/sandbox_resize_test.go
git commit -m "feat(api): map cgroup_unavailable to 503 and cgroup_write_failed to 500"
```

---

## Task 3: Add `CgroupActive` field to VMInfo

**Files:**
- Modify: `internal/provider/firecracker/vminfo.go`

- [ ] **Step 1: Add the field**

In `internal/provider/firecracker/vminfo.go`, in the `VMInfo` struct (after `CeilingMemMib`), add:

```go
	// CgroupActive is true if the boot-time cgroup setup succeeded for this VM.
	// When false, live CPU resize returns ResizeReasonCgroupUnavailable.
	// Pre-spec sandboxes (vminfo.json without this field) deserialize as false;
	// they continue to function but cannot live-resize CPU until restarted.
	CgroupActive bool `json:"cgroup_active,omitempty"`
```

- [ ] **Step 2: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 3: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/vminfo.go
git add internal/provider/firecracker/vminfo.go
git commit -m "feat(firecracker): VMInfo.CgroupActive field for live CPU resize gating"
```

---

## Task 4: Add `Config.CgroupRoot` field + daemon flag

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go`
- Modify: `cmd/navarisd/main.go`
- Modify: `cmd/navarisd/provider_firecracker.go`

- [ ] **Step 1: Add `CgroupRoot` to `firecracker.Config`**

In `internal/provider/firecracker/firecracker.go`, in the `Config` struct (next to `ChrootBase`), add:

```go
	// CgroupRoot is the host directory under which the daemon creates a
	// per-VM cgroup for non-jailer sandboxes (e.g. "/sys/fs/cgroup/navaris-fc").
	// Unused when EnableJailer is true. Empty defaults to "/sys/fs/cgroup/navaris-fc".
	CgroupRoot string
```

- [ ] **Step 2: Default `CgroupRoot` in `Config.defaults()`**

In the same file, in `Config.defaults()` (around line 60-70), append:

```go
	if c.CgroupRoot == "" {
		c.CgroupRoot = "/sys/fs/cgroup/navaris-fc"
	}
```

- [ ] **Step 3: Add the daemon flag**

In `cmd/navarisd/main.go`, in the `config` struct, near other firecracker-related fields, add:

```go
	firecrackerCgroupRoot string
```

In `parseFlags`, near other firecracker flags:

```go
	flag.StringVar(&cfg.firecrackerCgroupRoot, "firecracker-cgroup-root", "/sys/fs/cgroup/navaris-fc",
		"host directory for per-VM cgroups in non-jailer mode (cgroup CPU bandwidth limits live here)")
```

- [ ] **Step 4: Pass through to the FC provider constructor**

In `cmd/navarisd/provider_firecracker.go`, in the `firecracker.Config{...}` literal, add the field:

```go
	return firecracker.New(firecracker.Config{
		// ...existing fields...
		CgroupRoot: cfg.firecrackerCgroupRoot,
	})
```

- [ ] **Step 5: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 6: Verify the flag appears in --help**

```bash
go build -tags 'incus firecracker' -o /tmp/navarisd ./cmd/navarisd/
/tmp/navarisd --help 2>&1 | grep firecracker-cgroup-root
```

Expected: one line showing the flag.

- [ ] **Step 7: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/firecracker.go cmd/navarisd/main.go cmd/navarisd/provider_firecracker.go
git add internal/provider/firecracker/firecracker.go cmd/navarisd/main.go cmd/navarisd/provider_firecracker.go
git commit -m "feat(firecracker): --firecracker-cgroup-root flag + Config.CgroupRoot"
```

---

## Task 5: Cgroup helpers — path derivation + write

**Files:**
- Create: `internal/provider/firecracker/cgroup.go`
- Create: `internal/provider/firecracker/cgroup_test.go`

The helpers run only under `//go:build firecracker`. They have no external dependencies beyond the `os` and `path/filepath` packages.

- [ ] **Step 1: Write failing tests for path derivation + writeCPUMax**

Create `internal/provider/firecracker/cgroup_test.go`:

```go
//go:build firecracker

package firecracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCgroupCPUDir_NonJailer_v2(t *testing.T) {
	p := &Provider{
		config:        Config{CgroupRoot: "/sys/fs/cgroup/navaris-fc", EnableJailer: false},
		cgroupVersion: "2",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/navaris-fc/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestCgroupCPUDir_NonJailer_v1(t *testing.T) {
	p := &Provider{
		config:        Config{CgroupRoot: "/sys/fs/cgroup/navaris-fc", EnableJailer: false},
		cgroupVersion: "1",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/cpu/navaris-fc/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestCgroupCPUDir_Jailer_v2(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "2",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/firecracker/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestCgroupCPUDir_Jailer_v1(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "1",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/cpu/firecracker/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestWriteCPUMax_v2(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{cgroupVersion: "2"}
	if err := p.writeCPUMax(dir, 200000, 100000); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "200000 100000" {
		t.Errorf("cpu.max = %q, want %q", string(got), "200000 100000")
	}
}

func TestWriteCPUMax_v1(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{cgroupVersion: "1"}
	if err := p.writeCPUMax(dir, 200000, 100000); err != nil {
		t.Fatal(err)
	}
	quota, err := os.ReadFile(filepath.Join(dir, "cpu.cfs_quota_us"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(quota)) != "200000" {
		t.Errorf("cpu.cfs_quota_us = %q", string(quota))
	}
	period, err := os.ReadFile(filepath.Join(dir, "cpu.cfs_period_us"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(period)) != "100000" {
		t.Errorf("cpu.cfs_period_us = %q", string(period))
	}
}
```

- [ ] **Step 2: Run; expect compile failures**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run "TestCgroupCPUDir|TestWriteCPUMax" -v
```

Expected: FAIL — `cgroupCPUDir`, `writeCPUMax` undefined.

- [ ] **Step 3: Implement `cgroup.go`**

Create `internal/provider/firecracker/cgroup.go`:

```go
//go:build firecracker

package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// cpuPeriod is the CFS scheduling period used for all FC sandboxes.
// 100ms is the kernel default; quota is computed as LimitCPU * cpuPeriod.
const cpuPeriod int64 = 100_000

// cgroupCPUDir returns the absolute filesystem path to the CPU cgroup
// directory for vmID. Caller writes cpu.max (v2) or cpu.cfs_quota_us +
// cpu.cfs_period_us (v1) inside this directory.
func (p *Provider) cgroupCPUDir(vmID string) string {
	if p.config.EnableJailer {
		// Jailer creates firecracker/<vm-id> by default; we follow the same convention.
		if p.cgroupVersion == "1" {
			return filepath.Join("/sys/fs/cgroup/cpu/firecracker", vmID)
		}
		return filepath.Join("/sys/fs/cgroup/firecracker", vmID)
	}
	if p.cgroupVersion == "1" {
		return filepath.Join("/sys/fs/cgroup/cpu", filepath.Base(p.config.CgroupRoot), vmID)
	}
	return filepath.Join(p.config.CgroupRoot, vmID)
}

// writeCPUMax writes the CFS quota+period to the cgroup directory dir,
// branching on cgroupVersion.
func (p *Provider) writeCPUMax(dir string, quota, period int64) error {
	if p.cgroupVersion == "1" {
		if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_quota_us"),
			[]byte(strconv.FormatInt(quota, 10)), 0644); err != nil {
			return fmt.Errorf("write cpu.cfs_quota_us: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_period_us"),
			[]byte(strconv.FormatInt(period, 10)), 0644); err != nil {
			return fmt.Errorf("write cpu.cfs_period_us: %w", err)
		}
		return nil
	}
	// cgroup v2: single cpu.max file with "<quota> <period>".
	line := fmt.Sprintf("%d %d", quota, period)
	if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(line), 0644); err != nil {
		return fmt.Errorf("write cpu.max: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests; expect pass**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run "TestCgroupCPUDir|TestWriteCPUMax" -v
```

All green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/cgroup.go internal/provider/firecracker/cgroup_test.go
git add internal/provider/firecracker/cgroup.go internal/provider/firecracker/cgroup_test.go
git commit -m "feat(firecracker): cgroup path + writeCPUMax helpers"
```

---

## Task 6: Cgroup helpers — `setupCgroup` (non-jailer mode)

**Files:**
- Modify: `internal/provider/firecracker/cgroup.go`
- Modify: `internal/provider/firecracker/cgroup_test.go`

`setupCgroup` is non-jailer-only. Jailer mode delegates the same work to `JailerCfg.CgroupArgs` (Task 7).

- [ ] **Step 1: Write failing test**

Append to `internal/provider/firecracker/cgroup_test.go`:

```go
func TestSetupCgroup_NonJailer_WritesQuota(t *testing.T) {
	root := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion: "2",
	}

	// setupCgroup needs a real PID; use the test process's own PID — writing
	// it to a tempdir cgroup.procs is harmless because the dir isn't a real
	// cgroup mount.
	pid := os.Getpid()
	if err := p.setupCgroup(pid, "vm-test", 2); err != nil {
		t.Fatalf("setupCgroup: %v", err)
	}

	// Verify the directory exists.
	dir := filepath.Join(root, "vm-test")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cgroup dir missing: %v", err)
	}

	// Verify cpu.max content.
	got, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "200000 100000" {
		t.Errorf("cpu.max = %q, want %q", string(got), "200000 100000")
	}

	// Verify cgroup.procs content.
	procs, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(procs)) != strconv.Itoa(pid) {
		t.Errorf("cgroup.procs = %q, want %d", string(procs), pid)
	}
}

func TestSetupCgroup_Jailer_NoOp(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "2",
	}
	// Should be a no-op when jailer is enabled (jailer handles its own cgroup).
	if err := p.setupCgroup(os.Getpid(), "vm-test", 2); err != nil {
		t.Errorf("setupCgroup with jailer should be no-op, got: %v", err)
	}
}
```

Add `"strconv"` to the imports if not already present.

- [ ] **Step 2: Run; expect compile failure**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run TestSetupCgroup -v
```

Expected: FAIL — `setupCgroup` undefined.

- [ ] **Step 3: Implement `setupCgroup` in `cgroup.go`**

Append to `internal/provider/firecracker/cgroup.go`:

```go
// setupCgroup creates a per-VM cgroup, enables the cpu controller (v2),
// places the FC PID into it, and writes the initial CPU bandwidth quota.
// Returns nil and is a no-op in jailer mode (the jailer handles cgroup
// creation and the initial limit via JailerCfg.CgroupArgs).
//
// Idempotent: tolerates "already exists" / "already enabled" errors so a
// second call (or an inherited cgroup from a previous daemon invocation)
// does not fail the sandbox start.
func (p *Provider) setupCgroup(pid int, vmID string, limitCPU int64) error {
	if p.config.EnableJailer {
		return nil // jailer handles cgroup creation
	}

	dir := p.cgroupCPUDir(vmID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir cgroup dir %s: %w", dir, err)
	}

	// On cgroup v2, child cgroups can only use the cpu controller if
	// the parent enables it via subtree_control. Idempotent — ignore
	// "no such device" / "already enabled" errors.
	if p.cgroupVersion == "2" {
		parent := filepath.Dir(dir)
		_ = os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"),
			[]byte("+cpu"), 0644)
	}

	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"),
		[]byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("write cgroup.procs: %w", err)
	}

	quota := limitCPU * cpuPeriod
	if err := p.writeCPUMax(dir, quota, cpuPeriod); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run; expect pass**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run "TestSetupCgroup|TestCgroupCPUDir|TestWriteCPUMax" -v
```

All green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/cgroup.go internal/provider/firecracker/cgroup_test.go
git add internal/provider/firecracker/cgroup.go internal/provider/firecracker/cgroup_test.go
git commit -m "feat(firecracker): setupCgroup creates per-VM cgroup with CPU bandwidth limit"
```

---

## Task 7: Cgroup helpers — `removeCgroup`

**Files:**
- Modify: `internal/provider/firecracker/cgroup.go`
- Modify: `internal/provider/firecracker/cgroup_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/provider/firecracker/cgroup_test.go`:

```go
func TestRemoveCgroup_NonJailer_RemovesDir(t *testing.T) {
	root := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion: "2",
	}
	dir := filepath.Join(root, "vm-rm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := p.removeCgroup("vm-rm"); err != nil {
		t.Fatalf("removeCgroup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir still exists: err=%v", err)
	}
}

func TestRemoveCgroup_Missing_NoError(t *testing.T) {
	root := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion: "2",
	}
	// Removing a non-existent cgroup should be a no-op.
	if err := p.removeCgroup("vm-never-existed"); err != nil {
		t.Errorf("removeCgroup on missing dir should not error, got: %v", err)
	}
}

func TestRemoveCgroup_Jailer_NoOp(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "2",
	}
	if err := p.removeCgroup("vm-jailer"); err != nil {
		t.Errorf("removeCgroup with jailer should be no-op, got: %v", err)
	}
}
```

- [ ] **Step 2: Run; expect compile failure**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run TestRemoveCgroup -v
```

Expected: FAIL — `removeCgroup` undefined.

- [ ] **Step 3: Implement `removeCgroup`**

Append to `internal/provider/firecracker/cgroup.go`:

```go
// removeCgroup deletes the per-VM cgroup directory. Idempotent: missing
// directories are not an error. Jailer mode is a no-op (jailer cleans up
// its own cgroup tree on FC exit).
func (p *Provider) removeCgroup(vmID string) error {
	if p.config.EnableJailer {
		return nil
	}
	dir := p.cgroupCPUDir(vmID)
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove cgroup dir %s: %w", dir, err)
	}
	return nil
}
```

- [ ] **Step 4: Run; expect pass**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run TestRemoveCgroup -v
```

All green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/cgroup.go internal/provider/firecracker/cgroup_test.go
git add internal/provider/firecracker/cgroup.go internal/provider/firecracker/cgroup_test.go
git commit -m "feat(firecracker): removeCgroup helper for sandbox destroy"
```

---

## Task 8: Wire boot-time cgroup setup into `StartSandbox` (non-jailer path)

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go`

- [ ] **Step 1: Add the setup call after `machine.Start()` in `StartSandbox`**

In `internal/provider/firecracker/sandbox.go::StartSandbox`, find the block immediately after `machine.Start()` succeeds (around line 260-274 — the existing code that fetches `pid` via `machine.PID()` and writes vminfo). Insert a cgroup setup call between getting the PID and writing the info:

Find this existing block:

```go
if err := machine.Start(machineCtx); err != nil {
    network.DeleteTap(tapName)
    p.subnets.Release(subnetIdx)
    return fmt.Errorf("firecracker start machine %s: %w", vmID, err)
}

// Update vminfo with runtime state.
pid, pidErr := machine.PID()
if pidErr != nil {
    slog.Warn("firecracker: could not get PID", "vm", vmID, "error", pidErr)
}
info.PID = pid
info.TapDevice = tapName
info.SubnetIdx = subnetIdx
info.Write(infoPath)
```

Replace with (insert the cgroup setup between PID fetch and info.Write):

```go
if err := machine.Start(machineCtx); err != nil {
    network.DeleteTap(tapName)
    p.subnets.Release(subnetIdx)
    return fmt.Errorf("firecracker start machine %s: %w", vmID, err)
}

// Update vminfo with runtime state.
pid, pidErr := machine.PID()
if pidErr != nil {
    slog.Warn("firecracker: could not get PID", "vm", vmID, "error", pidErr)
}
info.PID = pid
info.TapDevice = tapName
info.SubnetIdx = subnetIdx

// Best-effort per-VM cgroup setup. Failures (permission denied in DinD CI,
// missing cpu controller) leave info.CgroupActive=false; live CPU resize on
// such a sandbox returns ResizeReasonCgroupUnavailable. The sandbox itself
// still starts.
if pid > 0 {
    if err := p.setupCgroup(pid, vmID, info.LimitCPU); err != nil {
        slog.Warn("firecracker: cgroup setup failed; live CPU resize disabled for this VM",
            "vm", vmID, "err", err)
    } else {
        info.CgroupActive = true
    }
}

info.Write(infoPath)
```

- [ ] **Step 2: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 3: Run FC tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/
```

All green (existing tests are unaffected; new cgroup tests already pass).

- [ ] **Step 4: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/sandbox.go
git add internal/provider/firecracker/sandbox.go
git commit -m "feat(firecracker): per-VM cgroup setup at StartSandbox (non-jailer)"
```

---

## Task 9: Wire boot-time cgroup setup into `startFromSnapshot`

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go`

`startFromSnapshot` is the second VM boot path (snapshot/CoW restore). It mirrors `StartSandbox` and needs the same cgroup setup.

- [ ] **Step 1: Find the `machine.Start` call in `startFromSnapshot`**

In `internal/provider/firecracker/sandbox.go::startFromSnapshot` (around line 380-396), find the same pattern: `machine.Start(machineCtx)` followed by `pid, pidErr := machine.PID()`.

- [ ] **Step 2: Insert the same cgroup setup block**

After `pid, pidErr := machine.PID()` and before the existing `info.Write(infoPath)`, add:

```go
if pid > 0 {
    if err := p.setupCgroup(pid, vmID, info.LimitCPU); err != nil {
        slog.Warn("firecracker: cgroup setup failed; live CPU resize disabled for this VM",
            "vm", vmID, "err", err)
    } else {
        info.CgroupActive = true
    }
}
```

- [ ] **Step 3: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 4: Run FC tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/
```

All green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/sandbox.go
git add internal/provider/firecracker/sandbox.go
git commit -m "feat(firecracker): cgroup setup also in snapshot-restore boot path"
```

---

## Task 10: Wire jailer `CgroupArgs` for initial limit

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go`

In jailer mode, the FC SDK's `JailerCfg.CgroupArgs` accepts key=value pairs that the jailer applies to the cgroup before exec'ing FC. We use this to set the initial CPU bandwidth limit.

- [ ] **Step 1: Find the `JailerCfg` literal in `StartSandbox`**

In `internal/provider/firecracker/sandbox.go::StartSandbox`, find the `JailerCfg` literal (around line 195-202, inside an `if p.config.EnableJailer` block):

```go
jailerCfg := &fcsdk.JailerConfig{
    GID:            fcsdk.Int(info.UID),
    UID:            fcsdk.Int(info.UID),
    ID:             vmID,
    NumaNode:       fcsdk.Int(0),
    ExecFile:       p.config.FirecrackerBin,
    JailerBinary:   p.config.JailerBin,
    ChrootBaseDir:  p.config.ChrootBase,
    ChrootStrategy: fcsdk.NewNaiveChrootStrategy(p.config.KernelPath),
    CgroupVersion:  p.cgroupVersion,
}
```

- [ ] **Step 2: Compute the cgroup args based on `info.LimitCPU` and the cgroup version**

Just before the `JailerConfig{...}` literal, compute:

```go
cgroupArgs := []string{}
quota := info.LimitCPU * cpuPeriod
if p.cgroupVersion == "1" {
    cgroupArgs = append(cgroupArgs,
        fmt.Sprintf("cpu.cfs_quota_us=%d", quota),
        fmt.Sprintf("cpu.cfs_period_us=%d", cpuPeriod),
    )
} else {
    cgroupArgs = append(cgroupArgs, fmt.Sprintf("cpu.max=%d %d", quota, cpuPeriod))
}
```

Then add `CgroupArgs: cgroupArgs,` to the `JailerConfig{...}` literal:

```go
jailerCfg := &fcsdk.JailerConfig{
    // ...existing fields...
    CgroupVersion:  p.cgroupVersion,
    CgroupArgs:     cgroupArgs,
}
```

- [ ] **Step 3: Apply the same change in `startFromSnapshot`**

The same `JailerCfg` literal exists around line 343-353 in `startFromSnapshot`. Apply the same `cgroupArgs` computation and `CgroupArgs:` field addition.

- [ ] **Step 4: Mark jailer-mode sandboxes as `CgroupActive = true` at boot**

Since jailer applies the cgroup before exec, jailer-mode sandboxes have an active cgroup as soon as `machine.Start()` returns. In both `StartSandbox` and `startFromSnapshot`, set `info.CgroupActive = true` unconditionally when `p.config.EnableJailer == true` (the `setupCgroup` no-op path doesn't set the field).

Find the existing block from Task 8/9:

```go
if pid > 0 {
    if err := p.setupCgroup(pid, vmID, info.LimitCPU); err != nil {
        slog.Warn("firecracker: cgroup setup failed; live CPU resize disabled for this VM",
            "vm", vmID, "err", err)
    } else {
        info.CgroupActive = true
    }
}
```

Update to:

```go
if p.config.EnableJailer {
    // Jailer set the cgroup before exec via CgroupArgs; cgroup is active.
    info.CgroupActive = true
} else if pid > 0 {
    if err := p.setupCgroup(pid, vmID, info.LimitCPU); err != nil {
        slog.Warn("firecracker: cgroup setup failed; live CPU resize disabled for this VM",
            "vm", vmID, "err", err)
    } else {
        info.CgroupActive = true
    }
}
```

Apply this update in BOTH `StartSandbox` and `startFromSnapshot`.

- [ ] **Step 5: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 6: Run FC tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/
```

All green.

- [ ] **Step 7: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/sandbox.go
git add internal/provider/firecracker/sandbox.go
git commit -m "feat(firecracker): jailer CgroupArgs for boot-time CPU limit + CgroupActive flag"
```

---

## Task 11: Implement live CPU resize in `UpdateResources`

**Files:**
- Modify: `internal/provider/firecracker/sandbox_resize.go`
- Modify: `internal/provider/firecracker/sandbox_resize_test.go`

This is the core of the spec. Replace the CPU rejection with an actual cgroup write.

- [ ] **Step 1: Read the existing tests to understand the harness**

```bash
grep -n "func Test\|TestUpdateResources" internal/provider/firecracker/sandbox_resize_test.go | head -20
```

Identify the existing test that asserts CPU is rejected (likely named `TestUpdateResources_CPULimit_Rejected_OnFC` or similar). Note the test setup pattern (how the Provider, VMInfo, and request are constructed).

- [ ] **Step 2: Flip the existing CPU-rejected test + add new tests**

In `internal/provider/firecracker/sandbox_resize_test.go`:

(a) Find the existing CPU-rejected test and replace its body to assert the success path. Rename if needed (e.g. `TestUpdateResources_CPU_AppliedViaCgroup`):

```go
func TestUpdateResources_CPU_AppliedViaCgroup(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: tmp, EnableJailer: false},
		cgroupVersion: "2",
		vms: map[string]*VMInfo{
			"vm-cpu": {
				ID:           "vm-cpu",
				PID:          os.Getpid(),
				LimitCPU:     1,
				CeilingCPU:   4,
				CgroupActive: true,
			},
		},
	}
	// Pre-create the cgroup directory like setupCgroup would have at boot.
	if err := os.MkdirAll(filepath.Join(tmp, "vm-cpu"), 0755); err != nil {
		t.Fatal(err)
	}

	cpu := 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-cpu"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}

	// Verify cpu.max was written.
	got, err := os.ReadFile(filepath.Join(tmp, "vm-cpu", "cpu.max"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "200000 100000" {
		t.Errorf("cpu.max = %q, want %q", string(got), "200000 100000")
	}

	// Verify VMInfo.LimitCPU updated.
	if p.vms["vm-cpu"].LimitCPU != 2 {
		t.Errorf("LimitCPU = %d, want 2", p.vms["vm-cpu"].LimitCPU)
	}
}
```

> **Note on `info.Write` in the test:** the existing `UpdateResources` code calls `info.Write(p.vmInfoPath(vmID))` after success. The test path `tmp/vm-cpu/vminfo.json` will be written; that's fine since `tmp` is a tempdir. If `vmInfoPath` derives from `p.config.ChrootBase` rather than `CgroupRoot`, set `ChrootBase: tmp` too in the Provider literal so the write target is also under tmp.

(b) Add the ceiling-rejection test:

```go
func TestUpdateResources_CPU_ExceedsCeiling(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: tmp, ChrootBase: tmp, EnableJailer: false},
		cgroupVersion: "2",
		vms: map[string]*VMInfo{
			"vm-c": {
				ID:           "vm-c",
				PID:          os.Getpid(),
				LimitCPU:     1,
				CeilingCPU:   2,
				CgroupActive: true,
			},
		},
	}
	cpu := 4 // > ceiling of 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-c"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("expected ProviderResizeError, got %T: %v", err, err)
	}
	if prErr.Reason != domain.ResizeReasonExceedsCeiling {
		t.Errorf("reason = %q, want %q", prErr.Reason, domain.ResizeReasonExceedsCeiling)
	}
}
```

(c) Add the cgroup-unavailable test:

```go
func TestUpdateResources_CPU_NoCgroup_Unavailable(t *testing.T) {
	p := &Provider{
		cgroupVersion: "2",
		vms: map[string]*VMInfo{
			"vm-nc": {
				ID:           "vm-nc",
				LimitCPU:     1,
				CeilingCPU:   4,
				CgroupActive: false,
			},
		},
	}
	cpu := 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-nc"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("expected ProviderResizeError, got %T", err)
	}
	if prErr.Reason != domain.ResizeReasonCgroupUnavailable {
		t.Errorf("reason = %q, want %q", prErr.Reason, domain.ResizeReasonCgroupUnavailable)
	}
}
```

Add `"errors"`, `"os"`, `"path/filepath"`, `"strings"` to the imports if not already present, plus `"github.com/navaris/navaris/internal/domain"`.

- [ ] **Step 3: Run; expect failures**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run "TestUpdateResources_CPU" -v
```

Expected: the original "rejected" test fails (because we changed its body to assert success on the not-yet-implemented path); the new tests fail similarly.

- [ ] **Step 4: Update `UpdateResources` in `sandbox_resize.go`**

Replace the existing CPU rejection branch (around line 21-26):

```go
if req.CPULimit != nil {
    return &domain.ProviderResizeError{
        Reason: domain.ResizeReasonCPUUnsupportedByBackend,
        Detail: "Firecracker provider in this build does not support live vCPU resize",
    }
}
```

With the new implementation:

```go
if req.CPULimit != nil {
    p.vmMu.RLock()
    info, ok := p.vms[ref.Ref]
    p.vmMu.RUnlock()
    if !ok {
        return fmt.Errorf("firecracker: vm %q not found: %w", ref.Ref, domain.ErrNotFound)
    }

    newCPU := int64(*req.CPULimit)
    ceiling := info.CeilingCPU
    if ceiling == 0 {
        ceiling = info.VcpuCount // pre-headroom sandbox
    }
    if newCPU > ceiling {
        return &domain.ProviderResizeError{
            Reason: domain.ResizeReasonExceedsCeiling,
            Detail: fmt.Sprintf("cpu_limit %d > ceiling %d", newCPU, ceiling),
        }
    }
    if !info.CgroupActive {
        return &domain.ProviderResizeError{
            Reason: domain.ResizeReasonCgroupUnavailable,
            Detail: "boot-time cgroup setup did not succeed for this VM",
        }
    }

    quota := newCPU * cpuPeriod
    if err := p.writeCPUMax(p.cgroupCPUDir(ref.Ref), quota, cpuPeriod); err != nil {
        return &domain.ProviderResizeError{
            Reason: domain.ResizeReasonCgroupWriteFailed,
            Detail: err.Error(),
        }
    }

    p.vmMu.Lock()
    info.LimitCPU = newCPU
    p.vmMu.Unlock()
    if err := info.Write(p.vmInfoPath(ref.Ref)); err != nil {
        return fmt.Errorf("firecracker: persist vminfo after CPU resize: %w", err)
    }
    // Fall through to memory handling if both were specified (unlikely
    // given service layer single-resource policy, but supported).
}

if req.MemoryLimitMB == nil {
    return nil
}
```

> **Note:** the existing memory branch checks `req.MemoryLimitMB == nil` to short-circuit. After our CPU branch, ensure both directions are correctly returned. The structure above falls through to memory only when memory is also non-nil; otherwise returns nil after CPU.

Update the file's package doc comment if it asserts "no CPU resize support" — replace with a brief description of what the CPU path does now.

- [ ] **Step 5: Run tests; expect pass**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run "TestUpdateResources" -v
go test -tags firecracker ./internal/provider/firecracker/
```

All green.

- [ ] **Step 6: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 7: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/sandbox_resize.go internal/provider/firecracker/sandbox_resize_test.go
git add internal/provider/firecracker/sandbox_resize.go internal/provider/firecracker/sandbox_resize_test.go
git commit -m "feat(firecracker): live CPU resize via cgroup CPU bandwidth"
```

---

## Task 12: Cgroup cleanup on destroy

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go`

- [ ] **Step 1: Find the destroy / stop teardown path**

```bash
grep -n "func.*StopSandbox\|func.*DestroySandbox" internal/provider/firecracker/sandbox.go
```

Look for the function that runs the final cleanup after the FC process has exited. The boost listener cleanup (`p.stopBoostListener(vmID)`) is already called there — we add the cgroup removal next to it.

- [ ] **Step 2: Add `removeCgroup` call**

In the destroy/stop path, after FC has exited and after `stopBoostListener` is called, add:

```go
if err := p.removeCgroup(vmID); err != nil {
    slog.Warn("firecracker: remove cgroup", "vm", vmID, "err", err)
}
```

(Failure is logged, never fatal — stale cgroups are harmless and clean themselves up on host reboot.)

If both `StopSandbox` and `DestroySandbox` exist as separate functions, add the call to whichever runs LAST (the one that fires when the VM truly goes away). If unsure, add to `DestroySandbox`; the boost listener pattern already does this idempotently (Task 12 from spec #15).

- [ ] **Step 3: Build all four configurations**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

- [ ] **Step 4: Run FC tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/
```

All green.

- [ ] **Step 5: gofmt and commit**

```bash
gofmt -l internal/provider/firecracker/sandbox.go
git add internal/provider/firecracker/sandbox.go
git commit -m "feat(firecracker): remove per-VM cgroup on sandbox destroy"
```

---

## Task 13: Integration test — `TestSandbox_HonorsRequestedCPULimit`

**Files:**
- Create: `test/integration/limits_cpu_test.go`

This mirrors `TestSandbox_HonorsRequestedMemoryLimit` (in `limits_test.go`) but for CPU. The check reads `/sys/fs/cgroup/cpu.max` from inside the FC guest — the file is enforced by the kernel and visible inside the guest because FC's kernel mounts cgroupfs by default.

- [ ] **Step 1: Create the test file**

Create `test/integration/limits_cpu_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// TestSandbox_HonorsRequestedCPULimit creates a Firecracker sandbox with
// cpu_limit=2 and verifies that /sys/fs/cgroup/cpu.max inside the guest
// reflects that limit. FC mounts cgroupfs by default, and the per-VM
// cgroup is propagated as the guest's root cgroup.
//
// Skipped on Incus: limits.cpu enforcement inside docker-in-docker is
// unreliable (see TestBoost_E2E_Incus_CPU_VisibleInGuest skip comment).
// The Incus path uses limits.cpu cgroup writes too, but verifying it
// requires a non-DinD environment.
func TestSandbox_HonorsRequestedCPULimit(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): cpuset enforcement unreliable in DinD", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu := 2
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "limits-cpu-2",
		ImageID:   img,
		CPULimit:  &cpu,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	// Read the cgroup v2 cpu.max file from inside the guest. Format: "<quota> <period>".
	// FC mounts cgroupfs at /sys/fs/cgroup; the per-VM cgroup is the guest's root cgroup.
	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "cat /sys/fs/cgroup/cpu.max 2>/dev/null || cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us"},
	})
	if err != nil {
		t.Fatalf("exec read cpu.max: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Skipf("cpu.max not readable from guest (exit=%d, stderr=%s); guest kernel may not mount cgroupfs", exec.ExitCode, exec.Stderr)
	}

	raw := strings.TrimSpace(exec.Stdout)
	t.Logf("guest cpu.max: %q", raw)

	// Parse "<quota> <period>" (v2) or just "<quota>" (v1). Expected quota: cpu_limit * 100000.
	parts := strings.Fields(raw)
	if len(parts) == 0 || parts[0] == "max" {
		t.Fatalf("unexpected cpu.max value: %q", raw)
	}
	quota, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("parse quota %q: %v", parts[0], err)
	}
	const expectedQuota = 2 * 100_000
	if quota != expectedQuota {
		t.Errorf("cpu.max quota = %d, want %d (cpu_limit=2 * period 100000)", quota, expectedQuota)
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
gofmt -l test/integration/limits_cpu_test.go
git add test/integration/limits_cpu_test.go
git commit -m "test(integration): TestSandbox_HonorsRequestedCPULimit (FC, via cpu.max)"
```

---

## Task 14: Integration test — `TestBoost_E2E_FC_CPU_AppliesToGuest`

**Files:**
- Modify: `test/integration/boost_e2e_test.go`

Mirrors the existing `TestBoost_E2E_FC_Memory_VisibleInGuest` but for CPU.

- [ ] **Step 1: Append the new test**

In `test/integration/boost_e2e_test.go`, append (after the existing memory test, reusing `ptrIntE2E`):

```go
// TestBoost_E2E_FC_CPU_AppliesToGuest creates a Firecracker sandbox with
// cpu_limit=1, boosts to cpu_limit=2 within the boot ceiling, and reads
// /sys/fs/cgroup/cpu.max from inside the guest to verify the change. Cancel
// reverts to cpu_limit=1.
//
// This test ONLY exercises the FC path. It implicitly assumes the daemon
// was started with --firecracker-vcpu-headroom-mult > 1.0 so that the boot
// ceiling is at least 2; otherwise the boost is rejected with exceeds_ceiling.
// CI's docker-compose-firecracker.yml uses headroom 2.0 (set in PR #17).
//
// Skipped on Incus.
func TestBoost_E2E_FC_CPU_AppliesToGuest(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s)", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu := 1
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "boost-e2e-cpu",
		ImageID:   img,
		CPULimit:  &cpu,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	beforeQuota := readGuestCPUQuota(t, c, sandboxID)
	t.Logf("cpu.max quota before boost: %d", beforeQuota)
	if beforeQuota != 100_000 {
		t.Fatalf("baseline quota = %d, want 100000 (cpu_limit=1)", beforeQuota)
	}

	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		CPULimit:        ptrIntE2E(2),
		DurationSeconds: 30,
	}); err != nil {
		if strings.Contains(err.Error(), "exceeds_ceiling") {
			t.Skipf("CPU boost rejected for exceeding ceiling — daemon may need --firecracker-vcpu-headroom-mult>1.0; got: %v", err)
		}
		t.Fatalf("StartBoost: %v", err)
	}

	duringQuota := readGuestCPUQuota(t, c, sandboxID)
	t.Logf("cpu.max quota during boost: %d", duringQuota)
	if duringQuota != 200_000 {
		t.Errorf("quota during boost = %d, want 200000 (cpu_limit=2)", duringQuota)
	}

	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}

	afterQuota := readGuestCPUQuota(t, c, sandboxID)
	t.Logf("cpu.max quota after cancel: %d", afterQuota)
	if afterQuota != 100_000 {
		t.Errorf("quota after cancel = %d, want 100000 (reverted)", afterQuota)
	}
}

// readGuestCPUQuota reads the CFS quota microseconds from inside the
// sandbox via /sys/fs/cgroup/cpu.max (v2) or /sys/fs/cgroup/cpu/cpu.cfs_quota_us (v1).
func readGuestCPUQuota(t *testing.T, c *client.Client, sandboxID string) int {
	t.Helper()
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "cat /sys/fs/cgroup/cpu.max 2>/dev/null || cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us"},
	})
	if err != nil {
		t.Fatalf("exec read cpu.max: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec exit %d: stderr=%s", exec.ExitCode, exec.Stderr)
	}
	parts := strings.Fields(strings.TrimSpace(exec.Stdout))
	if len(parts) == 0 || parts[0] == "max" {
		t.Fatalf("unexpected cpu.max value: %q", exec.Stdout)
	}
	q, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("parse quota %q: %v", parts[0], err)
	}
	return q
}
```

Add `"strconv"` to the imports if not already present.

- [ ] **Step 2: Compile-only check**

```bash
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both clean.

- [ ] **Step 3: gofmt and commit**

```bash
gofmt -l test/integration/boost_e2e_test.go
git add test/integration/boost_e2e_test.go
git commit -m "test(integration): TestBoost_E2E_FC_CPU_AppliesToGuest (FC, via cpu.max)"
```

---

## Task 15: Enable headroom in CI compose so CPU boost test runs

**Files:**
- Modify: `docker-compose.integration-firecracker.yml`
- Modify: `docker-compose.integration-firecracker-cow.yml`

The new CPU boost test needs `--firecracker-vcpu-headroom-mult > 1.0` to allow growing from `cpu_limit=1` to `2`. Without it, the test skips (which is acceptable but reduces coverage).

- [ ] **Step 1: Add the flag to both FC compose files**

In `docker-compose.integration-firecracker.yml`, in the `navarisd` service's `command:` list, add:

```yaml
      - --firecracker-vcpu-headroom-mult=2.0
      - --firecracker-mem-headroom-mult=2.0
```

(Adding memory headroom too so the existing memory grow test in PR #17's local-only suite could also run in CI if env-gated to do so. It's a single-flag change either way.)

In `docker-compose.integration-firecracker-cow.yml`, do the same in the `navarisd` service.

- [ ] **Step 2: Build matrix unaffected (compose-only change)**

No need to re-run go builds; this is purely runtime config.

- [ ] **Step 3: Commit**

```bash
git add docker-compose.integration-firecracker.yml docker-compose.integration-firecracker-cow.yml
git commit -m "ci: enable FC CPU+memory headroom mult=2.0 for boost grow tests"
```

---

## Task 16: Documentation updates

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-04-25-runtime-resource-resize-design.md`

- [ ] **Step 1: Update the README**

In `README.md`, find the resource-limits / boost feature lines. Locate the line that mentions FC CPU resize being unsupported (likely a caveat in the runtime-resize or boost feature bullet).

If such a caveat exists, remove it. Otherwise, augment the boost line to mention CPU is now also live-resizable on FC:

```markdown
- **Time-bounded boost**: temporarily raise CPU/memory for a fixed duration via `POST /v1/sandboxes/{id}/boost`; the daemon auto-reverts at expiry (with retry-on-failure). FC CPU is enforced via cgroup CPU bandwidth (`cpu.max`); FC memory via virtio-balloon. Cap with `--boost-max-duration` (default 1h, max 24h).
```

(Adapt to the actual phrasing in the README.)

- [ ] **Step 2: Cross-reference from the runtime-resize spec**

In `docs/superpowers/specs/2026-04-25-runtime-resource-resize-design.md`, find §3.5 (the section that originally said "CPU resize lands in a follow-up spec"). Append a one-line cross-reference:

```markdown
**Update (2026-04-27):** completed in [2026-04-27-firecracker-live-cpu-resize-design.md](2026-04-27-firecracker-live-cpu-resize-design.md). FC CPU is enforced via cgroup CPU bandwidth, not vCPU hot-plug.
```

- [ ] **Step 3: Commit**

```bash
git add README.md docs/superpowers/specs/2026-04-25-runtime-resource-resize-design.md
git commit -m "docs: README + cross-reference for FC live CPU resize"
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

All four green.

- [ ] **Compile integration tests:**

```bash
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both clean.

- [ ] **Verify daemon flag:**

```bash
go build -tags 'incus firecracker' -o /tmp/navarisd ./cmd/navarisd/
/tmp/navarisd --help 2>&1 | grep firecracker-cgroup-root
```

Expected: one line shown.

- [ ] **Local smoke test (optional, requires FC env):**

```bash
make integration-env-firecracker-headroom  # already exists from PR #17
NAVARIS_API_URL=http://localhost:18080 NAVARIS_TOKEN=test-token NAVARIS_BASE_IMAGE=alpine-3.21 \
    go test -tags integration -v -run "TestSandbox_HonorsRequestedCPULimit|TestBoost_E2E_FC_CPU_AppliesToGuest" ./test/integration/
make integration-env-firecracker-headroom-down
```

- [ ] **Open a PR** referencing the spec doc and this plan.
