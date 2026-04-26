# Runtime Resource Resize — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `PATCH /sandboxes/{id}/resources` and Firecracker boot-time headroom so callers can resize CPU/memory on running sandboxes (memory both backends; CPU on Incus live + on Firecracker stopped-only).

**Architecture:** Sync PATCH at the API → service-layer `UpdateResources` validates and persists → provider-layer `UpdateResources` applies live (Incus: `incus config set`; Firecracker: balloon `PATCH`). Boot-time headroom on FC: every new VM boots at `limit × multiplier`, with the balloon inflated by the difference so the guest sees the user's `limit` until a resize deflates the balloon.

**Tech Stack:** Go, sqlite, Firecracker (firecracker-go-sdk v1.0.0 — `PatchBalloon`), Incus CLI, OpenTelemetry events, existing WebSocket event bus.

**Spec:** [docs/superpowers/specs/2026-04-25-runtime-resource-resize-design.md](../specs/2026-04-25-runtime-resource-resize-design.md)

---

## File Plan

### Created
- `internal/service/sandbox_resize.go` — `UpdateResources` method on `SandboxService`
- `internal/service/sandbox_resize_test.go` — service-layer tests
- `internal/api/sandbox_resize_test.go` — API-layer tests
- `internal/provider/firecracker/sandbox_resize.go` — `UpdateResources` method (FC), balloon helper
- `internal/provider/firecracker/sandbox_resize_test.go` — FC unit tests
- `internal/provider/incus/sandbox_resize.go` — `UpdateResources` method (Incus)
- `internal/provider/incus/sandbox_resize_test.go` — Incus unit tests

### Modified
- `internal/domain/provider.go` — `UpdateResourcesRequest`, `ProviderResizeError`, interface method
- `internal/domain/event.go` — new `EventSandboxResourcesUpdated`
- `internal/service/limits.go` — extract `validateResourceBounds`
- `internal/api/sandbox.go` — handler + request/response types
- `internal/api/server.go` — register PATCH route
- `internal/api/response.go` — map `*ProviderResizeError` to 409
- `internal/provider/firecracker/firecracker.go` — `VcpuHeadroomMult` / `MemHeadroomMult` config + validation
- `internal/provider/firecracker/sandbox.go` — boot with ceiling, configure balloon, populate new VMInfo fields
- `internal/provider/firecracker/vminfo.go` — `LimitCPU`, `LimitMemMib`, `CeilingCPU`, `CeilingMemMib` fields
- `internal/provider/incus/sandbox.go` — (no edit; new method goes in `sandbox_resize.go`)
- `cmd/navarisd/main.go` — `--firecracker-vcpu-headroom-mult`, `--firecracker-mem-headroom-mult` flags
- `cmd/navaris/sandbox.go` — `navaris sandbox resize` subcommand
- `pkg/client/sandbox.go` — `UpdateResources` client method

---

## Conventions

- All work is on a fresh feature branch off `main`.
- Each task ends with a commit. Commit messages use the existing `feat:` / `test:` / `refactor:` prefixes seen in `git log`.
- Build tags: Firecracker code is gated by `//go:build firecracker`; Incus by `//go:build incus`. Mock-only paths build without tags.
- Tests: run `go test ./...` for non-tagged tests; `go test -tags firecracker ./...` and `go test -tags incus ./...` for backend tests.

---

## Task 1: Add domain types for resize

**Files:**
- Modify: `internal/domain/provider.go`
- Modify: `internal/domain/event.go`

- [ ] **Step 1: Add `UpdateResourcesRequest` and `ProviderResizeError` to `internal/domain/provider.go`**

Append to the file (after `PublishedEndpoint`):

```go
// UpdateResourcesRequest carries the desired new CPU/memory limits for a
// running sandbox. A nil pointer means "leave unchanged". The service layer
// rejects requests where both fields are nil.
type UpdateResourcesRequest struct {
	CPULimit      *int
	MemoryLimitMB *int
}

// ProviderResizeError is returned from Provider.UpdateResources when the
// backend cannot apply the requested change live. The Reason is a stable,
// machine-readable code; Detail is a human-readable supplement.
type ProviderResizeError struct {
	Reason string
	Detail string
}

func (e *ProviderResizeError) Error() string {
	if e.Detail != "" {
		return e.Reason + ": " + e.Detail
	}
	return e.Reason
}

const (
	ResizeReasonExceedsCeiling          = "exceeds_ceiling"
	ResizeReasonCPUUnsupportedByBackend = "cpu_resize_unsupported_by_backend"
	ResizeReasonBackendRejected         = "backend_rejected"
)
```

Add `UpdateResources` to the `Provider` interface (place after `ForkSandbox`):

```go
	// UpdateResources applies new CPU/memory limits to a running sandbox.
	// Returns *ProviderResizeError when the backend cannot apply the change
	// live (the service layer maps that to HTTP 409). Other errors are
	// treated as backend failures.
	UpdateResources(ctx context.Context, ref BackendRef, req UpdateResourcesRequest) error
```

- [ ] **Step 2: Add the new event type to `internal/domain/event.go`**

In the `EventType` block, after `EventSandboxStateChanged`:

```go
	EventSandboxResourcesUpdated EventType = "sandbox_resources_updated"
```

- [ ] **Step 3: Build to confirm everything still compiles**

Run: `go build ./...`
Expected: success. (Implementations of `Provider` will fail to build until task 2.)

If implementations break the build before task 2, that's expected and acceptable for this single step — we'll add stubs in task 2.

- [ ] **Step 4: Commit**

```bash
git add internal/domain/provider.go internal/domain/event.go
git commit -m "feat(domain): add UpdateResources interface, ProviderResizeError, event type"
```

---

## Task 2: Stub `UpdateResources` on every existing Provider implementation

The interface gained a method in task 1; every concrete `Provider` (Incus, Firecracker, mock) must implement it or the build breaks. We stub them all to return `ErrNotSupported`, then fill them in later tasks.

**Files:**
- Modify: `internal/provider/incus/sandbox.go` (or new `sandbox_resize.go`)
- Modify: `internal/provider/firecracker/sandbox.go` (or new `sandbox_resize.go`)
- Modify: any in-tree mock `Provider` (search before stubbing)

- [ ] **Step 1: Find all `Provider` implementations**

Run: `grep -rn "domain.Provider = (\*" internal/ pkg/ test/`
Expected output includes the Incus and Firecracker providers, plus any test mocks.

Also: `grep -rn "func (.*) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest)" internal/ pkg/ test/` to catch implementations that don't use the static-assertion pattern.

- [ ] **Step 2: Add stub to Incus provider**

Create `internal/provider/incus/sandbox_resize.go`:

```go
//go:build incus

package incus

import (
	"context"

	"github.com/navaris/navaris/internal/domain"
)

// UpdateResources is implemented in task 9.
func (p *IncusProvider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	return domain.ErrNotSupported
}
```

- [ ] **Step 3: Add stub to Firecracker provider**

Create `internal/provider/firecracker/sandbox_resize.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"

	"github.com/navaris/navaris/internal/domain"
)

// UpdateResources is implemented in task 8.
func (p *Provider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	return domain.ErrNotSupported
}
```

- [ ] **Step 4: Update any test mock provider**

For each mock found in step 1 (e.g. `internal/service/sandbox_test.go` may contain a `mockProvider`), add:

```go
func (m *mockProvider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	if m.updateResourcesFn != nil {
		return m.updateResourcesFn(ctx, ref, req)
	}
	return nil
}
```

…and add the matching `updateResourcesFn func(...)` field to the mock struct. Do this for every mock found.

- [ ] **Step 5: Build everything (all build tags)**

```bash
go build ./...
go build -tags firecracker ./...
go build -tags incus ./...
go build -tags 'incus firecracker' ./...
```

Expected: all four succeed.

- [ ] **Step 6: Commit**

```bash
git add -A internal/provider internal/service
git commit -m "feat(provider): stub UpdateResources on all Provider implementations"
```

---

## Task 3: Extract `validateResourceBounds` helper

The existing `validateLimits(opts CreateSandboxOpts, backend string)` will be reused by the resize path, but the resize path doesn't have a `CreateSandboxOpts`. We extract the bounds check into a smaller helper that takes the two raw fields.

**Files:**
- Modify: `internal/service/limits.go`
- Modify: `internal/service/limits_test.go`

- [ ] **Step 1: Add a failing test case to `internal/service/limits_test.go`**

Add a new test function under the existing tests:

