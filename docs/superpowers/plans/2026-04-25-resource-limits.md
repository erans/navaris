# Sandbox Resource Limits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire `req.CPULimit` and `req.MemoryLimitMB` into the Firecracker provider's `MachineCfg` (Incus already reads them), add backend-agnostic validation at the service layer, and introduce `--firecracker-default-vcpu` / `--firecracker-default-memory-mb` daemon flags.

**Architecture:** A new `domain.ErrInvalidArgument` sentinel maps to HTTP 400. A new `validateLimits` helper in `internal/service/limits.go` is called at the top of `SandboxService.Create` and `CreateFromSnapshot` — it bounds-checks non-nil values (CPU 1..32, memory 128..8192) and rejects any limit on from-snapshot. The Firecracker provider reads `req.CPULimit` / `req.MemoryLimitMB`, falling back to per-provider defaults plumbed from new daemon flags.

**Tech Stack:** Go 1.22+, build tag `//go:build firecracker` for VMM-specific code. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-25-resource-limits-design.md`

---

## File Structure

**New files:**
- `internal/service/limits.go` — `validateLimits(opts CreateSandboxOpts, fromSnapshot bool) error`.
- `internal/service/limits_test.go` — table-driven validation tests.
- `test/integration/limits_test.go` — `TestSandbox_HonorsRequestedMemoryLimit` (build tag `integration`).

**Modified files:**
- `internal/domain/errors.go` — add `ErrInvalidArgument`.
- `internal/api/response.go` — map `ErrInvalidArgument` to 400.
- `internal/api/response_test.go` — extend mapping test.
- `internal/api/sandbox_test.go` — two new 400 tests.
- `internal/service/sandbox.go` — call `validateLimits` in `Create` and `CreateFromSnapshot`.
- `internal/service/sandbox_test.go` — two new validation tests.
- `internal/provider/firecracker/firecracker.go` — `Config{DefaultVcpuCount, DefaultMemoryMib}` + `defaults()`.
- `internal/provider/firecracker/sandbox.go` — replace hardcoded `MachineCfg`.
- `internal/provider/firecracker/storage_wiring_test.go` — defaults + plumbing tests.
- `cmd/navarisd/main.go` — two new flags + config fields.
- `cmd/navarisd/provider_firecracker.go` — forward flags into `firecracker.Config`.
- `cmd/navarisd/storage_test.go` — flag-default plumbing test (file already exists for storage flags; add a similar test there).
- `README.md` — daemon-flags table rows for the two new flags.
- `docs/native-install.md` — env-var entries.
- `packaging/systemd/navarisd.env.example` — env-var entries.
- `packaging/systemd/navarisd-launch.sh` — env→flag mapping.

---

## Task 1: `ErrInvalidArgument` sentinel + 400 mapping

**Files:**
- Modify: `internal/domain/errors.go`
- Modify: `internal/api/response.go`
- Modify: `internal/api/response_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/api/response_test.go`, append:

```go
func TestMapErrorCode_InvalidArgument(t *testing.T) {
	got := mapErrorCode(domain.ErrInvalidArgument)
	if got != http.StatusBadRequest {
		t.Errorf("ErrInvalidArgument → %d, want %d", got, http.StatusBadRequest)
	}

	wrapped := fmt.Errorf("cpu_limit must be 1..32: %w", domain.ErrInvalidArgument)
	if got := mapErrorCode(wrapped); got != http.StatusBadRequest {
		t.Errorf("wrapped ErrInvalidArgument → %d, want %d", got, http.StatusBadRequest)
	}
}
```

Add `"fmt"` to imports if not already there.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/... -run TestMapErrorCode_InvalidArgument`
Expected: FAIL — `domain.ErrInvalidArgument` undefined.

- [ ] **Step 3: Add the sentinel**

In `internal/domain/errors.go`, inside the existing `var (...)` block, add (after `ErrNotSupported`):

```go
ErrInvalidArgument  = errors.New("invalid argument")
```

- [ ] **Step 4: Map it to 400**

In `internal/api/response.go`, inside `mapErrorCode`, add (after the `ErrConflict` check, before `ErrInvalidState`):

```go
if errors.Is(err, domain.ErrInvalidArgument) {
    return http.StatusBadRequest
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/domain/... ./internal/api/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/errors.go internal/api/response.go internal/api/response_test.go
git commit -m "$(cat <<'EOF'
feat(domain): add ErrInvalidArgument sentinel; map to HTTP 400

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `validateLimits` helper

**Files:**
- Create: `internal/service/limits.go`
- Create: `internal/service/limits_test.go`

- [ ] **Step 1: Write the failing test**

`internal/service/limits_test.go`:

```go
package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func ptrInt(v int) *int { return &v }

