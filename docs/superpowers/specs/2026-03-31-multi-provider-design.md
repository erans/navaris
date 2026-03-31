# Multi-Provider Support for navarisd

## Problem

navarisd currently supports only one provider per instance — Incus OR Firecracker, selected at startup via CLI flags. Operators who want both container and microVM isolation must run separate instances and manage routing externally. This adds operational complexity and prevents a unified API surface.

## Goal

A single navarisd instance manages both Incus containers and Firecracker VMs simultaneously. KVM absence degrades gracefully (warning + Firecracker disabled, Incus still works). The API lets callers choose a backend explicitly or rely on auto-detection.

## Design

### ProviderRegistry

New file: `internal/provider/registry.go`

A `Registry` struct that implements `domain.Provider` by dispatching to the correct backend provider based on `BackendRef.Backend`. This is a drop-in replacement — all existing code that accepts `domain.Provider` works unchanged.

```go
type Registry struct {
    providers map[string]domain.Provider
    fallback  string // default backend for new sandboxes ("incus" when both available)
}

func (r *Registry) Register(name string, p domain.Provider)
func (r *Registry) Len() int
func (r *Registry) SetFallback(name string)
func (r *Registry) resolve(backend string) (domain.Provider, error)
```

**Method dispatch:**

- Most methods (Start, Stop, Destroy, Exec, Snapshot, etc.) receive `BackendRef` which already contains `Backend`. The registry calls `resolve(ref.Backend)` and delegates.
- `CreateSandbox` reads the new `Backend` field on `domain.CreateSandboxRequest` for dispatch. Falls back to `r.fallback` if empty.
- `Health` aggregates all providers: `Healthy` is true if any provider is healthy, `Backend` lists all active backends comma-separated (e.g. `"incus,firecracker"`).

### Backend Selection for New Sandboxes

Three layers thread the backend choice from API to provider:

**API** (`internal/api/sandbox.go`): Add optional `"backend"` field to `createSandboxRequest` and `createSandboxFromImageRequest`. Consistent with `registerImageRequest` which already has this field.

**Service** (`internal/service/sandbox.go`): Add `Backend string` to `CreateSandboxOpts`. Resolution order in `Create()`:

1. Explicit `opts.Backend` from API → use it
2. Auto-detect from image ref: contains `/` → `"incus"`, flat name → `"firecracker"`
3. Fall back to `s.defaultBackend` (registry's fallback)

Set `sbx.Backend` before persisting. The `handleCreate` worker threads it into `domain.CreateSandboxRequest.Backend`.

**Domain** (`internal/domain/provider.go`): Add `Backend string` to `CreateSandboxRequest`.

### Startup and KVM Detection

Replace the exclusive switch in `cmd/navarisd/main.go` with additive initialization:

```go
registry := provider.NewRegistry()

if cfg.incusSocket != "" {
    p, err := newIncusProvider(cfg.incusSocket)
    // fatal on error — operator explicitly configured it
    registry.Register("incus", p)
}

if cfg.firecrackerBin != "" {
    if !kvmAvailable() {
        logger.Warn("KVM not available (/dev/kvm), firecracker provider disabled")
    } else {
        p, err := newFirecrackerProvider(cfg)
        // fatal on error — KVM exists but init failed
        registry.Register("firecracker", p)
    }
}

if registry.Len() == 0 {
    registry.Register("mock", provider.NewMock())
}

registry.SetFallback(...) // incus > firecracker > mock
```

`kvmAvailable()` in `cmd/navarisd/provider_firecracker.go`: opens `/dev/kvm` (not just stat) to confirm access.

All downstream code receives the registry as `domain.Provider`. No constructor signature changes.

### Error Handling

- **Unknown backend**: `Registry.resolve("foo")` returns `"unknown backend \"foo\""`.
- **Unavailable backend**: `Registry.resolve("firecracker")` when not registered returns `"firecracker provider not available (is KVM enabled?)"`.
- **Incus failure at startup**: Fatal — operator explicitly configured it.
- **KVM missing at startup**: Warning log, Firecracker skipped, Incus continues.
- **Cross-backend operations**: Snapshots inherit parent sandbox's backend. Images inherit snapshot's backend. All existing `BackendRef` plumbing handles this correctly.

### Reconciler and GC

No changes needed. Both already construct `BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}` from DB records. The registry dispatches to the correct provider automatically.

## Files to Modify

| File | Change |
|------|--------|
| `internal/provider/registry.go` | **New** — Registry implementing `domain.Provider` |
| `internal/provider/registry_test.go` | **New** — Unit tests for registry dispatch |
| `internal/domain/provider.go` | Add `Backend string` to `CreateSandboxRequest` |
| `internal/api/sandbox.go` | Add `backend` field to create request structs |
| `internal/service/sandbox.go` | Backend resolution logic in `Create()`, thread to `handleCreate` |
| `cmd/navarisd/main.go` | Replace switch with additive registry init |
| `cmd/navarisd/provider_firecracker.go` | Add `kvmAvailable()` helper |

Files that need NO changes (already multi-backend ready):
- `internal/service/snapshot.go` — inherits backend from sandbox
- `internal/service/image.go` — inherits backend from snapshot or explicit
- `internal/service/reconcile.go` — already uses BackendRef from DB
- `internal/worker/gc.go` — already uses BackendRef from DB
- All provider implementations (incus, firecracker, mock)

## Testing

**Unit**: `registry_test.go` — two mock providers, verify dispatch by backend, unknown backend error, Health aggregation.

**Integration**: New `docker-compose.integration-mixed.yml` running Incus + Firecracker behind a single navarisd with both flags. Test creates sandboxes on each backend and verifies both work.

**KVM-absent**: navarisd with `--firecracker-bin` but no `/dev/kvm`. Verify warning log, Incus works, Firecracker creation fails with clear error.

**Regression**: Existing `make integration-test` and `make integration-test-firecracker` pass unchanged — a registry with one provider behaves identically to a single provider.