```go
func TestValidateResourceBounds(t *testing.T) {
	cases := []struct {
		name       string
		cpu        *int
		mem        *int
		backend    string
		wantErr    bool
		wantSubstr string
	}{
		{name: "fc cpu out of range", cpu: ptrInt(33), mem: nil, backend: "firecracker", wantErr: true, wantSubstr: "cpu_limit"},
		{name: "fc mem ok", cpu: nil, mem: ptrInt(512), backend: "firecracker", wantErr: false},
		{name: "incus generic bounds", cpu: ptrInt(257), mem: nil, backend: "incus", wantErr: true, wantSubstr: "cpu_limit"},
		{name: "both nil ok at this level", cpu: nil, mem: nil, backend: "firecracker", wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateResourceBounds(tc.cpu, tc.mem, tc.backend)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSubstr)
				}
				if !errors.Is(err, domain.ErrInvalidArgument) {
					t.Fatalf("error does not wrap ErrInvalidArgument: %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
```

Add the imports `"errors"`, `"strings"` if not already present in the test file.

- [ ] **Step 2: Run the test, expect compile error**

Run: `go test ./internal/service/ -run TestValidateResourceBounds`
Expected: FAIL — `validateResourceBounds` undefined.

- [ ] **Step 3: Implement `validateResourceBounds` in `internal/service/limits.go`**

Replace the body of `validateLimits` and add the new helper. Final shape:

```go
// validateResourceBounds checks raw CPU / memory pointers against
// backend-specific bounds. Wraps domain.ErrInvalidArgument so the API maps
// to 400. Nil pointers are allowed and treated as "unchanged".
func validateResourceBounds(cpu *int, mem *int, backend string) error {
	minCPU, maxCPU, minMem, maxMem := limitGenericMinCPU, limitGenericMaxCPU, limitGenericMinMemMB, limitGenericMaxMemMB
	if backend == backendFirecracker {
		minCPU, maxCPU, minMem, maxMem = limitFCMinCPU, limitFCMaxCPU, limitFCMinMemMB, limitFCMaxMemMB
	}
	if cpu != nil {
		v := *cpu
		if v < minCPU || v > maxCPU {
			return fmt.Errorf("cpu_limit must be %d..%d for backend %q, got %d: %w", minCPU, maxCPU, backend, v, domain.ErrInvalidArgument)
		}
	}
	if mem != nil {
		v := *mem
		if v < minMem || v > maxMem {
			return fmt.Errorf("memory_limit_mb must be %d..%d for backend %q, got %d: %w", minMem, maxMem, backend, v, domain.ErrInvalidArgument)
		}
	}
	return nil
}

// validateLimits is the create-time bounds check; preserved for callers in
// service/sandbox.go.
func validateLimits(opts CreateSandboxOpts, backend string) error {
	return validateResourceBounds(opts.CPULimit, opts.MemoryLimitMB, backend)
}
```

- [ ] **Step 4: Run the new test**

Run: `go test ./internal/service/ -run TestValidateResourceBounds -v`
Expected: PASS for all four cases.

- [ ] **Step 5: Run the full service-test suite to make sure existing tests still pass**

Run: `go test ./internal/service/ -v`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/service/limits.go internal/service/limits_test.go
git commit -m "refactor(service): extract validateResourceBounds for reuse by resize"
```

---

## Task 4: Service-layer `UpdateResources` — happy path on stopped sandbox

We start with the easiest case: stopped sandbox. SQLite is updated, the provider is **not** called, `applied_live=false` is returned, and the event is emitted.

**Files:**
- Create: `internal/service/sandbox_resize.go`
- Create: `internal/service/sandbox_resize_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/service/sandbox_resize_test.go`:

```go
package service

import (
	"context"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestUpdateResources_StoppedSandbox(t *testing.T) {
	ctx := context.Background()
	svc, deps := newTestSandboxService(t) // existing helper used elsewhere; if missing, see Task 4 note
	sbx := deps.seedSandbox(t, "sbx-1", domain.SandboxStopped, "incus")

	cpu := 4
	mem := 1024
	res, err := svc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      &cpu,
		MemoryLimitMB: &mem,
	})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}
	if res.AppliedLive {
		t.Fatalf("AppliedLive=true on stopped sandbox; want false")
	}
	if got := *res.Sandbox.CPULimit; got != 4 {
		t.Fatalf("CPULimit = %d, want 4", got)
	}
	if got := *res.Sandbox.MemoryLimitMB; got != 1024 {
		t.Fatalf("MemoryLimitMB = %d, want 1024", got)
	}

	// Provider must NOT have been called.
	if deps.mockProv.updateResourcesCalls != 0 {
		t.Fatalf("provider.UpdateResources called %d times; want 0", deps.mockProv.updateResourcesCalls)
	}

	// Event must have been emitted.
	if got := deps.eventBus.countOf(domain.EventSandboxResourcesUpdated); got != 1 {
		t.Fatalf("EventSandboxResourcesUpdated count = %d; want 1", got)
	}
}
```

> **Note:** `newTestSandboxService` and `deps.seedSandbox` are conventional patterns. If the service test file uses a different setup (`setupTest`, `newServiceWithMocks`, etc.), adapt to match. Search `internal/service/sandbox_test.go` for the pattern and reuse it. The test must (a) construct a real `SandboxService` with sqlite in-memory and a mock provider, (b) seed one sandbox row in the chosen state, (c) capture events for assertion. If no such helper exists, add one in this task — but keep it small.

- [ ] **Step 2: Run the test, confirm it fails**

Run: `go test ./internal/service/ -run TestUpdateResources_StoppedSandbox`
Expected: FAIL — `UpdateResources` undefined.

- [ ] **Step 3: Create `internal/service/sandbox_resize.go`**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

// UpdateResourcesOpts describes a CPU / memory resize of an existing sandbox.
type UpdateResourcesOpts struct {
	SandboxID     string
	CPULimit      *int
	MemoryLimitMB *int
}

// UpdateResourcesResult is what UpdateResources returns on success.
type UpdateResourcesResult struct {
	Sandbox     *domain.Sandbox
	AppliedLive bool
}

// UpdateResources applies new CPU / memory limits to an existing sandbox.
//
// If the sandbox is running, the provider is asked to apply the change live.
// If stopped, only the persisted limits are updated; they take effect on
// next start. Errors:
//   - ErrInvalidArgument: both fields nil, or bounds violation
//   - ErrNotFound: no such sandbox
//   - ErrInvalidState: sandbox is destroyed/failed
//   - *ProviderResizeError: backend rejected the live resize
func (s *SandboxService) UpdateResources(ctx context.Context, opts UpdateResourcesOpts) (*UpdateResourcesResult, error) {
	if opts.CPULimit == nil && opts.MemoryLimitMB == nil {
		return nil, fmt.Errorf("at least one of cpu_limit, memory_limit_mb must be supplied: %w", domain.ErrInvalidArgument)
	}

	sbx, err := s.sandboxes.Get(ctx, opts.SandboxID)
	if err != nil {
		return nil, err
	}

	if sbx.State == domain.SandboxDestroyed || sbx.State == domain.SandboxFailed {
		return nil, fmt.Errorf("cannot resize sandbox in state %s: %w", sbx.State, domain.ErrInvalidState)
	}

	if err := validateResourceBounds(opts.CPULimit, opts.MemoryLimitMB, sbx.Backend); err != nil {
		return nil, err
	}

	prevCPU := sbx.CPULimit
	prevMem := sbx.MemoryLimitMB

	if opts.CPULimit != nil {
		v := *opts.CPULimit
		sbx.CPULimit = &v
	}
	if opts.MemoryLimitMB != nil {
		v := *opts.MemoryLimitMB
		sbx.MemoryLimitMB = &v
	}
	sbx.UpdatedAt = time.Now().UTC()

	if err := s.sandboxes.Update(ctx, sbx); err != nil {
		return nil, fmt.Errorf("persist resize: %w", err)
	}

	appliedLive := false
	if sbx.State == domain.SandboxRunning {
		req := domain.UpdateResourcesRequest{
			CPULimit:      opts.CPULimit,
			MemoryLimitMB: opts.MemoryLimitMB,
		}
		if err := s.provider.UpdateResources(ctx, domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}, req); err != nil {
			// Roll back persisted limits on provider failure so DB state stays
			// consistent with the running VM.
			sbx.CPULimit = prevCPU
			sbx.MemoryLimitMB = prevMem
			if rbErr := s.sandboxes.Update(ctx, sbx); rbErr != nil {
				return nil, fmt.Errorf("provider resize failed: %v; rollback also failed: %w", err, rbErr)
			}
			var prErr *domain.ProviderResizeError
			if errors.As(err, &prErr) {
				return nil, prErr
			}
			return nil, err
		}
		appliedLive = true
	}

	_ = s.events.Publish(ctx, domain.Event{
		Type:      domain.EventSandboxResourcesUpdated,
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"sandbox_id":      sbx.SandboxID,
			"cpu_limit":       sbx.CPULimit,
			"memory_limit_mb": sbx.MemoryLimitMB,
			"applied_live":    appliedLive,
		},
	})

	return &UpdateResourcesResult{Sandbox: sbx, AppliedLive: appliedLive}, nil
}
```