func TestValidateLimits(t *testing.T) {
	cases := []struct {
		name         string
		opts         CreateSandboxOpts
		fromSnapshot bool
		wantErr      bool
		wantMatch    string // substring expected in error message; "" = any
	}{
		// Both nil — always OK.
		{name: "both nil, normal", opts: CreateSandboxOpts{}, wantErr: false},
		{name: "both nil, from-snapshot", opts: CreateSandboxOpts{}, fromSnapshot: true, wantErr: false},

		// CPU bounds (normal).
		{name: "cpu 0 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(0)}, wantErr: true, wantMatch: "cpu_limit"},
		{name: "cpu -1 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(-1)}, wantErr: true, wantMatch: "cpu_limit"},
		{name: "cpu 33 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(33)}, wantErr: true, wantMatch: "cpu_limit"},
		{name: "cpu 1 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(1)}, wantErr: false},
		{name: "cpu 16 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(16)}, wantErr: false},
		{name: "cpu 32 normal", opts: CreateSandboxOpts{CPULimit: ptrInt(32)}, wantErr: false},

		// Memory bounds (normal).
		{name: "mem 0 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(0)}, wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "mem 127 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(127)}, wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "mem 8193 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(8193)}, wantErr: true, wantMatch: "memory_limit_mb"},
		{name: "mem 128 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(128)}, wantErr: false},
		{name: "mem 1024 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(1024)}, wantErr: false},
		{name: "mem 8192 normal", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(8192)}, wantErr: false},

		// From-snapshot: any non-nil rejected.
		{name: "cpu set on from-snapshot", opts: CreateSandboxOpts{CPULimit: ptrInt(2)}, fromSnapshot: true, wantErr: true, wantMatch: "cpu_limit cannot be set on from-snapshot"},
		{name: "mem set on from-snapshot", opts: CreateSandboxOpts{MemoryLimitMB: ptrInt(512)}, fromSnapshot: true, wantErr: true, wantMatch: "memory_limit_mb cannot be set on from-snapshot"},
		{name: "both set on from-snapshot reports first", opts: CreateSandboxOpts{CPULimit: ptrInt(2), MemoryLimitMB: ptrInt(512)}, fromSnapshot: true, wantErr: true, wantMatch: "cpu_limit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLimits(tc.opts, tc.fromSnapshot)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, domain.ErrInvalidArgument) {
					t.Errorf("error %v should wrap ErrInvalidArgument", err)
				}
				if tc.wantMatch != "" && !strings.Contains(err.Error(), tc.wantMatch) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantMatch)
				}
			} else if err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/... -run TestValidateLimits -v`
Expected: FAIL — `validateLimits` undefined.

- [ ] **Step 3: Implement `validateLimits`**

`internal/service/limits.go`:

```go
package service

import (
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

// Sandbox resource limit bounds. The CPU upper bound is Firecracker's
// MAX_SUPPORTED_VCPUS (32). The memory bounds are policy: 128 MB is the
// floor where most modern guest kernels boot without panic; 8192 MB is a
// sane sandbox ceiling. Operators who need higher must edit these
// constants — no daemon flag is exposed (deliberately, to keep policy
// decisions out of operator hands at this stage).
const (
	limitMinCPU   = 1
	limitMaxCPU   = 32
	limitMinMemMB = 128
	limitMaxMemMB = 8192
)

// validateLimits checks CPULimit / MemoryLimitMB against the bounds
// above and rejects any non-nil value when fromSnapshot is true (snapshot
// restores carry vmstate.bin-baked values; an override would silently or
// noisily diverge). Returns nil if all checks pass.
//
// Errors wrap domain.ErrInvalidArgument so the API maps them to 400.
func validateLimits(opts CreateSandboxOpts, fromSnapshot bool) error {
	if fromSnapshot {
		if opts.CPULimit != nil {
			return fmt.Errorf("cpu_limit cannot be set on from-snapshot create; vCPU count is baked into the snapshot: %w", domain.ErrInvalidArgument)
		}
		if opts.MemoryLimitMB != nil {
			return fmt.Errorf("memory_limit_mb cannot be set on from-snapshot create; memory size is baked into the snapshot: %w", domain.ErrInvalidArgument)
		}
		return nil
	}
	if opts.CPULimit != nil {
		v := *opts.CPULimit
		if v < limitMinCPU || v > limitMaxCPU {
			return fmt.Errorf("cpu_limit must be %d..%d, got %d: %w", limitMinCPU, limitMaxCPU, v, domain.ErrInvalidArgument)
		}
	}
	if opts.MemoryLimitMB != nil {
		v := *opts.MemoryLimitMB
		if v < limitMinMemMB || v > limitMaxMemMB {
			return fmt.Errorf("memory_limit_mb must be %d..%d, got %d: %w", limitMinMemMB, limitMaxMemMB, v, domain.ErrInvalidArgument)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/... -run TestValidateLimits -v`
Expected: PASS — all 14 sub-tests green.

- [ ] **Step 5: Run vet and the full service package**

Run: `go vet ./internal/service/... && go test ./internal/service/...`
Expected: clean + PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/limits.go internal/service/limits_test.go
git commit -m "$(cat <<'EOF'
feat(service): add validateLimits with CPU/memory bounds and from-snapshot rejection

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Wire `validateLimits` into the service entry points

**Files:**
- Modify: `internal/service/sandbox.go`
- Modify: `internal/service/sandbox_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/service/sandbox_test.go`, append:

```go
func TestCreate_RejectsOutOfBoundsMemory(t *testing.T) {
	env := newServiceEnv(t)
	mem := 8193
	op, err := env.svc.Create(context.Background(), env.projectID, "too-big",
		"alpine-3.21", CreateSandboxOpts{MemoryLimitMB: &mem})
	if err == nil {
		t.Fatalf("expected error, got op=%+v", op)
	}
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("error %v should wrap ErrInvalidArgument", err)
	}
	// Sandbox row must NOT have been created.
	all, _ := env.svc.sandboxes.List(context.Background(), domain.SandboxFilter{})
	for _, sb := range all {
		if sb.Name == "too-big" {
			t.Errorf("sandbox 'too-big' was created despite validation failure")
		}
	}
}

func TestCreateFromSnapshot_RejectsCPULimit(t *testing.T) {
	env := newServiceEnv(t)
	// Insert a snapshot row so the snapshot lookup succeeds far enough
	// to hit validateLimits — actually validateLimits runs first, so the
	// snapshot doesn't need to exist.
	cpu := 2
	op, err := env.svc.CreateFromSnapshot(context.Background(), env.projectID, "from-snap",
		"snap-irrelevant", CreateSandboxOpts{CPULimit: &cpu})
	if err == nil {
		t.Fatalf("expected error, got op=%+v", op)
	}
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("error %v should wrap ErrInvalidArgument", err)
	}
}
```

If the imports in `sandbox_test.go` don't already include `"errors"` and `"context"`, add them.

If `newServiceEnv` doesn't already populate `env.projectID`, look at `internal/service/sandbox_fork_test.go` (it has a `mustCreateSandbox` helper that assumes a project) and follow the same pattern. If you need to create a project inline for these tests, do:

```go
proj, err := env.svc.projects.Create(context.Background(), &domain.Project{Name: "test"})
if err != nil { t.Fatalf("create project: %v", err) }
projectID := proj.ProjectID
```

— and use `projectID` in the calls. Inspect the existing `serviceEnv` struct in `internal/service/sandbox_test.go` (it's defined alongside `newServiceEnv`) to see what's already wired.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/... -run "TestCreate_RejectsOutOfBoundsMemory|TestCreateFromSnapshot_RejectsCPULimit"`
Expected: FAIL — service doesn't validate yet, so it tries to enqueue and may succeed or fail with a different error.

- [ ] **Step 3: Wire `validateLimits` into Create**

In `internal/service/sandbox.go`, find `func (s *SandboxService) Create(ctx context.Context, projectID, name, imageID string, opts CreateSandboxOpts) (*domain.Operation, error)` (around line 96).

Immediately after the function signature opens (before any other work), add:

```go
if err := validateLimits(opts, false); err != nil {
    return nil, err
}
```

Place it as the very first statement in the function body, before the OpenTelemetry span and any DB work. Failing fast means no span is opened for an invalid request — clean and consistent.

- [ ] **Step 4: Wire `validateLimits` into CreateFromSnapshot**

In the same file, find `func (s *SandboxService) CreateFromSnapshot(...) (*domain.Operation, error)` (around line 154).

Immediately after the function signature opens, add:

```go
if err := validateLimits(opts, true); err != nil {
    return nil, err
}
```

Same pattern — first statement in the body.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/service/...`
Expected: PASS — all existing service tests + the two new ones.

- [ ] **Step 6: Run vet and the full repo**

Run: `go vet ./... && go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/service/sandbox.go internal/service/sandbox_test.go
git commit -m "$(cat <<'EOF'
feat(service): validate sandbox limits on Create and CreateFromSnapshot

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Firecracker `Config` defaults

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go`
- Modify: `internal/provider/firecracker/storage_wiring_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/provider/firecracker/storage_wiring_test.go`, append:

```go
func TestConfig_Defaults_FillsZeroLimitFields(t *testing.T) {
	cfg := Config{} // DefaultVcpuCount=0, DefaultMemoryMib=0
	cfg.defaults()
	if cfg.DefaultVcpuCount != 1 {
		t.Errorf("DefaultVcpuCount = %d, want 1", cfg.DefaultVcpuCount)
	}
	if cfg.DefaultMemoryMib != 256 {
		t.Errorf("DefaultMemoryMib = %d, want 256", cfg.DefaultMemoryMib)
	}
}

func TestConfig_Defaults_RespectsNonZeroLimitFields(t *testing.T) {
	cfg := Config{DefaultVcpuCount: 4, DefaultMemoryMib: 1024}
	cfg.defaults()
	if cfg.DefaultVcpuCount != 4 {
		t.Errorf("DefaultVcpuCount = %d, want 4 (preserved)", cfg.DefaultVcpuCount)
	}
	if cfg.DefaultMemoryMib != 1024 {
		t.Errorf("DefaultMemoryMib = %d, want 1024 (preserved)", cfg.DefaultMemoryMib)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/... -run "TestConfig_Defaults"`
Expected: FAIL — `Config` has no fields `DefaultVcpuCount` / `DefaultMemoryMib`.

- [ ] **Step 3: Add the fields and defaults**

In `internal/provider/firecracker/firecracker.go`, inside the `Config` struct (around line 25), add at the end (before the closing brace):

```go
// DefaultVcpuCount is used when CreateSandboxRequest.CPULimit is nil.
// Set via --firecracker-default-vcpu on the daemon.
DefaultVcpuCount int

// DefaultMemoryMib is used when CreateSandboxRequest.MemoryLimitMB is
// nil. Set via --firecracker-default-memory-mb on the daemon. Note
// that the API field is named ...MB but is fed straight to MemSizeMib;
// see docs/superpowers/specs/2026-04-25-resource-limits-design.md §3.5.
DefaultMemoryMib int
```

In `Config.defaults()` (around line 35-46), append:

```go
if c.DefaultVcpuCount == 0 {
    c.DefaultVcpuCount = 1
}
if c.DefaultMemoryMib == 0 {
    c.DefaultMemoryMib = 256
}
```

- [ ] **Step 4: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/firecracker.go internal/provider/firecracker/storage_wiring_test.go
git commit -m "$(cat <<'EOF'
feat(firecracker): add DefaultVcpuCount/DefaultMemoryMib config fields

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Wire `req.CPULimit` / `req.MemoryLimitMB` into Firecracker `MachineCfg`

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go`
- Modify: `internal/provider/firecracker/storage_wiring_test.go`

- [ ] **Step 1: Read the surrounding context**

Open `internal/provider/firecracker/sandbox.go` and find the `MachineCfg` literal (around line 141-144). Note the surrounding `fcsdk.Config{...}` block; we modify only the `MachineCfg` field.

- [ ] **Step 2: Write the failing test**

In `internal/provider/firecracker/storage_wiring_test.go`, append:

```go
func TestResolveMachineLimits(t *testing.T) {
	p := &Provider{config: Config{DefaultVcpuCount: 1, DefaultMemoryMib: 256}}

	cases := []struct {
		name     string
		req      domain.CreateSandboxRequest
		wantVcpu int64
		wantMem  int64
	}{
		{name: "all nil → defaults", req: domain.CreateSandboxRequest{}, wantVcpu: 1, wantMem: 256},
		{name: "cpu set", req: domain.CreateSandboxRequest{CPULimit: ptrInt(4)}, wantVcpu: 4, wantMem: 256},
		{name: "mem set", req: domain.CreateSandboxRequest{MemoryLimitMB: ptrInt(512)}, wantVcpu: 1, wantMem: 512},
		{name: "both set", req: domain.CreateSandboxRequest{CPULimit: ptrInt(2), MemoryLimitMB: ptrInt(1024)}, wantVcpu: 2, wantMem: 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vcpu, mem := p.resolveMachineLimits(tc.req)
			if vcpu != tc.wantVcpu {
				t.Errorf("vcpu = %d, want %d", vcpu, tc.wantVcpu)
			}
			if mem != tc.wantMem {
				t.Errorf("mem = %d, want %d", mem, tc.wantMem)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }
```

If `ptrInt` is already declared elsewhere in this test package, drop the helper from the snippet above. Check first with `grep -n "func ptrInt" internal/provider/firecracker/`.

If the imports don't include `"github.com/navaris/navaris/internal/domain"`, add it.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test -tags firecracker ./internal/provider/firecracker/... -run TestResolveMachineLimits`
Expected: FAIL — `p.resolveMachineLimits` undefined.

- [ ] **Step 4: Extract the resolution helper**

In `internal/provider/firecracker/sandbox.go`, add a small private method (place it just above or below `CreateSandbox`, but inside the same file):

```go
// resolveMachineLimits picks the vCPU count and memory size for a new
// VM, falling back to the provider's configured defaults when the
// request leaves them unset. See
// docs/superpowers/specs/2026-04-25-resource-limits-design.md §3.4.
func (p *Provider) resolveMachineLimits(req domain.CreateSandboxRequest) (vcpu, mem int64) {
	vcpu = int64(p.config.DefaultVcpuCount)
	if req.CPULimit != nil {
		vcpu = int64(*req.CPULimit)
	}
	mem = int64(p.config.DefaultMemoryMib)
	if req.MemoryLimitMB != nil {
		mem = int64(*req.MemoryLimitMB)
	}
	return vcpu, mem
}
```

- [ ] **Step 5: Use the helper in `MachineCfg`**

In `internal/provider/firecracker/sandbox.go`, find the `MachineCfg` literal (around line 141-144). It currently reads:

```go
MachineCfg: models.MachineConfiguration{
    VcpuCount:  fcsdk.Int64(1),
    MemSizeMib: fcsdk.Int64(256),
},
```

Replace with:

```go
MachineCfg: func() models.MachineConfiguration {
    vcpu, mem := p.resolveMachineLimits(req)
    return models.MachineConfiguration{
        VcpuCount:  fcsdk.Int64(vcpu),
        MemSizeMib: fcsdk.Int64(mem),
    }
}(),
```

(The closure-and-call form keeps the call site compact and mirrors the existing struct-literal style. If you'd rather, hoist the two `vcpu, mem :=` lines above the `fcCfg :=` declaration and reference them directly inside `MachineCfg`.)

- [ ] **Step 6: Run tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/...`
Expected: PASS — the new `TestResolveMachineLimits` passes plus all existing tests.

- [ ] **Step 7: Verify build cleanliness**

Run: `go build -tags firecracker ./... && go vet -tags firecracker ./internal/provider/firecracker/...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/provider/firecracker/sandbox.go internal/provider/firecracker/storage_wiring_test.go
git commit -m "$(cat <<'EOF'
feat(firecracker): honor req.CPULimit and req.MemoryLimitMB in MachineCfg

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Daemon flags + plumbing into `firecracker.Config`

**Files:**
- Modify: `cmd/navarisd/main.go`
- Modify: `cmd/navarisd/provider_firecracker.go`
- Modify: `cmd/navarisd/storage_test.go`

- [ ] **Step 1: Write the failing test**

In `cmd/navarisd/storage_test.go`, append:

```go
func TestParseFlags_FirecrackerDefaults(t *testing.T) {
	// Defaults
	cfg := config{}
	resetFlags(t)
	parseFlagsInto(&cfg, []string{"navarisd"})
	if cfg.firecrackerDefaultVcpu != 1 {
		t.Errorf("default vcpu = %d, want 1", cfg.firecrackerDefaultVcpu)
	}
	if cfg.firecrackerDefaultMemoryMB != 256 {
		t.Errorf("default memory = %d, want 256", cfg.firecrackerDefaultMemoryMB)
	}

	// Explicit values
	cfg = config{}
	resetFlags(t)
	parseFlagsInto(&cfg, []string{"navarisd", "--firecracker-default-vcpu=4", "--firecracker-default-memory-mb=1024"})
	if cfg.firecrackerDefaultVcpu != 4 {
		t.Errorf("explicit vcpu = %d, want 4", cfg.firecrackerDefaultVcpu)
	}
	if cfg.firecrackerDefaultMemoryMB != 1024 {
		t.Errorf("explicit memory = %d, want 1024", cfg.firecrackerDefaultMemoryMB)
	}
}
```

This test relies on helpers `resetFlags` / `parseFlagsInto`. If they don't exist in `storage_test.go` already, replace the test body with a simpler invocation: read the existing flag-test pattern in this file (`grep -n "flag.CommandLine\|os.Args\|parseFlags" cmd/navarisd/`) and follow whatever shape is there. If no flag-parsing helper exists, the simplest approach is to invoke `parseFlags()` from a sub-test that sets `os.Args`. **Inspect `cmd/navarisd/main.go`'s `parseFlags()` function first**: if it reads `os.Args` directly via `flag.Parse()`, isolate the test by saving and restoring `os.Args` and `flag.CommandLine`:

```go
func TestParseFlags_FirecrackerDefaults_Defaults(t *testing.T) {
	oldArgs := os.Args
	oldFlags := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
	}()
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"navarisd"}
	cfg := parseFlags()
	if cfg.firecrackerDefaultVcpu != 1 {
		t.Errorf("default vcpu = %d, want 1", cfg.firecrackerDefaultVcpu)
	}
	if cfg.firecrackerDefaultMemoryMB != 256 {
		t.Errorf("default memory mb = %d, want 256", cfg.firecrackerDefaultMemoryMB)
	}
}

func TestParseFlags_FirecrackerDefaults_Explicit(t *testing.T) {
	oldArgs := os.Args
	oldFlags := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
	}()
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"navarisd", "--firecracker-default-vcpu=4", "--firecracker-default-memory-mb=1024"}
	cfg := parseFlags()
	if cfg.firecrackerDefaultVcpu != 4 {
		t.Errorf("explicit vcpu = %d, want 4", cfg.firecrackerDefaultVcpu)
	}
	if cfg.firecrackerDefaultMemoryMB != 1024 {
		t.Errorf("explicit memory mb = %d, want 1024", cfg.firecrackerDefaultMemoryMB)
	}
}
```

Imports: `"flag"`, `"os"` — likely already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/navarisd/... -run TestParseFlags_FirecrackerDefaults`
Expected: FAIL — `config` has no `firecrackerDefaultVcpu` / `firecrackerDefaultMemoryMB` fields.

- [ ] **Step 3: Add fields to `config` and define the flags**

In `cmd/navarisd/main.go`, inside the `config` struct (look for `type config struct {`), add (place near other firecracker-prefixed fields like `firecrackerBin`):

```go
firecrackerDefaultVcpu     int
firecrackerDefaultMemoryMB int
```

In `parseFlags()`, alongside the existing `flag.StringVar(&cfg.firecrackerBin, ...)` line, add:

```go
flag.IntVar(&cfg.firecrackerDefaultVcpu, "firecracker-default-vcpu", 1, "default vCPU count for Firecracker sandboxes when CPULimit is unset")
flag.IntVar(&cfg.firecrackerDefaultMemoryMB, "firecracker-default-memory-mb", 256, "default memory (MB, treated as MiB inside Firecracker) when MemoryLimitMB is unset")
```

- [ ] **Step 4: Forward into `firecracker.Config`**

In `cmd/navarisd/provider_firecracker.go`, find `firecracker.New(firecracker.Config{...})` and add at the end of the literal (before the closing brace):

```go
DefaultVcpuCount: cfg.firecrackerDefaultVcpu,
DefaultMemoryMib: cfg.firecrackerDefaultMemoryMB,
```

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/navarisd/...`
Expected: PASS.

Run: `go build -tags firecracker ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/navarisd/main.go cmd/navarisd/provider_firecracker.go cmd/navarisd/storage_test.go
git commit -m "$(cat <<'EOF'
feat(navarisd): add --firecracker-default-vcpu and --firecracker-default-memory-mb flags

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: API-layer 400 tests

**Files:**
- Modify: `internal/api/sandbox_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/api/sandbox_test.go`, append:

```go
func TestCreateSandbox_RejectsCPUOutOfBounds(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes", map[string]any{
		"project_id": projectID,
		"name":       "bad-cpu",
		"image_id":   "img-1",
		"cpu_limit":  33,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSandboxFromSnapshot_RejectsMemoryLimit(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/from-snapshot", map[string]any{
		"project_id":      projectID,
		"name":            "bad-snap",
		"snapshot_id":     "snap-1",
		"memory_limit_mb": 1024,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
```

`http` is likely already imported; if not add `"net/http"`.

- [ ] **Step 2: Run tests**

Run: `go test ./internal/api/... -run "RejectsCPU|RejectsMemoryLimit"`
Expected: PASS — Task 3 already wired validation; this test simply confirms the 400 reaches the API surface.

(If you happen to run this before Task 3, it would FAIL because validation isn't wired yet; the dependency is on Task 3 not Task 7.)

- [ ] **Step 3: Commit**

```bash
git add internal/api/sandbox_test.go
git commit -m "$(cat <<'EOF'
test(api): assert 400 for out-of-bounds limits and from-snapshot overrides

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Integration test — guest sees the requested memory

**Files:**
- Create: `test/integration/limits_test.go`

- [ ] **Step 1: Inspect the existing harness**

Read `test/integration/helpers_test.go` and one existing integration test (e.g. `test/integration/snapshot_test.go`) to see:
- The build tag (`//go:build integration`).
- Helper signatures: `newClient()`, `createTestProject(t, c)`, `createTestSandbox(t, c, projectID, name)`, `c.Exec(ctx, sandboxID, client.ExecRequest{...})`.
- The base image env var (`NAVARIS_BASE_IMAGE`, fed in via the compose).

- [ ] **Step 2: Write the test**

`test/integration/limits_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// TestSandbox_HonorsRequestedMemoryLimit creates a sandbox with
// memory_limit_mb=512 and verifies that the guest's MemTotal in
// /proc/meminfo is in a sensible band around 512 MiB. The band
// accounts for kernel + initramfs reserved memory; we assert
// "approximately 512 MiB", not equality.
func TestSandbox_HonorsRequestedMemoryLimit(t *testing.T) {
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandbox(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "limits-mem-512",
		ImageID:       os.Getenv("NAVARIS_BASE_IMAGE"),
		MemoryLimitMB: &mem,
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if _, err := c.WaitForOperation(ctx, op.OperationID, nil); err != nil {
		t.Fatalf("wait create op: %v", err)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandbox(ctx, sandboxID) })

	// Read MemTotal (kB) from /proc/meminfo.
	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "awk '/^MemTotal:/ {print $2}' /proc/meminfo"},
	})
	if err != nil {
		t.Fatalf("exec MemTotal: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec MemTotal exit %d: %s", exec.ExitCode, exec.Stderr)
	}
	memKB, err := strconv.Atoi(strings.TrimSpace(exec.Stdout))
	if err != nil {
		t.Fatalf("parse MemTotal %q: %v", exec.Stdout, err)
	}
	memMiB := memKB / 1024

	// Band: 480..520 MiB. Lower end accounts for kernel + initramfs
	// reservations (typically ~30 MiB on a minimal Alpine guest); upper
	// end leaves a small allowance for measurement variance.
	const lo, hi = 480, 520
	if memMiB < lo || memMiB > hi {
		t.Errorf("guest MemTotal = %d MiB, expected %d..%d MiB", memMiB, lo, hi)
	}
}
```

- [ ] **Step 3: Build sanity-check**

Run: `go vet -tags integration ./test/integration/... && go build -tags integration ./test/integration/...`
Expected: clean.

The test runs only with the `integration` tag and against a live navarisd; you will not run it locally without the docker-compose stack. It WILL run in `integration` and `integration-firecracker` CI legs automatically.

- [ ] **Step 4: Commit**

```bash
git add test/integration/limits_test.go
git commit -m "$(cat <<'EOF'
test(integration): assert sandbox guest sees requested memory_limit_mb

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/native-install.md`
- Modify: `packaging/systemd/navarisd.env.example`
- Modify: `packaging/systemd/navarisd-launch.sh`

- [ ] **Step 1: Update README daemon-flags table**

In `README.md`, find the daemon-flags table (look for `### Daemon flags`). Add two rows alongside the existing firecracker-related rows:

```markdown
| `--firecracker-default-vcpu` | `1` | Default vCPU count for Firecracker sandboxes when `cpu_limit` is unset on the API |
| `--firecracker-default-memory-mb` | `256` | Default memory (MB, treated as MiB) for Firecracker sandboxes when `memory_limit_mb` is unset |
```

Place them after the existing `--enable-jailer` row to keep firecracker-related flags grouped.

- [ ] **Step 2: Update `docs/native-install.md`**

Find the Firecracker env-var block (search for `NAVARIS_KERNEL_PATH`). Add two commented entries:

```bash
# NAVARIS_FIRECRACKER_DEFAULT_VCPU=1
# NAVARIS_FIRECRACKER_DEFAULT_MEMORY_MB=256
```

Add a one-line note above the block: "Sandboxes get these as defaults when the API call doesn't specify `cpu_limit` / `memory_limit_mb`."

- [ ] **Step 3: Update `packaging/systemd/navarisd.env.example`**

Add to the Firecracker section:

```bash
# NAVARIS_FIRECRACKER_DEFAULT_VCPU=1
# NAVARIS_FIRECRACKER_DEFAULT_MEMORY_MB=256
```

- [ ] **Step 4: Update `packaging/systemd/navarisd-launch.sh`**

Find the existing `add_string_flag --firecracker-bin` lines. Add two more `add_int_flag` (or `add_string_flag`, whichever pattern the file uses for non-string flags — inspect first):

```bash
add_string_flag --firecracker-default-vcpu "${NAVARIS_FIRECRACKER_DEFAULT_VCPU:-}"
add_string_flag --firecracker-default-memory-mb "${NAVARIS_FIRECRACKER_DEFAULT_MEMORY_MB:-}"
```

(`add_string_flag` works because Go's flag package accepts `--foo=4` for int flags. If the launcher script has a dedicated `add_int_flag` helper, prefer that.)

- [ ] **Step 5: Sanity-check builds**

Run: `go build ./... && go build -tags firecracker ./...`
Expected: clean (no code changes beyond docs/scripts).

- [ ] **Step 6: Commit**

```bash
git add README.md docs/native-install.md packaging/systemd/navarisd.env.example packaging/systemd/navarisd-launch.sh
git commit -m "$(cat <<'EOF'
docs: surface --firecracker-default-vcpu and --firecracker-default-memory-mb

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review Checklist (run after the plan is written)

- [x] **Spec §1 (Goals)** — wire-up at `MachineCfg` → Task 5; service-layer validation → Tasks 2–3.
- [x] **Spec §3.1 (ErrInvalidArgument)** → Task 1.
- [x] **Spec §3.2 (validation rules + bounds + from-snapshot rejection)** → Tasks 2–3.
- [x] **Spec §3.3 (daemon flags)** → Task 6.
- [x] **Spec §3.4 (Firecracker provider wire-up)** → Tasks 4 and 5.
- [x] **Spec §3.5 (memory-unit semantics, no conversion)** — covered by no-op in Task 5 (we feed the bare number to `MemSizeMib`); documented in Task 9 and in code comments in Task 4.
- [x] **Spec §3.6 (Incus side: nothing changes)** — explicitly nothing in this plan touches the Incus provider. Confirmed in self-review.
- [x] **Spec §3.7 (fork inheritance, no-op)** — explicitly nothing changes; the Fork code path bypasses `validateLimits` deliberately. No task needed.
- [x] **Spec §4 (testing)** — Tasks 2 (unit), 3 (service), 4 (config defaults), 5 (resolveMachineLimits), 6 (flag parsing), 7 (API 400), 8 (integration).
- [x] **Spec §5 (rollout / behavior changes)** — captured by the defaults in Tasks 4 and 6 preserving status quo. Release-note caveats are documentation, not code; mention in Task 9 commit message body if desired.
- [x] **Spec §6 (documentation)** → Task 9.
- [x] **Spec §7 (open questions)** — risks are captured in the spec; no plan tasks needed (revisit if real-world feedback arrives).
- [x] **Placeholder scan**: no "TBD"/"TODO" in any code block. Helper-existence checks (`grep -n "func ptrInt"`, `parseFlags` flag-test pattern) are concrete instructions, not placeholders.
- [x] **Type consistency**: `validateLimits(opts CreateSandboxOpts, fromSnapshot bool) error` is identical in Tasks 2 and 3. `Config.DefaultVcpuCount` / `Config.DefaultMemoryMib` consistent in Tasks 4–6. `resolveMachineLimits(req) (vcpu, mem int64)` consistent in Task 5. `firecrackerDefaultVcpu` / `firecrackerDefaultMemoryMB` consistent in Task 6 (config field + flag binding + provider construction).

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-25-resource-limits.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best for a 9-task plan with mostly-mechanical work.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch with checkpoints.

Which approach?
