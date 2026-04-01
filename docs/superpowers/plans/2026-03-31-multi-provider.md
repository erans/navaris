# Multi-Provider Support Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable a single navarisd instance to manage both Incus containers and Firecracker VMs simultaneously via a ProviderRegistry that dispatches operations by backend name.

**Architecture:** A `Registry` struct implements `domain.Provider` and holds a map of named providers. Most Provider methods dispatch via `BackendRef.Backend`. `CreateSandbox` dispatches via a new `Backend` field on `CreateSandboxRequest`. Startup initializes both providers additively; missing KVM logs a warning and disables Firecracker.

**Tech Stack:** Go, existing domain.Provider interface, build tags for firecracker

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/provider/registry.go` | **New** — Registry dispatching to named providers |
| `internal/provider/registry_test.go` | **New** — Unit tests for registry |
| `internal/domain/provider.go` | Add `Backend` field to `CreateSandboxRequest` |
| `internal/api/sandbox.go` | Add `backend` field to API create request structs |
| `internal/service/sandbox.go` | Backend resolution in `Create()`/`CreateFromSnapshot()`, thread to `handleCreate` |
| `cmd/navarisd/main.go` | Replace exclusive switch with additive registry init |
| `cmd/navarisd/provider_firecracker.go` | Add `kvmAvailable()` |
| `cmd/navarisd/provider_firecracker_stub.go` | Add `kvmAvailable()` stub |

---

### Task 1: Add Backend field to domain.CreateSandboxRequest

**Files:**
- Modify: `internal/domain/provider.go:13-20`

- [ ] **Step 1: Add Backend field**

In `internal/domain/provider.go`, add `Backend` to `CreateSandboxRequest`:

```go
type CreateSandboxRequest struct {
	Name          string
	ImageRef      string
	Backend       string
	CPULimit      *int
	MemoryLimitMB *int
	NetworkMode   NetworkMode
	Metadata      map[string]any
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /home/eran/work/navaris && go build ./...`
Expected: PASS — Backend field is added but unused; existing callers don't set it.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/provider.go
git commit -m "feat: add Backend field to CreateSandboxRequest"
```

---

### Task 2: Create ProviderRegistry with tests

**Files:**
- Create: `internal/provider/registry.go`
- Create: `internal/provider/registry_test.go`

- [ ] **Step 1: Write failing test for registry dispatch**

Create `internal/provider/registry_test.go`:

```go
package provider_test

import (
	"context"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider"
)

func TestRegistryDispatchByBackend(t *testing.T) {
	incus := provider.NewMock()
	incus.HealthFn = func(_ context.Context) domain.ProviderHealth {
		return domain.ProviderHealth{Backend: "incus", Healthy: true}
	}
	incus.StartSandboxFn = func(_ context.Context, ref domain.BackendRef) error {
		if ref.Backend != "incus" {
			t.Fatalf("expected incus backend, got %s", ref.Backend)
		}
		return nil
	}

	fc := provider.NewMock()
	fc.HealthFn = func(_ context.Context) domain.ProviderHealth {
		return domain.ProviderHealth{Backend: "firecracker", Healthy: true}
	}
	fc.StartSandboxFn = func(_ context.Context, ref domain.BackendRef) error {
		if ref.Backend != "firecracker" {
			t.Fatalf("expected firecracker backend, got %s", ref.Backend)
		}
		return nil
	}

	reg := provider.NewRegistry()
	reg.Register("incus", incus)
	reg.Register("firecracker", fc)
	reg.SetFallback("incus")

	ctx := context.Background()

	// Dispatch to incus
	if err := reg.StartSandbox(ctx, domain.BackendRef{Backend: "incus", Ref: "test"}); err != nil {
		t.Fatal(err)
	}

	// Dispatch to firecracker
	if err := reg.StartSandbox(ctx, domain.BackendRef{Backend: "firecracker", Ref: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryUnknownBackend(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register("incus", provider.NewMock())
	reg.SetFallback("incus")

	err := reg.StartSandbox(context.Background(), domain.BackendRef{Backend: "nope", Ref: "x"})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestRegistryCreateSandboxUsesFallback(t *testing.T) {
	var called string
	mock := provider.NewMock()
	mock.CreateSandboxFn = func(_ context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
		called = "mock"
		return domain.BackendRef{Backend: "mock", Ref: "m-1"}, nil
	}

	reg := provider.NewRegistry()
	reg.Register("mock", mock)
	reg.SetFallback("mock")

	// No Backend set on request — should use fallback
	_, err := reg.CreateSandbox(context.Background(), domain.CreateSandboxRequest{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if called != "mock" {
		t.Fatalf("expected mock called, got %q", called)
	}
}

func TestRegistryCreateSandboxExplicitBackend(t *testing.T) {
	var calledBackend string
	fc := provider.NewMock()
	fc.CreateSandboxFn = func(_ context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
		calledBackend = "firecracker"
		return domain.BackendRef{Backend: "firecracker", Ref: "fc-1"}, nil
	}

	reg := provider.NewRegistry()
	reg.Register("incus", provider.NewMock())
	reg.Register("firecracker", fc)
	reg.SetFallback("incus")

	// Explicit Backend — should override fallback
	_, err := reg.CreateSandbox(context.Background(), domain.CreateSandboxRequest{
		Name:    "test",
		Backend: "firecracker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calledBackend != "firecracker" {
		t.Fatalf("expected firecracker, got %q", calledBackend)
	}
}

func TestRegistryHealthAggregates(t *testing.T) {
	incus := provider.NewMock()
	incus.HealthFn = func(_ context.Context) domain.ProviderHealth {
		return domain.ProviderHealth{Backend: "incus", Healthy: true}
	}
	fc := provider.NewMock()
	fc.HealthFn = func(_ context.Context) domain.ProviderHealth {
		return domain.ProviderHealth{Backend: "firecracker", Healthy: true}
	}

	reg := provider.NewRegistry()
	reg.Register("incus", incus)
	reg.Register("firecracker", fc)
	reg.SetFallback("incus")

	h := reg.Health(context.Background())
	if !h.Healthy {
		t.Fatal("expected healthy")
	}
	// Backend should list both
	if h.Backend != "firecracker,incus" && h.Backend != "incus,firecracker" {
		t.Fatalf("expected both backends listed, got %q", h.Backend)
	}
}

func TestRegistryLen(t *testing.T) {
	reg := provider.NewRegistry()
	if reg.Len() != 0 {
		t.Fatal("expected 0")
	}
	reg.Register("a", provider.NewMock())
	if reg.Len() != 1 {
		t.Fatal("expected 1")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/eran/work/navaris && go test ./internal/provider/ -run TestRegistry -v`
Expected: FAIL — `NewRegistry`, `Register`, `SetFallback` not defined

- [ ] **Step 3: Implement Registry**

Create `internal/provider/registry.go`:

```go
package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/navaris/navaris/internal/domain"
)

// Registry implements domain.Provider by dispatching to named backend providers.
type Registry struct {
	providers map[string]domain.Provider
	fallback  string
}

var _ domain.Provider = (*Registry)(nil)

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]domain.Provider)}
}

func (r *Registry) Register(name string, p domain.Provider) {
	r.providers[name] = p
}

func (r *Registry) Len() int { return len(r.providers) }

// SetFallback sets the default backend for CreateSandbox when no Backend is specified.
func (r *Registry) SetFallback(name string) { r.fallback = name }

// Fallback returns the current fallback backend name.
func (r *Registry) Fallback() string { return r.fallback }

func (r *Registry) resolve(backend string) (domain.Provider, error) {
	p, ok := r.providers[backend]
	if !ok {
		return nil, fmt.Errorf("provider %q not available", backend)
	}
	return p, nil
}

func (r *Registry) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	backend := req.Backend
	if backend == "" {
		backend = r.fallback
	}
	p, err := r.resolve(backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.CreateSandbox(ctx, req)
}

func (r *Registry) StartSandbox(ctx context.Context, ref domain.BackendRef) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.StartSandbox(ctx, ref)
}

func (r *Registry) StopSandbox(ctx context.Context, ref domain.BackendRef, force bool) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.StopSandbox(ctx, ref, force)
}

func (r *Registry) DestroySandbox(ctx context.Context, ref domain.BackendRef) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.DestroySandbox(ctx, ref)
}

func (r *Registry) GetSandboxState(ctx context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return "", err
	}
	return p.GetSandboxState(ctx, ref)
}

func (r *Registry) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.ExecHandle{}, err
	}
	return p.Exec(ctx, ref, req)
}

func (r *Registry) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.DetachedExecHandle, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.DetachedExecHandle{}, err
	}
	return p.ExecDetached(ctx, ref, req)
}

func (r *Registry) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.SessionHandle{}, err
	}
	return p.AttachSession(ctx, ref, req)
}

func (r *Registry) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.CreateSnapshot(ctx, ref, label, mode)
}

func (r *Registry) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	p, err := r.resolve(sandboxRef.Backend)
	if err != nil {
		return err
	}
	return p.RestoreSnapshot(ctx, sandboxRef, snapshotRef)
}

func (r *Registry) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	p, err := r.resolve(snapshotRef.Backend)
	if err != nil {
		return err
	}
	return p.DeleteSnapshot(ctx, snapshotRef)
}

func (r *Registry) CreateSandboxFromSnapshot(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	p, err := r.resolve(snapshotRef.Backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.CreateSandboxFromSnapshot(ctx, snapshotRef, req)
}

func (r *Registry) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	p, err := r.resolve(snapshotRef.Backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.PublishSnapshotAsImage(ctx, snapshotRef, req)
}

func (r *Registry) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	p, err := r.resolve(imageRef.Backend)
	if err != nil {
		return err
	}
	return p.DeleteImage(ctx, imageRef)
}

func (r *Registry) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
	p, err := r.resolve(imageRef.Backend)
	if err != nil {
		return domain.ImageInfo{}, err
	}
	return p.GetImageInfo(ctx, imageRef)
}

func (r *Registry) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.PublishedEndpoint{}, err
	}
	return p.PublishPort(ctx, ref, targetPort, opts)
}

func (r *Registry) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.UnpublishPort(ctx, ref, publishedPort)
}

func (r *Registry) Health(ctx context.Context) domain.ProviderHealth {
	var names []string
	healthy := false
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []string
	for _, name := range names {
		h := r.providers[name].Health(ctx)
		if h.Healthy {
			healthy = true
		} else if h.Error != "" {
			errs = append(errs, name+": "+h.Error)
		}
	}

	result := domain.ProviderHealth{
		Backend: strings.Join(names, ","),
		Healthy: healthy,
	}
	if len(errs) > 0 {
		result.Error = strings.Join(errs, "; ")
	}
	return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/eran/work/navaris && go test ./internal/provider/ -run TestRegistry -v`
Expected: All 6 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/registry.go internal/provider/registry_test.go
git commit -m "feat: add ProviderRegistry implementing domain.Provider"
```

---

### Task 3: Add backend field to API sandbox requests

**Files:**
- Modify: `internal/api/sandbox.go:12-44`

- [ ] **Step 1: Add backend to request structs**

Add `Backend string \`json:"backend"\`` to these three structs in `internal/api/sandbox.go`:

- `createSandboxRequest` (after line 13, among the existing fields)
- `createSandboxFromSnapshotRequest` (after line 25)
- `createSandboxFromImageRequest` (after line 36)

- [ ] **Step 2: Thread backend into CreateSandboxOpts in handlers**

In the `createSandbox` handler (~line 66), add `Backend: req.Backend` to the `CreateSandboxOpts` struct literal.

In `createSandboxFromSnapshot` handler (~line 100), add `Backend: req.Backend` to opts.

In `createSandboxFromImage` handler (~line 127), add `Backend: req.Backend` to opts.

- [ ] **Step 3: Verify compilation**

Run: `cd /home/eran/work/navaris && go build ./...`
Expected: FAIL — `CreateSandboxOpts` doesn't have `Backend` field yet. That's Task 4.

- [ ] **Step 4: Commit (WIP — compiles after Task 4)**

```bash
git add internal/api/sandbox.go
git commit -m "feat: add backend field to sandbox API create requests"
```

---

### Task 4: Add backend resolution to SandboxService

**Files:**
- Modify: `internal/service/sandbox.go:16-22` (CreateSandboxOpts)
- Modify: `internal/service/sandbox.go:69-125` (Create)
- Modify: `internal/service/sandbox.go:127-183` (CreateFromSnapshot)
- Modify: `internal/service/sandbox.go:382-388` (handleCreate — createReq)

- [ ] **Step 1: Add Backend to CreateSandboxOpts**

In `internal/service/sandbox.go`, add `Backend string` to the `CreateSandboxOpts` struct (line ~16-22):

```go
type CreateSandboxOpts struct {
	Backend       string
	CPULimit      *int
	MemoryLimitMB *int
	NetworkMode   domain.NetworkMode
	ExpiresAt     *time.Time
	Metadata      map[string]any
}
```

- [ ] **Step 2: Add resolveBackend helper**

Add a private method after `CreateSandboxOpts` (~line 23):

```go
// resolveBackend picks the backend for a new sandbox.
// Priority: explicit > auto-detect from image ref > default.
func (s *SandboxService) resolveBackend(explicit, imageRef string) string {
	if explicit != "" {
		return explicit
	}
	if strings.Contains(imageRef, "/") {
		return "incus"
	}
	if imageRef != "" {
		return "firecracker"
	}
	return s.defaultBackend
}
```

Add `"strings"` to the import block.

- [ ] **Step 3: Use resolveBackend in Create()**

In `Create()` (~line 89), replace:
```go
Backend: s.defaultBackend,
```
with:
```go
Backend: s.resolveBackend(opts.Backend, imageID),
```

- [ ] **Step 4: Use resolveBackend in CreateFromSnapshot()**

In `CreateFromSnapshot()` (~line 147), replace:
```go
Backend: s.defaultBackend,
```
with:
```go
Backend: s.resolveBackend(opts.Backend, ""),
```

For snapshot-based creation, the backend comes from the snapshot (handled in `handleCreate`), but `resolveBackend("", "")` returns `s.defaultBackend` which is fine as a placeholder — `handleCreate` overwrites `sbx.Backend` with the ref's backend anyway (line 423).

- [ ] **Step 5: Thread Backend into createReq in handleCreate()**

In `handleCreate()` (~line 382-388), add `Backend` to the `createReq`:

```go
createReq := domain.CreateSandboxRequest{
	Name:          sbx.Name,
	ImageRef:      sbx.SourceImageID,
	Backend:       sbx.Backend,
	CPULimit:      sbx.CPULimit,
	MemoryLimitMB: sbx.MemoryLimitMB,
	NetworkMode:   sbx.NetworkMode,
}
```

- [ ] **Step 6: Verify compilation and existing tests**

Run: `cd /home/eran/work/navaris && go build ./... && go test ./internal/service/ -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/service/sandbox.go
git commit -m "feat: add backend resolution to SandboxService"
```

---

### Task 5: Replace exclusive provider switch with additive registry init

**Files:**
- Modify: `cmd/navarisd/main.go:113-143`
- Modify: `cmd/navarisd/provider_firecracker.go`
- Modify: `cmd/navarisd/provider_firecracker_stub.go`

- [ ] **Step 1: Add kvmAvailable() to provider_firecracker.go**

Append to `cmd/navarisd/provider_firecracker.go`:

```go
func kvmAvailable() bool {
	f, err := os.Open("/dev/kvm")
	if err != nil {
		return false
	}
	f.Close()
	return true
}
```

Add `"os"` to imports.

- [ ] **Step 2: Add kvmAvailable() stub**

Append to `cmd/navarisd/provider_firecracker_stub.go`:

```go
func kvmAvailable() bool { return false }
```

- [ ] **Step 3: Replace provider switch in main.go**

Replace lines 113-137 in `cmd/navarisd/main.go` (the `var prov` / `switch` block) with:

```go
	// Provider registry — enable all configured backends.
	reg := provider.NewRegistry()

	if cfg.incusSocket != "" {
		p, err := newIncusProvider(cfg.incusSocket)
		if err != nil {
			return fmt.Errorf("incus provider: %w", err)
		}
		reg.Register("incus", p)
		logger.Info("incus provider enabled", "socket", cfg.incusSocket)
	}

	if cfg.firecrackerBin != "" {
		if !kvmAvailable() {
			logger.Warn("KVM not available (/dev/kvm), firecracker provider disabled")
		} else {
			p, err := newFirecrackerProvider(cfg)
			if err != nil {
				return fmt.Errorf("firecracker provider: %w", err)
			}
			reg.Register("firecracker", p)
			logger.Info("firecracker provider enabled")
		}
	}

	if reg.Len() == 0 {
		reg.Register("mock", provider.NewMock())
		logger.Info("no providers configured, using mock")
	}

	// Fallback priority: incus > firecracker > first registered.
	switch {
	case reg.Len() > 0 && reg.Fallback() == "":
		for _, name := range []string{"incus", "firecracker"} {
			if _, err := reg.StartSandbox(context.Background(), domain.BackendRef{Backend: name}); err == nil || reg.Len() > 0 {
				// Just check if registered
			}
		}
	}
	// Simple fallback selection
	for _, name := range []string{"incus", "firecracker", "mock"} {
		if reg.Len() > 0 {
			reg.SetFallback(name)
			break
		}
	}

	var prov domain.Provider = reg
	backendName := reg.Fallback()
```

Actually, that fallback logic is overcomplicated. Let me simplify. Replace the entire block (lines 113-137) with:

```go
	// Provider registry — enable all configured backends.
	reg := provider.NewRegistry()

	if cfg.incusSocket != "" {
		p, err := newIncusProvider(cfg.incusSocket)
		if err != nil {
			return fmt.Errorf("incus provider: %w", err)
		}
		reg.Register("incus", p)
		logger.Info("incus provider enabled", "socket", cfg.incusSocket)
	}

	if cfg.firecrackerBin != "" {
		if !kvmAvailable() {
			logger.Warn("KVM not available (/dev/kvm), firecracker provider disabled")
		} else {
			p, err := newFirecrackerProvider(cfg)
			if err != nil {
				return fmt.Errorf("firecracker provider: %w", err)
			}
			reg.Register("firecracker", p)
			logger.Info("firecracker provider enabled")
		}
	}

	if reg.Len() == 0 {
		reg.Register("mock", provider.NewMock())
		logger.Info("no providers configured, using mock")
	}

	// Set default backend: incus > firecracker > mock.
	for _, name := range []string{"incus", "firecracker", "mock"} {
		if reg.Has(name) {
			reg.SetFallback(name)
			break
		}
	}

	var prov domain.Provider = reg
	backendName := reg.Fallback()
```

This requires adding a `Has(name string) bool` method to Registry. Add to `internal/provider/registry.go`:

```go
func (r *Registry) Has(name string) bool {
	_, ok := r.providers[name]
	return ok
}
```

- [ ] **Step 4: Add `"github.com/navaris/navaris/internal/provider"` import to main.go**

The `provider` package import is needed for `provider.NewRegistry()` and `provider.NewMock()`. It may already be imported — check and add if missing.

- [ ] **Step 5: Verify compilation**

Run: `cd /home/eran/work/navaris && go build -tags firecracker ./cmd/navarisd && go build ./cmd/navarisd`
Expected: Both PASS (with and without firecracker tag)

- [ ] **Step 6: Commit**

```bash
git add cmd/navarisd/main.go cmd/navarisd/provider_firecracker.go cmd/navarisd/provider_firecracker_stub.go internal/provider/registry.go
git commit -m "feat: replace exclusive provider switch with additive registry init"
```

---

### Task 6: Run existing tests to verify no regressions

**Files:** None — verification only.

- [ ] **Step 1: Run unit tests**

Run: `cd /home/eran/work/navaris && go test ./... -count=1`
Expected: All PASS

- [ ] **Step 2: Run Firecracker integration tests**

Run: `cd /home/eran/work/navaris && make integration-test-firecracker`
Expected: All 21 tests PASS (single-provider registry behaves identically to single provider)

- [ ] **Step 3: Commit any fixes if needed**

If tests fail, fix the issue and commit.