- [ ] **Step 4: Run the test, confirm it passes**

Run: `go test ./internal/service/ -run TestUpdateResources_StoppedSandbox -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/sandbox_resize.go internal/service/sandbox_resize_test.go
git commit -m "feat(service): UpdateResources for stopped sandboxes"
```

---

## Task 5: Service-layer `UpdateResources` — running sandbox + error paths

Layer in the running-sandbox path, the rollback on provider error, and the validation/state error paths. Each is a distinct test.

**Files:**
- Modify: `internal/service/sandbox_resize_test.go`

- [ ] **Step 1: Add failing tests for the remaining paths**

Append to `internal/service/sandbox_resize_test.go`:

```go
func TestUpdateResources_RunningSandbox_AppliesLive(t *testing.T) {
	ctx := context.Background()
	svc, deps := newTestSandboxService(t)
	sbx := deps.seedSandbox(t, "sbx-2", domain.SandboxRunning, "incus")

	cpu := 2
	res, err := svc.UpdateResources(ctx, UpdateResourcesOpts{SandboxID: sbx.SandboxID, CPULimit: &cpu})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}
	if !res.AppliedLive {
		t.Fatalf("AppliedLive=false on running sandbox; want true")
	}
	if deps.mockProv.updateResourcesCalls != 1 {
		t.Fatalf("provider.UpdateResources calls = %d; want 1", deps.mockProv.updateResourcesCalls)
	}
}

func TestUpdateResources_BothFieldsNil(t *testing.T) {
	ctx := context.Background()
	svc, deps := newTestSandboxService(t)
	sbx := deps.seedSandbox(t, "sbx-3", domain.SandboxStopped, "incus")

	_, err := svc.UpdateResources(ctx, UpdateResourcesOpts{SandboxID: sbx.SandboxID})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestUpdateResources_DestroyedSandbox(t *testing.T) {
	ctx := context.Background()
	svc, deps := newTestSandboxService(t)
	sbx := deps.seedSandbox(t, "sbx-4", domain.SandboxDestroyed, "incus")

	cpu := 2
	_, err := svc.UpdateResources(ctx, UpdateResourcesOpts{SandboxID: sbx.SandboxID, CPULimit: &cpu})
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
}

func TestUpdateResources_BoundsViolation(t *testing.T) {
	ctx := context.Background()
	svc, deps := newTestSandboxService(t)
	sbx := deps.seedSandbox(t, "sbx-5", domain.SandboxStopped, "firecracker")

	cpu := 99
	_, err := svc.UpdateResources(ctx, UpdateResourcesOpts{SandboxID: sbx.SandboxID, CPULimit: &cpu})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestUpdateResources_ProviderError_RollsBack(t *testing.T) {
	ctx := context.Background()
	svc, deps := newTestSandboxService(t)
	sbx := deps.seedSandbox(t, "sbx-6", domain.SandboxRunning, "firecracker")
	origCPU := *sbx.CPULimit

	wantErr := &domain.ProviderResizeError{Reason: domain.ResizeReasonExceedsCeiling, Detail: "test"}
	deps.mockProv.updateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		return wantErr
	}

	cpu := 2
	_, err := svc.UpdateResources(ctx, UpdateResourcesOpts{SandboxID: sbx.SandboxID, CPULimit: &cpu})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("err = %v, want *ProviderResizeError", err)
	}

	// SQLite must show the original value, not the requested 2.
	got, _ := deps.sbxStore.Get(ctx, sbx.SandboxID)
	if *got.CPULimit != origCPU {
		t.Fatalf("CPULimit after rollback = %d; want %d (original)", *got.CPULimit, origCPU)
	}
}
```

Add `"errors"` to the test file imports if missing.

- [ ] **Step 2: Run the new tests, confirm they pass**

Run: `go test ./internal/service/ -run TestUpdateResources -v`
Expected: all five pass (one from task 4 plus four new). If `TestUpdateResources_ProviderError_RollsBack` fails, double-check the rollback `sandboxes.Update` in `sandbox_resize.go`.

- [ ] **Step 3: Commit**

```bash
git add internal/service/sandbox_resize_test.go
git commit -m "test(service): cover running, error, and rollback paths for UpdateResources"
```

---

## Task 6: API — PATCH endpoint + handler + error mapping

**Files:**
- Modify: `internal/api/sandbox.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/response.go`
- Create: `internal/api/sandbox_resize_test.go`

- [ ] **Step 1: Add the failing API test**

Create `internal/api/sandbox_resize_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

func TestPatchResources_OK(t *testing.T) {
	srv, deps := newTestAPIServer(t) // existing helper in api package
	deps.seedSandbox(t, "sbx-1", domain.SandboxStopped, "incus")

	body := strings.NewReader(`{"cpu_limit": 4, "memory_limit_mb": 1024}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/sandboxes/sbx-1/resources", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got struct {
		SandboxID     string `json:"sandbox_id"`
		CPULimit      int    `json:"cpu_limit"`
		MemoryLimitMB int    `json:"memory_limit_mb"`
		AppliedLive   bool   `json:"applied_live"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.CPULimit != 4 || got.MemoryLimitMB != 1024 || got.AppliedLive {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestPatchResources_BothFieldsOmitted_400(t *testing.T) {
	srv, deps := newTestAPIServer(t)
	deps.seedSandbox(t, "sbx-1", domain.SandboxStopped, "incus")

	req := httptest.NewRequest(http.MethodPatch, "/v1/sandboxes/sbx-1/resources", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestPatchResources_NotFound_404(t *testing.T) {
	srv, _ := newTestAPIServer(t)

	req := httptest.NewRequest(http.MethodPatch, "/v1/sandboxes/missing/resources", strings.NewReader(`{"cpu_limit": 4}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestPatchResources_ProviderResizeError_409(t *testing.T) {
	srv, deps := newTestAPIServer(t)
	deps.seedSandbox(t, "sbx-1", domain.SandboxRunning, "firecracker")
	deps.mockProv.updateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{Reason: domain.ResizeReasonExceedsCeiling, Detail: "memory_limit_mb 4096 > ceiling 2048"}
	}

	body := bytes.NewBufferString(`{"memory_limit_mb": 4096}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/sandboxes/sbx-1/resources", body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "exceeds_ceiling") {
		t.Fatalf("body missing reason: %s", rr.Body.String())
	}
}

func TestPatchResources_BoundsViolation_400(t *testing.T) {
	srv, deps := newTestAPIServer(t)
	deps.seedSandbox(t, "sbx-1", domain.SandboxStopped, "firecracker")

	body := bytes.NewBufferString(`{"cpu_limit": 99}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/sandboxes/sbx-1/resources", body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}

	// Sanity: must wrap ErrInvalidArgument upstream.
	var resp errorResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !strings.Contains(resp.Error.Message, "cpu_limit") {
		t.Fatalf("body missing field name: %s", rr.Body.String())
	}
}
```

> **Note:** `newTestAPIServer` follows the same convention as the service test helper. If it doesn't exist by that name, search `internal/api/sandbox_test.go` and `internal/api/helpers_test.go` for the existing setup helper and reuse / extend it.

- [ ] **Step 2: Run, expect failures**

Run: `go test ./internal/api/ -run TestPatchResources -v`
Expected: all five FAIL (route not registered → 404 for everything, or compile error).

- [ ] **Step 3: Add request/response types and handler in `internal/api/sandbox.go`**

Append to the request types block:

```go
type updateResourcesRequest struct {
	CPULimit      *int `json:"cpu_limit"`
	MemoryLimitMB *int `json:"memory_limit_mb"`
}

type updateResourcesResponse struct {
	SandboxID     string `json:"sandbox_id"`
	CPULimit      *int   `json:"cpu_limit"`
	MemoryLimitMB *int   `json:"memory_limit_mb"`
	AppliedLive   bool   `json:"applied_live"`
}
```

Append the handler (e.g. after `forkSandbox`):

```go
func (s *Server) updateSandboxResources(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing sandbox id", http.StatusBadRequest)
		return
	}
	var req updateResourcesRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.CPULimit == nil && req.MemoryLimitMB == nil {
		http.Error(w, "at least one of cpu_limit, memory_limit_mb is required", http.StatusBadRequest)
		return
	}

	res, err := s.cfg.Sandboxes.UpdateResources(r.Context(), service.UpdateResourcesOpts{
		SandboxID:     id,
		CPULimit:      req.CPULimit,
		MemoryLimitMB: req.MemoryLimitMB,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondData(w, http.StatusOK, updateResourcesResponse{
		SandboxID:     res.Sandbox.SandboxID,
		CPULimit:      res.Sandbox.CPULimit,
		MemoryLimitMB: res.Sandbox.MemoryLimitMB,
		AppliedLive:   res.AppliedLive,
	})
}
```

- [ ] **Step 4: Register the route in `internal/api/server.go`**

Add this line in the same block as the other sandbox routes (e.g. just after `POST /v1/sandboxes/{id}/fork`):

```go
	api.HandleFunc("PATCH /v1/sandboxes/{id}/resources", s.updateSandboxResources)
```

- [ ] **Step 5: Map `*ProviderResizeError` to 409 in `internal/api/response.go`**

In `mapErrorCode`, before the default `return http.StatusInternalServerError`:

```go
	var prErr *domain.ProviderResizeError
	if errors.As(err, &prErr) {
		return http.StatusConflict
	}
```

- [ ] **Step 6: Run the API tests**

Run: `go test ./internal/api/ -run TestPatchResources -v`
Expected: all five PASS.

- [ ] **Step 7: Run the whole API test suite**

Run: `go test ./internal/api/`
Expected: all green.

- [ ] **Step 8: Commit**

```bash
git add internal/api/sandbox.go internal/api/server.go internal/api/response.go internal/api/sandbox_resize_test.go
git commit -m "feat(api): PATCH /sandboxes/{id}/resources"
```

---

## Task 7: Firecracker — headroom config flags + VMInfo fields

This task adds the Firecracker boot-time wiring. Pure additive: new config fields, new VMInfo fields, no behavior change yet (boot path still uses `limit` directly). Behavior changes in task 8.

**Files:**
- Modify: `internal/provider/firecracker/firecracker.go`
- Modify: `internal/provider/firecracker/vminfo.go`
- Modify: `internal/provider/firecracker/firecracker_test.go`

- [ ] **Step 1: Add headroom fields and validation to `Config`**

In `internal/provider/firecracker/firecracker.go`, append to `Config`:

```go
	// VcpuHeadroomMult controls boot-time vCPU headroom: every new VM boots
	// with vcpu_count = ceil(limit * VcpuHeadroomMult). Must be >= 1.0.
	// Default 2.0; set to 1.0 to disable headroom (boot at exact limit).
	// Set via --firecracker-vcpu-headroom-mult.
	VcpuHeadroomMult float64

	// MemHeadroomMult controls boot-time memory headroom analogously.
	// Memory above the user's limit is reclaimed via a balloon device
	// inflated to (ceiling - limit) MiB at boot.
	MemHeadroomMult float64
```

In `(c *Config) defaults()`:

```go
	if c.VcpuHeadroomMult == 0 {
		c.VcpuHeadroomMult = 2.0
	}
	if c.MemHeadroomMult == 0 {
		c.MemHeadroomMult = 2.0
	}
```

In `(c *Config) validateDefaults()`, append:

```go
	if c.VcpuHeadroomMult < 1.0 {
		return fmt.Errorf("firecracker-vcpu-headroom-mult=%g must be >= 1.0", c.VcpuHeadroomMult)
	}
	if c.MemHeadroomMult < 1.0 {
		return fmt.Errorf("firecracker-mem-headroom-mult=%g must be >= 1.0", c.MemHeadroomMult)
	}
```

- [ ] **Step 2: Add tests for the new validation**

In `internal/provider/firecracker/firecracker_test.go` (or sibling `_test.go`), add:

```go
func TestValidateDefaults_HeadroomMultipliers(t *testing.T) {
	cases := []struct {
		name        string
		vcpuMult    float64
		memMult     float64
		wantErrPart string
	}{
		{"vcpu below 1.0", 0.5, 2.0, "vcpu-headroom-mult"},
		{"mem below 1.0", 2.0, 0.5, "mem-headroom-mult"},
		{"both at 1.0 ok", 1.0, 1.0, ""},
		{"both at 4.0 ok", 4.0, 4.0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				DefaultVcpuCount:  1,
				DefaultMemoryMib:  256,
				VcpuHeadroomMult:  tc.vcpuMult,
				MemHeadroomMult:   tc.memMult,
			}
			err := c.validateDefaults()
			if tc.wantErrPart == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErrPart)
			}
		})
	}
}
```

Add `"strings"` import if missing.

- [ ] **Step 3: Add new fields to `VMInfo`**

In `internal/provider/firecracker/vminfo.go`, add to the struct:

```go
	LimitCPU      int64 `json:"limit_cpu,omitempty"`
	LimitMemMib   int64 `json:"limit_mem_mib,omitempty"`
	CeilingCPU    int64 `json:"ceiling_cpu,omitempty"`
	CeilingMemMib int64 `json:"ceiling_mem_mib,omitempty"`
```

(Keep `VcpuCount` and `MemSizeMib` — they continue to record the actual booted machine size.)

- [ ] **Step 4: Run the new tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -run TestValidateDefaults_HeadroomMultipliers -v
```

Expected: PASS for all four cases.

- [ ] **Step 5: Run the full FC test suite to confirm no regressions**

```bash
go test -tags firecracker ./internal/provider/firecracker/
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/firecracker/firecracker.go internal/provider/firecracker/vminfo.go internal/provider/firecracker/firecracker_test.go
git commit -m "feat(firecracker): add VcpuHeadroomMult/MemHeadroomMult config and VMInfo fields"
```

---

## Task 8: Firecracker — boot with ceiling + balloon device

Now wire `resolveMachineLimits` to compute and persist the ceiling, and add the balloon device to the FC config so the guest sees the user's `limit` despite booting at the ceiling.

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go`
- Modify: `internal/provider/firecracker/storage_wiring_test.go` (existing test references resolveMachineLimits)
- Create: `internal/provider/firecracker/headroom_test.go`

- [ ] **Step 1: Write a failing test for the new resolveMachineLimits shape**

Create `internal/provider/firecracker/headroom_test.go`:

```go
//go:build firecracker

package firecracker

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestResolveMachineLimits_AppliesHeadroom(t *testing.T) {
	cases := []struct {
		name           string
		vcpuMult       float64
		memMult        float64
		req            domain.CreateSandboxRequest
		wantLimitCPU   int64
		wantLimitMem   int64
		wantCeilingCPU int64
		wantCeilingMem int64
	}{
		{
			name:           "default 2x headroom",
			vcpuMult:       2.0,
			memMult:        2.0,
			req:            domain.CreateSandboxRequest{CPULimit: ptrInt(2), MemoryLimitMB: ptrInt(256)},
			wantLimitCPU:   2,
			wantLimitMem:   256,
			wantCeilingCPU: 4,
			wantCeilingMem: 512,
		},
		{
			name:           "1x headroom = no headroom",
			vcpuMult:       1.0,
			memMult:        1.0,
			req:            domain.CreateSandboxRequest{CPULimit: ptrInt(2), MemoryLimitMB: ptrInt(256)},
			wantLimitCPU:   2,
			wantLimitMem:   256,
			wantCeilingCPU: 2,
			wantCeilingMem: 256,
		},
		{
			name:           "ceiling clamped to FC max (32 vcpu, 8192 mem)",
			vcpuMult:       4.0,
			memMult:        4.0,
			req:            domain.CreateSandboxRequest{CPULimit: ptrInt(16), MemoryLimitMB: ptrInt(4096)},
			wantLimitCPU:   16,
			wantLimitMem:   4096,
			wantCeilingCPU: 32,
			wantCeilingMem: 8192,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{config: Config{
				DefaultVcpuCount: 1, DefaultMemoryMib: 256,
				VcpuHeadroomMult: tc.vcpuMult, MemHeadroomMult: tc.memMult,
			}}
			got := p.resolveMachineLimits(tc.req)
			if got.LimitCPU != tc.wantLimitCPU || got.LimitMemMib != tc.wantLimitMem || got.CeilingCPU != tc.wantCeilingCPU || got.CeilingMemMib != tc.wantCeilingMem {
				t.Fatalf("got %+v, want LimitCPU=%d LimitMem=%d CeilingCPU=%d CeilingMem=%d",
					got, tc.wantLimitCPU, tc.wantLimitMem, tc.wantCeilingCPU, tc.wantCeilingMem)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }
```

- [ ] **Step 2: Run the test, confirm failure**

Run: `go test -tags firecracker ./internal/provider/firecracker/ -run TestResolveMachineLimits_AppliesHeadroom`
Expected: FAIL — `resolveMachineLimits` still returns `(vcpu, mem int64)`, not a struct.

- [ ] **Step 3: Refactor `resolveMachineLimits`**

In `internal/provider/firecracker/sandbox.go`, replace the existing function:

```go
type resolvedLimits struct {
	LimitCPU      int64
	LimitMemMib   int64
	CeilingCPU    int64
	CeilingMemMib int64
}

// resolveMachineLimits computes the user-facing CPU/memory limit and the
// boot-time ceiling for a new VM. The VM boots with vcpu_count =
// CeilingCPU and mem_size_mib = CeilingMemMib; a balloon device inflated to
// (CeilingMemMib - LimitMemMib) at boot enforces the user-facing limit.
// See docs/superpowers/specs/2026-04-25-runtime-resource-resize-design.md §3.4.
func (p *Provider) resolveMachineLimits(req domain.CreateSandboxRequest) resolvedLimits {
	limitCPU := int64(p.config.DefaultVcpuCount)
	if req.CPULimit != nil {
		limitCPU = int64(*req.CPULimit)
	}
	limitMem := int64(p.config.DefaultMemoryMib)
	if req.MemoryLimitMB != nil {
		limitMem = int64(*req.MemoryLimitMB)
	}
	ceilingCPU := int64(math.Ceil(float64(limitCPU) * p.config.VcpuHeadroomMult))
	ceilingMem := int64(math.Ceil(float64(limitMem) * p.config.MemHeadroomMult))
	if ceilingCPU > defaultMaxVcpu {
		ceilingCPU = defaultMaxVcpu
	}
	if ceilingMem > defaultMaxMemMB {
		ceilingMem = defaultMaxMemMB
	}
	return resolvedLimits{
		LimitCPU:      limitCPU,
		LimitMemMib:   limitMem,
		CeilingCPU:    ceilingCPU,
		CeilingMemMib: ceilingMem,
	}
}
```

Add `"math"` import to the file.

Update the call site in `CreateSandbox` (replaces the `vcpu, mem := p.resolveMachineLimits(req)` line):

```go
	rl := p.resolveMachineLimits(req)
	info := &VMInfo{
		ID:            vmID,
		CID:           cid,
		UID:           uid,
		NetworkMode:   string(req.NetworkMode),
		VcpuCount:     rl.CeilingCPU,
		MemSizeMib:    rl.CeilingMemMib,
		LimitCPU:      rl.LimitCPU,
		LimitMemMib:   rl.LimitMemMib,
		CeilingCPU:    rl.CeilingCPU,
		CeilingMemMib: rl.CeilingMemMib,
	}
```

Update the second call site in `CreateSandboxFromSnapshot` (around `sandbox.go:571`) the same way.

Update the tuple-destructuring usage in `internal/provider/firecracker/storage_wiring_test.go:138` — that test currently does `vcpu, mem := p.resolveMachineLimits(tc.req)`; change to `rl := p.resolveMachineLimits(tc.req)` and reference `rl.CeilingCPU` / `rl.CeilingMemMib` (these are what the boot path uses, replacing the old `vcpu` / `mem`).

Update the `StartSandbox` body section that re-resolves limits when `info.VcpuCount` or `info.MemSizeMib` is zero (around `sandbox.go:139-145`): keep using `info.VcpuCount` and `info.MemSizeMib` as-is — those now hold the ceiling. The legacy fallback to `p.config.DefaultVcpuCount` stays for old VMInfos.

- [ ] **Step 4: Add the balloon device to the FC config**

Still in `internal/provider/firecracker/sandbox.go`, in `StartSandbox` after `fcCfg := fcsdk.Config{...}` (line ~196):

```go
	// Headroom: if the booted ceiling exceeds the user-facing limit, attach
	// a balloon inflated to the difference so the guest sees `LimitMemMib`
	// despite booting at `CeilingMemMib`. Balloon-deflate during a runtime
	// resize gives the guest more memory back, up to the ceiling.
	if info.CeilingMemMib > 0 && info.CeilingMemMib > info.LimitMemMib {
		balloonMib := info.CeilingMemMib - info.LimitMemMib
		fcCfg.VMID = vmID // ensure VMID is set for balloon
		fcCfg.Balloon = fcsdk.NewBalloonDevice(balloonMib, true /* deflate_on_oom */)
	}
```

If the test in step 1 doesn't already test boot, that's fine — task 8 testing is mostly the `resolveMachineLimits` unit test and the existing `storage_wiring_test.go`. Live-balloon assertions are exercised in the integration leg (task 13).

- [ ] **Step 5: Run all FC tests**

```bash
go test -tags firecracker ./internal/provider/firecracker/ -v
```

Expected: all green, including the new `TestResolveMachineLimits_AppliesHeadroom` and the updated `storage_wiring_test.go`.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/firecracker/sandbox.go internal/provider/firecracker/storage_wiring_test.go internal/provider/firecracker/headroom_test.go
git commit -m "feat(firecracker): boot VMs at ceiling with balloon enforcing user limit"
```

---

## Task 9: Firecracker — `UpdateResources` implementation

Replace the stub from task 2 with the real implementation.

**Files:**
- Modify: `internal/provider/firecracker/sandbox_resize.go`
- Create: `internal/provider/firecracker/sandbox_resize_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/provider/firecracker/sandbox_resize_test.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestUpdateResources_FC_RejectsCPUChange(t *testing.T) {
	p := &Provider{config: Config{VcpuHeadroomMult: 2.0, MemHeadroomMult: 2.0}}
	cpu := 2
	err := p.UpdateResources(context.Background(), domain.BackendRef{Backend: "firecracker", Ref: "vm-x"}, domain.UpdateResourcesRequest{CPULimit: &cpu})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) || prErr.Reason != domain.ResizeReasonCPUUnsupportedByBackend {
		t.Fatalf("err = %v, want ProviderResizeError(cpu_resize_unsupported_by_backend)", err)
	}
}

func TestUpdateResources_FC_MemoryAboveCeiling(t *testing.T) {
	p := newFCProviderWithVM(t, "vm-1", &VMInfo{
		ID: "vm-1", LimitMemMib: 256, CeilingMemMib: 512, MemSizeMib: 512,
	})
	mem := 1024 // above the 512 ceiling
	err := p.UpdateResources(context.Background(), domain.BackendRef{Backend: "firecracker", Ref: "vm-1"}, domain.UpdateResourcesRequest{MemoryLimitMB: &mem})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) || prErr.Reason != domain.ResizeReasonExceedsCeiling {
		t.Fatalf("err = %v, want ProviderResizeError(exceeds_ceiling)", err)
	}
}

// newFCProviderWithVM is a tiny helper. If a similar one already exists in
// the firecracker package (search for `newTestProvider` or similar), reuse it
// instead of adding this one.
func newFCProviderWithVM(t *testing.T, vmID string, info *VMInfo) *Provider {
	t.Helper()
	dir := t.TempDir()
	p := &Provider{
		config: Config{
			ChrootBase: dir, EnableJailer: false,
			VcpuHeadroomMult: 2.0, MemHeadroomMult: 2.0,
		},
		vms: map[string]*VMInfo{vmID: info},
	}
	return p
}
```

- [ ] **Step 2: Run, confirm failures**

Run: `go test -tags firecracker ./internal/provider/firecracker/ -run TestUpdateResources_FC -v`
Expected: FAIL on both — current stub returns `ErrNotSupported`.

- [ ] **Step 3: Replace the stub in `internal/provider/firecracker/sandbox_resize.go`**

Replace the file contents with:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

func (p *Provider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) error {
	if req.CPULimit != nil {
		// See spec §3.6: pinned firecracker-go-sdk@v1.0.0 has no
		// PatchMachineConfiguration. CPU live-resize is deferred.
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonCPUUnsupportedByBackend,
			Detail: "Firecracker provider in this build does not support live vCPU resize",
		}
	}
	if req.MemoryLimitMB == nil {
		return nil // nothing to do
	}

	p.vmMu.RLock()
	info, ok := p.vms[ref.Ref]
	p.vmMu.RUnlock()
	if !ok {
		return fmt.Errorf("firecracker: vm %q not found: %w", ref.Ref, domain.ErrNotFound)
	}

	newLimit := int64(*req.MemoryLimitMB)
	ceiling := info.CeilingMemMib
	if ceiling == 0 {
		// Pre-headroom sandbox: treat the booted size as the ceiling.
		ceiling = info.MemSizeMib
	}
	if newLimit > ceiling {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonExceedsCeiling,
			Detail: fmt.Sprintf("memory_limit_mb %d > ceiling %d", newLimit, ceiling),
		}
	}

	if err := p.patchBalloon(ctx, ref.Ref, ceiling-newLimit); err != nil {
		return fmt.Errorf("firecracker: patch balloon: %w", err)
	}

	p.vmMu.Lock()
	info.LimitMemMib = newLimit
	p.vmMu.Unlock()
	if err := info.Write(p.vmInfoPath(ref.Ref)); err != nil {
		return fmt.Errorf("firecracker: persist vminfo after resize: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Add the `patchBalloon` helper that talks to FC**

Append to the same file:

```go
// patchBalloon sends PATCH /balloon to the Firecracker API socket of the
// given VM, setting the balloon's amount_mib to the requested value.
func (p *Provider) patchBalloon(ctx context.Context, vmID string, amountMib int64) error {
	// SDK note: the firecracker-go-sdk provides Client.PatchBalloon, but it
	// expects a fully-formed *Client tied to an existing VM Machine. The
	// rest of the codebase keeps no Machine reference around between
	// operations; instead it constructs ad-hoc clients backed by the unix
	// socket. Reuse that pattern.
	cli, err := p.newSDKClient(vmID)
	if err != nil {
		return err
	}
	update := &models.BalloonUpdate{AmountMib: fcsdk.Int64(amountMib)}
	if _, err := cli.PatchBalloon(ctx, update); err != nil {
		return err
	}
	return nil
}
```

> **Implementation note:** The firecracker package currently constructs a `*fcsdk.Machine` only inside `StartSandbox`. There is no existing `newSDKClient` helper. Add one in this same task (see Step 5) — keep it tiny, just enough to issue post-boot HTTP calls against the existing socket. If a helper already exists for snapshot operations, reuse it. Search: `grep -n "fcsdk.NewClient\|NewClient(" internal/provider/firecracker/*.go`.

- [ ] **Step 5: Add `newSDKClient` if missing**

Search for an existing helper: `grep -rn "fcsdk.NewClient\|fcsdk.Client" internal/provider/firecracker/`. If one exists for talking to a running VM's socket, reuse it. Otherwise, add to `internal/provider/firecracker/firecracker.go`:

```go
// newSDKClient returns a firecracker-go-sdk client bound to a running VM's
// API socket. It is the post-boot equivalent of the Machine constructor used
// during StartSandbox. Callers must ensure the VM is running.
func (p *Provider) newSDKClient(vmID string) (*fcsdk.Client, error) {
	socket := p.socketPath(vmID)
	if p.config.EnableJailer {
		// socketPath returns a relative path under jailer; resolve to abs.
		socket = filepath.Join(p.vmDir(vmID), "root", socket)
	}
	return fcsdk.NewClient(socket, nil, false), nil
}
```

Add the necessary imports (`fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"`, `"github.com/firecracker-microvm/firecracker-go-sdk/client/models"` in `sandbox_resize.go`).

- [ ] **Step 6: Run the unit tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/ -run TestUpdateResources_FC -v`
Expected: PASS on both.

- [ ] **Step 7: Run all FC tests**

Run: `go test -tags firecracker ./internal/provider/firecracker/`
Expected: all green.

- [ ] **Step 8: Commit**

```bash
git add internal/provider/firecracker/sandbox_resize.go internal/provider/firecracker/sandbox_resize_test.go internal/provider/firecracker/firecracker.go
git commit -m "feat(firecracker): UpdateResources via balloon patch (memory only)"
```

---

## Task 10: Incus — `UpdateResources` implementation

Replace the stub from task 2 with the real implementation. Single `incus config set` call applies both fields atomically.

**Files:**
- Modify: `internal/provider/incus/sandbox_resize.go`
- Create: `internal/provider/incus/sandbox_resize_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/provider/incus/sandbox_resize_test.go`:

```go
//go:build incus

package incus

import (
	"context"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestUpdateResources_Incus_BothFields(t *testing.T) {
	called := []map[string]string{}
	p := &IncusProvider{client: &mockIncusClient{
		updateInstanceFn: func(name string, put incusapi.InstancePut, etag string) (incusclient.Operation, error) {
			called = append(called, put.Config)
			return &fakeOp{}, nil
		},
	}}
	cpu, mem := 4, 1024
	err := p.UpdateResources(context.Background(), domain.BackendRef{Ref: "c1"}, domain.UpdateResourcesRequest{CPULimit: &cpu, MemoryLimitMB: &mem})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}
	if len(called) != 1 {
		t.Fatalf("UpdateInstance calls = %d; want 1", len(called))
	}
	if got := called[0]["limits.cpu"]; got != "4" {
		t.Fatalf("limits.cpu = %q; want 4", got)
	}
	if got := called[0]["limits.memory"]; got != "1024MB" {
		t.Fatalf("limits.memory = %q; want 1024MB", got)
	}
}

func TestUpdateResources_Incus_CPUOnly(t *testing.T) {
	called := []map[string]string{}
	p := &IncusProvider{client: &mockIncusClient{
		updateInstanceFn: func(name string, put incusapi.InstancePut, etag string) (incusclient.Operation, error) {
			called = append(called, put.Config)
			return &fakeOp{}, nil
		},
	}}
	cpu := 2
	err := p.UpdateResources(context.Background(), domain.BackendRef{Ref: "c1"}, domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}
	if _, hasMem := called[0]["limits.memory"]; hasMem {
		t.Fatalf("limits.memory unexpectedly set: %+v", called[0])
	}
}
```

> **Note:** `mockIncusClient` and `fakeOp` are existing patterns in the package. Search `grep -rn "mockIncusClient\|fakeOp" internal/provider/incus/` and reuse / extend. If neither exists, add minimal stubs in this same task. The Incus client interface this provider depends on is in `internal/provider/incus/incus.go` — match its signature exactly.

- [ ] **Step 2: Run, confirm failure**

Run: `go test -tags incus ./internal/provider/incus/ -run TestUpdateResources_Incus -v`
Expected: FAIL — stub returns `ErrNotSupported`.

- [ ] **Step 3: Replace the stub in `internal/provider/incus/sandbox_resize.go`**

```go
//go:build incus

package incus

import (
	"context"
	"fmt"

	incusapi "github.com/lxc/incus/v6/shared/api"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/telemetry"
)

func (p *IncusProvider) UpdateResources(ctx context.Context, ref domain.BackendRef, req domain.UpdateResourcesRequest) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "UpdateResources")
	defer func() { endSpan(retErr) }()

	if req.CPULimit == nil && req.MemoryLimitMB == nil {
		return nil
	}

	cur, etag, err := p.client.GetInstance(ref.Ref)
	if err != nil {
		return fmt.Errorf("incus get instance %q: %w", ref.Ref, err)
	}
	if cur.Config == nil {
		cur.Config = map[string]string{}
	}
	if req.CPULimit != nil {
		cur.Config["limits.cpu"] = fmt.Sprintf("%d", *req.CPULimit)
	}
	if req.MemoryLimitMB != nil {
		cur.Config["limits.memory"] = fmt.Sprintf("%dMB", *req.MemoryLimitMB)
	}

	put := incusapi.InstancePut{
		Architecture: cur.Architecture,
		Config:       cur.Config,
		Devices:      cur.Devices,
		Ephemeral:    cur.Ephemeral,
		Profiles:     cur.Profiles,
		Restore:      cur.Restore,
		Description:  cur.Description,
	}
	op, err := p.client.UpdateInstance(ref.Ref, put, etag)
	if err != nil {
		return fmt.Errorf("incus update instance %q: %w", ref.Ref, err)
	}
	if err := op.WaitContext(ctx); err != nil {
		return fmt.Errorf("incus update instance wait %q: %w", ref.Ref, err)
	}
	return nil
}
```

> **Note:** the exact Incus client method might be `UpdateContainerState` or `UpdateInstanceState` depending on the SDK version pinned in this repo. Verify with `grep -n "UpdateInstance\|UpdateContainer" internal/provider/incus/sandbox.go` and use whichever is already used. Match its etag/return signature.

- [ ] **Step 4: Run the tests**

Run: `go test -tags incus ./internal/provider/incus/ -run TestUpdateResources_Incus -v`
Expected: PASS on both.

- [ ] **Step 5: Run the full incus test suite**

Run: `go test -tags incus ./internal/provider/incus/`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/provider/incus/sandbox_resize.go internal/provider/incus/sandbox_resize_test.go
git commit -m "feat(incus): UpdateResources via incus config set"
```

---

## Task 11: Wire daemon flags in `cmd/navarisd/main.go`

**Files:**
- Modify: `cmd/navarisd/main.go`

- [ ] **Step 1: Locate the existing Firecracker flag block**

Run: `grep -n "firecracker-default-vcpu\|firecracker-default-memory" cmd/navarisd/main.go`
Expected: shows the existing flag definitions; the new flags go right after them.

- [ ] **Step 2: Add the two new flags**

Find the block where `firecracker-default-vcpu` and `firecracker-default-memory-mb` are defined (likely as `flag.IntVar`). Add immediately after:

```go
	var fcVcpuHeadroomMult float64
	flag.Float64Var(&fcVcpuHeadroomMult, "firecracker-vcpu-headroom-mult", 2.0, "boot-time vCPU headroom multiplier on Firecracker (>=1.0); a value of 1.0 disables headroom")

	var fcMemHeadroomMult float64
	flag.Float64Var(&fcMemHeadroomMult, "firecracker-mem-headroom-mult", 2.0, "boot-time memory headroom multiplier on Firecracker (>=1.0); a value of 1.0 disables headroom")
```

- [ ] **Step 3: Pass the flags into the Firecracker provider config**

Find the `firecracker.New(firecracker.Config{...})` call in the same file. Add the new fields:

```go
	firecracker.Config{
		// ...existing fields...
		VcpuHeadroomMult: fcVcpuHeadroomMult,
		MemHeadroomMult:  fcMemHeadroomMult,
	}
```

- [ ] **Step 4: Build the daemon**

Run: `go build -tags firecracker ./cmd/navarisd/`
Expected: success.

- [ ] **Step 5: Confirm `--help` shows the new flags**

Run: `./navarisd --help 2>&1 | grep headroom`
Expected: both new flags listed.

- [ ] **Step 6: Commit**

```bash
git add cmd/navarisd/main.go
git commit -m "feat(navarisd): --firecracker-{vcpu,mem}-headroom-mult flags"
```

---

## Task 12: Client SDK + CLI subcommand

**Files:**
- Modify: `pkg/client/sandbox.go`
- Modify: `cmd/navaris/sandbox.go`

- [ ] **Step 1: Add the client method to `pkg/client/sandbox.go`**

Find the existing client methods (e.g. `CreateSandbox`, `StartSandbox`). Add:

```go
type UpdateResourcesRequest struct {
	CPULimit      *int `json:"cpu_limit,omitempty"`
	MemoryLimitMB *int `json:"memory_limit_mb,omitempty"`
}

type UpdateResourcesResponse struct {
	SandboxID     string `json:"sandbox_id"`
	CPULimit      *int   `json:"cpu_limit"`
	MemoryLimitMB *int   `json:"memory_limit_mb"`
	AppliedLive   bool   `json:"applied_live"`
}

func (c *Client) UpdateSandboxResources(ctx context.Context, sandboxID string, req UpdateResourcesRequest) (*UpdateResourcesResponse, error) {
	var resp UpdateResourcesResponse
	if err := c.do(ctx, http.MethodPatch, "/v1/sandboxes/"+sandboxID+"/resources", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
```

> **Note:** the exact `c.do` shape varies by client; match the existing pattern in `pkg/client/sandbox.go`. Search for an existing PATCH/PUT method to copy the pattern; if none, look at `CreateSandbox` and adapt.

- [ ] **Step 2: Add CLI subcommand**

In `cmd/navaris/sandbox.go`, find where `start`, `stop`, `destroy` subcommands are defined. Add a `resize` subcommand:

```go
{
	Name:  "resize",
	Usage: "navaris sandbox resize <id> [--cpu N] [--memory N_MB]",
	Action: func(ctx context.Context, args []string) error {
		fs := flag.NewFlagSet("resize", flag.ContinueOnError)
		var cpu int
		var mem int
		fs.IntVar(&cpu, "cpu", 0, "new CPU limit (0 = leave unchanged)")
		fs.IntVar(&mem, "memory", 0, "new memory limit in MB (0 = leave unchanged)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() < 1 {
			return errors.New("usage: navaris sandbox resize <id> [--cpu N] [--memory N]")
		}
		if cpu == 0 && mem == 0 {
			return errors.New("at least one of --cpu, --memory is required")
		}
		req := client.UpdateResourcesRequest{}
		if cpu != 0 {
			req.CPULimit = &cpu
		}
		if mem != 0 {
			req.MemoryLimitMB = &mem
		}
		resp, err := apiClient.UpdateSandboxResources(ctx, fs.Arg(0), req)
		if err != nil {
			return err
		}
		fmt.Printf("sandbox %s resized: cpu=%v memory=%v applied_live=%v\n",
			resp.SandboxID, ptrIntStr(resp.CPULimit), ptrIntStr(resp.MemoryLimitMB), resp.AppliedLive)
		return nil
	},
},
```

> **Note:** the CLI structure may use a different command framework (cobra, urfave/cli, custom). Match the pattern of the existing `start` / `stop` subcommands. If `ptrIntStr` doesn't exist, write a tiny inline helper:
> ```go
> func ptrIntStr(p *int) string { if p == nil { return "—" }; return fmt.Sprintf("%d", *p) }
> ```

- [ ] **Step 3: Build the CLI and try a dry run**

```bash
go build -o navaris ./cmd/navaris/
./navaris sandbox resize --help 2>&1 | head -10
```

Expected: shows the resize usage line.

- [ ] **Step 4: Commit**

```bash
git add pkg/client/sandbox.go cmd/navaris/sandbox.go
git commit -m "feat(client,cli): UpdateSandboxResources + 'navaris sandbox resize' subcommand"
```

---

## Task 13: Integration tests

**Files:**
- Modify: `test/integration/sandbox_resize_test.go` (or wherever the existing integration tests live — search first)
- Possibly modify: `docker-compose.integration.yml` and `docker-compose.integration-firecracker.yml` if any new env vars are needed

- [ ] **Step 1: Find the existing integration test layout**

Run: `find test -type f -name "*resize*" -o -name "*integration*" | head` and `ls test/integration/ 2>/dev/null`
Then: `grep -n "TestIntegration_\|//go:build integration" test/**/*.go 2>/dev/null | head -20`

Match the existing pattern (file naming, build tag, helpers) for whatever you write next.

- [ ] **Step 2: Write the Incus integration test**

In a file matching the existing pattern (likely `test/integration/sandbox_resize_test.go`):

```go
//go:build integration && incus

package integration

import (
	"context"
	"testing"
	// ... existing imports for the integration helpers
)

func TestIntegration_Resize_Incus(t *testing.T) {
	ctx := context.Background()
	c := mustClient(t)

	sbx := mustCreateSandbox(t, c, "alpine/3.21", withCPU(1), withMem(512))
	defer mustDestroy(t, c, sbx.SandboxID)

	mustWaitState(t, c, sbx.SandboxID, "running")

	resp, err := c.UpdateSandboxResources(ctx, sbx.SandboxID, client.UpdateResourcesRequest{
		CPULimit: ptrInt(2), MemoryLimitMB: ptrInt(1024),
	})
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if !resp.AppliedLive {
		t.Fatalf("AppliedLive=false; want true")
	}

	// Read it back via incus and check.
	out := mustExec(t, c, sbx.SandboxID, "cat", "/sys/fs/cgroup/memory.max")
	// Allow some rounding slack — Incus reports memory in bytes.
	if !strings.Contains(out, "10737418") /* 1024MB ≈ 1073741824 */ {
		t.Fatalf("memory.max = %q; expected ~1024MB", out)
	}
}
```

> **Note:** the helper names (`mustClient`, `mustCreateSandbox`, `withCPU`, `mustExec`) are conventional. Use whatever the existing tests use. If no `mustExec` helper exists for executing commands inside a sandbox, the assertion is allowed to be `incus exec` via shell from the host instead — wrap in a tiny helper.

- [ ] **Step 3: Write the Firecracker integration test**

```go
//go:build integration && firecracker

package integration

func TestIntegration_Resize_Firecracker_MemoryShrinkAndGrow(t *testing.T) {
	ctx := context.Background()
	c := mustClient(t)

	// Daemon was started with --firecracker-mem-headroom-mult=2.0 so a
	// 256MB limit boots at 512MB ceiling.
	sbx := mustCreateSandbox(t, c, "alpine-3.21", withMem(256))
	defer mustDestroy(t, c, sbx.SandboxID)
	mustWaitState(t, c, sbx.SandboxID, "running")

	// Shrink: 256 -> 192. Balloon goes from 256 -> 320.
	if _, err := c.UpdateSandboxResources(ctx, sbx.SandboxID, client.UpdateResourcesRequest{MemoryLimitMB: ptrInt(192)}); err != nil {
		t.Fatalf("shrink: %v", err)
	}

	// Grow: 192 -> 384.
	if _, err := c.UpdateSandboxResources(ctx, sbx.SandboxID, client.UpdateResourcesRequest{MemoryLimitMB: ptrInt(384)}); err != nil {
		t.Fatalf("grow: %v", err)
	}

	// Above ceiling should 409.
	_, err := c.UpdateSandboxResources(ctx, sbx.SandboxID, client.UpdateResourcesRequest{MemoryLimitMB: ptrInt(600)})
	if err == nil || !strings.Contains(err.Error(), "exceeds_ceiling") {
		t.Fatalf("expected exceeds_ceiling, got: %v", err)
	}
}

func TestIntegration_Resize_Firecracker_CPULiveRejected(t *testing.T) {
	ctx := context.Background()
	c := mustClient(t)
	sbx := mustCreateSandbox(t, c, "alpine-3.21", withCPU(1), withMem(256))
	defer mustDestroy(t, c, sbx.SandboxID)
	mustWaitState(t, c, sbx.SandboxID, "running")

	_, err := c.UpdateSandboxResources(ctx, sbx.SandboxID, client.UpdateResourcesRequest{CPULimit: ptrInt(2)})
	if err == nil || !strings.Contains(err.Error(), "cpu_resize_unsupported_by_backend") {
		t.Fatalf("expected cpu_resize_unsupported_by_backend, got: %v", err)
	}
}
```

- [ ] **Step 4: Run the integration tests**

Pick the matrix that matches your local setup. Examples:

```bash
docker compose -f docker-compose.integration.yml run --rm test go test -tags 'integration incus' ./test/integration/ -run TestIntegration_Resize_Incus -v
docker compose -f docker-compose.integration-firecracker.yml run --rm test go test -tags 'integration firecracker' ./test/integration/ -run TestIntegration_Resize_Firecracker -v
```

Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add test/integration/sandbox_resize_test.go
git commit -m "test(integration): runtime resize for Incus and Firecracker"
```

---

---

## Task 15: Web UI — inline Resources panel on the sandbox detail page

The spec calls for an inline editable Resources section on the sandbox detail page. The web UI is built in `web/` (TypeScript + React). This task adds a minimal panel that lets the user edit the two limits and apply.

**Files:**
- Modify: `web/src/pages/SandboxDetail.tsx` (or whichever component renders the sandbox detail page — search first)
- Modify: `web/src/api/sandbox.ts` (or wherever the API client lives) — add `updateSandboxResources`
- Modify: tests under `web/src/__tests__/` if the project has them

- [ ] **Step 1: Find the sandbox detail component**

Run: `grep -rn "SandboxDetail\|sandbox detail\|getSandbox" web/src/ 2>/dev/null | head`

This points at the file rendering the per-sandbox view. The "Resources" panel goes there; existing patterns for displaying CPU / memory should be visible already (see the existing `Sandboxes.tsx` listing for the `cpu · mem` rendering).

- [ ] **Step 2: Add the API client method**

In `web/src/api/sandbox.ts` (or equivalent), add:

```ts
export async function updateSandboxResources(
	id: string,
	body: { cpu_limit?: number; memory_limit_mb?: number },
): Promise<{ sandbox_id: string; cpu_limit: number | null; memory_limit_mb: number | null; applied_live: boolean }> {
	const res = await apiFetch(`/v1/sandboxes/${id}/resources`, {
		method: "PATCH",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
	return res.json();
}
```

> Match `apiFetch` / fetch wrapper used elsewhere in the file.

- [ ] **Step 3: Add the inline Resources panel**

In the sandbox detail component, add a small panel:

```tsx
function ResourcesPanel({ sandbox, onApplied }: { sandbox: Sandbox; onApplied: () => void }) {
	const [cpu, setCpu] = useState<string>(sandbox.CPULimit?.toString() ?? "");
	const [mem, setMem] = useState<string>(sandbox.MemoryLimitMB?.toString() ?? "");
	const [busy, setBusy] = useState(false);
	const [err, setErr] = useState<string | null>(null);

	async function apply() {
		setErr(null);
		setBusy(true);
		try {
			const body: { cpu_limit?: number; memory_limit_mb?: number } = {};
			const cpuN = cpu === "" ? undefined : Number(cpu);
			const memN = mem === "" ? undefined : Number(mem);
			if (cpuN !== undefined && cpuN !== sandbox.CPULimit) body.cpu_limit = cpuN;
			if (memN !== undefined && memN !== sandbox.MemoryLimitMB) body.memory_limit_mb = memN;
			if (Object.keys(body).length === 0) { setBusy(false); return; }
			await updateSandboxResources(sandbox.SandboxID, body);
			onApplied();
		} catch (e: any) {
			setErr(e?.message ?? "resize failed");
		} finally {
			setBusy(false);
		}
	}

	return (
		<section>
			<h3>Resources</h3>
			<label>CPU <input value={cpu} onChange={e => setCpu(e.currentTarget.value)} disabled={busy} /></label>
			<label>Memory (MB) <input value={mem} onChange={e => setMem(e.currentTarget.value)} disabled={busy} /></label>
			<button onClick={apply} disabled={busy}>{busy ? "Applying…" : "Apply"}</button>
			{err && <p role="alert">{err}</p>}
		</section>
	);
}
```

> Adapt classes / styling to match the rest of the detail page; the existing components in `web/src/` use a particular CSS variable scheme — borrow from a sibling panel.

Hook it into the detail page so it appears next to the existing CPU/memory display, and call `onApplied` to invalidate the sandbox query so the page refreshes.

- [ ] **Step 4: Manual smoke test**

Run the dev server (`cd web && npm run dev` or whatever the project uses) and resize a sandbox via the UI. Confirm the new values are reflected after Apply.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): inline Resources panel on sandbox detail page"
```

---

## Task 16: Wire the new event into the WebSocket subscriber map (if needed)

The `internal/api/events.go` WebSocket handler may filter events by type. Confirm `EventSandboxResourcesUpdated` is forwarded.

**Files:**
- Modify: `internal/api/events.go` (if event-type filtering exists)

- [ ] **Step 1: Check whether events are filtered**

Run: `grep -n "EventType\|EventSandbox\|allowedEvents" internal/api/events.go`

If the file has an explicit allow-list (e.g. `allowedEvents := []EventType{...}`), add `domain.EventSandboxResourcesUpdated` to it. If not, this task is a no-op — the event is published already.

- [ ] **Step 2: If a change was made, run API tests again**

Run: `go test ./internal/api/`
Expected: green.

- [ ] **Step 3: Commit (only if a change was actually made)**

```bash
git add internal/api/events.go
git commit -m "feat(api): forward sandbox_resources_updated on the events stream"
```

---

## Task 17: README + docs touch-up

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add a one-line bullet under the Features list**

Find the existing `Features` block in `README.md`. Add (or insert after the resource-limits line if one exists):

```markdown
- **Runtime resize**: change CPU / memory limits on running sandboxes — `PATCH /v1/sandboxes/{id}/resources` (Incus: live up/down; Firecracker: memory live within boot-time headroom, CPU on next start)
```

- [ ] **Step 2: Note the new daemon flags**

If the README has a flags section or operator notes, mention `--firecracker-vcpu-headroom-mult` / `--firecracker-mem-headroom-mult` and the default of 2.0. Otherwise skip.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: runtime resize feature + Firecracker headroom flags"
```

---

## Final verification

- [ ] **Run the full unit test matrix:**

```bash
go test ./...
go test -tags incus ./...
go test -tags firecracker ./...
```

Expected: all green.

- [ ] **Run the full integration matrix the project supports** (whatever your local setup or CI runs — `docker-compose.integration*.yml` files):

```bash
docker compose -f docker-compose.integration.yml up --abort-on-container-exit
docker compose -f docker-compose.integration-incus-cow.yml up --abort-on-container-exit
docker compose -f docker-compose.integration-firecracker.yml up --abort-on-container-exit
docker compose -f docker-compose.integration-firecracker-cow.yml up --abort-on-container-exit
docker compose -f docker-compose.integration-mixed.yml up --abort-on-container-exit
```

Expected: all green.

- [ ] **Manual smoke test (if a dev environment is available):**

```bash
./navarisd --firecracker-mem-headroom-mult=2.0 ... &
./navaris sandbox create --image alpine/3.21 --memory 256 my-sbx
./navaris sandbox resize my-sbx --memory 384
./navaris sandbox resize my-sbx --memory 600   # expect 409 exceeds_ceiling
```

- [ ] **Open a PR** referencing the spec doc and this plan.
