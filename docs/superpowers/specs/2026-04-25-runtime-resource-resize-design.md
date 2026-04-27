# Runtime CPU / Memory Resize Design

**Status:** Draft
**Date:** 2026-04-25
**Scope:** Add a runtime resize path for sandbox CPU and memory limits on both backends (Firecracker, Incus). Wire boot-time headroom on Firecracker so that resize can grow as well as shrink. This is **spec #1 of three**; spec #2 layers a time-bounded boost with auto-revert on top, and spec #3 adds an in-sandbox channel that lets guest code request a boost back through the daemon.

## 1. Goals

`cpu_limit` and `memory_limit_mb` are accepted at sandbox create time today (PR #12). They cannot be changed after creation — the only way to "resize" is destroy and recreate. This spec adds:

1. A single PATCH endpoint that updates the persisted CPU/memory limits on a sandbox and, when the sandbox is running, applies the change live to the underlying backend.
2. The Firecracker boot-time wiring required for *upward* live memory resize: every new FC sandbox boots with a configurable headroom multiplier on top of the user's limit, with a memory balloon inflated at boot to enforce the user's actual limit. Resize then deflates / re-inflates the balloon.
3. A clean, structured error path (HTTP 409 with a machine-readable reason code) when a backend cannot apply a given resize live, so callers can decide their own fallback strategy.

The result is that resize succeeds in a sub-second sync request in the common case, fails fast with a legible reason in the uncommon case, and the headroom mechanic is in place for spec #2 to layer "boost for X minutes" without re-touching boot-time wiring.

## 2. Non-Goals

- **Disk size / storage resize.** Out of scope. (Different cost profile per backend; deferred.)
- **Time-bounded boost with auto-revert.** That is spec #2. Spec #1 lays the foundation by adding the resize primitive and headroom; spec #2 builds the timer + revert logic on top.
- **In-sandbox channel for boost requests.** That is spec #3.
- **Snapshot/restore-based resize on Firecracker.** When live resize is impossible (FC <1.13 changing CPU on a running VM, or any request above the boot-time ceiling), the API returns 409. We do not silently fall back to a multi-second snapshot/restore cycle. A future explicit endpoint (`POST /sandboxes/{id}/resize-with-restart`) could add that, but it is out of scope here.
- **Adding a balloon to existing pre-headroom sandboxes.** Sandboxes booted before this change have no balloon and were booted at exactly `limit`. They get a "ceiling = limit" treatment so they can shrink memory but not grow it. Live retrofit is out of scope.
- **Async / Operation-based resize.** Resize is genuinely fast (sub-second) on every supported path. The Operation/dispatcher pattern is reserved for long-running work.
- **Per-sandbox ceilings.** Headroom is daemon-wide, set by flag, not per-sandbox.

## 3. Architecture

### 3.1 Daemon flags (Firecracker only)

Two new flags on `navarisd`:

```
--firecracker-vcpu-headroom-mult float   default 1.0
--firecracker-mem-headroom-mult  float   default 1.0
```

Both must be `>= 1.0`. A multiplier of `1.0` (the default) disables headroom (sandbox boots at exactly `limit`, no growth headroom — equivalent to today's behavior). Set to a value `> 1.0` to enable grow-resize within the boot-time ceiling. Validated at daemon startup; out-of-range values cause startup failure with a clear message.

These live on `firecracker.Config` alongside `DefaultVcpuCount` / `DefaultMemoryMib`.

### 3.2 Provider interface change

`internal/domain/provider.go` gains:

```go
type UpdateResourcesRequest struct {
    CPULimit      *int   // omitted => unchanged
    MemoryLimitMB *int   // omitted => unchanged
}

type ProviderResizeError struct {
    Reason string // machine-readable, see §3.6
    Detail string // human-readable
}

func (e *ProviderResizeError) Error() string { ... }

// Provider interface
UpdateResources(ctx context.Context, sandboxRef string, req UpdateResourcesRequest) error
```

Implemented by `incus.Provider`, `firecracker.Provider`, and `mockProvider` (for tests).

`UpdateResources` is invoked **only** on running sandboxes. The service layer handles the stopped-sandbox case by writing to SQLite without calling the provider.

### 3.3 Service layer

New service method on `SandboxService`:

```go
type UpdateResourcesOpts struct {
    SandboxID     string
    CPULimit      *int
    MemoryLimitMB *int
}

type UpdateResourcesResult struct {
    Sandbox      *domain.Sandbox
    AppliedLive  bool   // true if the running VM was reconfigured
}

func (s *SandboxService) UpdateResources(ctx context.Context, opts UpdateResourcesOpts) (*UpdateResourcesResult, error)
```

Flow:

1. Reject if both `CPULimit` and `MemoryLimitMB` are nil → `domain.ErrInvalidArgument` ("at least one of cpu_limit / memory_limit_mb must be supplied").
2. Load sandbox by ID; 404 if not found.
3. Reject if state is `destroyed` or `failed` → `domain.ErrInvalidState`.
4. Validate the requested values against backend bounds by reusing the bounds defined in `internal/service/limits.go`. The existing `validateLimits` takes a `CreateSandboxOpts`; we extract the bounds-check logic into a smaller helper `validateResourceBounds(cpu *int, mem *int, backend string) error` that both `validateLimits` and the new resize path call. No bounds change.
5. If sandbox is running **and** backend is Firecracker: validate request against the persisted `ceiling_cpu` / `ceiling_mem_mib` on the VMInfo (see §3.4). If `new_limit > ceiling`, fail with `ProviderResizeError{Reason: "exceeds_ceiling"}` → mapped to 409.
6. Persist new limits to SQLite (`UPDATE sandbox SET cpu_limit = ?, memory_limit_mb = ?, updated_at = ?`).
7. If state is `running`, call `provider.UpdateResources`. Any error from this call: roll back the SQLite update and return the error.
8. Emit `sandbox.resources_updated` event with `{sandbox_id, cpu_limit, memory_limit_mb, applied_live}` payload.
9. Return the updated sandbox + `applied_live` flag.

Step 7 ordering note: we write SQLite first, then call the provider, then roll back on provider error. Alternative ordering (provider first, SQLite second) leaves a window where the running VM has new limits but persisted state has old limits — worse for crash consistency. The chosen order is "stage in DB, apply, roll back on failure."

### 3.4 Firecracker boot-time headroom

`firecracker.Provider.resolveMachineLimits` becomes:

```go
type resolvedLimits struct {
    LimitCPU    int64    // user-facing limit (what they get)
    LimitMemMib int64
    CeilingCPU  int64    // boot-time vCPU count
    CeilingMem  int64    // boot-time mem_size_mib
}

func (p *Provider) resolveMachineLimits(req domain.CreateSandboxRequest) resolvedLimits {
    limitCPU := int64(p.config.DefaultVcpuCount)
    if req.CPULimit != nil { limitCPU = int64(*req.CPULimit) }

    limitMem := int64(p.config.DefaultMemoryMib)
    if req.MemoryLimitMB != nil { limitMem = int64(*req.MemoryLimitMB) }

    ceilingCPU := clamp(int64(math.Ceil(float64(limitCPU) * p.config.VcpuHeadroomMult)), 1, limitFCMaxCPU)
    ceilingMem := clamp(int64(math.Ceil(float64(limitMem) * p.config.MemHeadroomMult)), limitFCMinMemMB, limitFCMaxMemMB)

    return resolvedLimits{LimitCPU: limitCPU, LimitMemMib: limitMem, CeilingCPU: ceilingCPU, CeilingMem: ceilingMem}
}
```

`VMInfo` (in `internal/provider/firecracker/vminfo.go`) gains:

```go
LimitCPU      int64 `json:"limit_cpu,omitempty"`
LimitMemMib   int64 `json:"limit_mem_mib,omitempty"`
CeilingCPU    int64 `json:"ceiling_cpu,omitempty"`
CeilingMemMib int64 `json:"ceiling_mem_mib,omitempty"`
// existing VcpuCount and MemSizeMib remain — they reflect the actual booted ceiling
```

Boot sequence (`sandbox.go:Start`):

1. `vcpu = ceilingCPU`, `mem_size_mib = ceilingMem` passed to FC `MachineCfg`.
2. Configure a balloon device: `PUT /balloon` with `{amount_mib: ceilingMem - limitMem, deflate_on_oom: true, stats_polling_interval_s: 0}`.
3. Persist `LimitCPU / LimitMemMib / CeilingCPU / CeilingMemMib` on `VMInfo`.

Skipped if `mem_headroom_mult == 1.0` and `vcpu_headroom_mult == 1.0` (degenerate case — no balloon, no growth, equivalent to today's behavior).

### 3.5 Firecracker `UpdateResources`

> **Update (2026-04-27):** the CPU-rejection branch shown below was replaced by live cgroup-based enforcement in [2026-04-27-firecracker-live-cpu-resize-design.md](2026-04-27-firecracker-live-cpu-resize-design.md). FC CPU is enforced via cgroup CPU bandwidth (`cpu.max` v2 / `cpu.cfs_quota_us` v1), not vCPU hot-plug. The `cpu_resize_unsupported_by_backend` reason is retained but no longer emitted by the FC provider.

```go
func (p *Provider) UpdateResources(ctx context.Context, vmID string, req domain.UpdateResourcesRequest) error {
    info, err := p.loadVMInfo(vmID)
    if err != nil { return err }

    if req.MemoryLimitMB != nil {
        newLimit := int64(*req.MemoryLimitMB)
        ceiling := info.CeilingMemMib
        if ceiling == 0 { ceiling = info.MemSizeMib } // pre-headroom sandbox
        if newLimit > ceiling {
            return &domain.ProviderResizeError{Reason: "exceeds_ceiling", Detail: fmt.Sprintf("memory_limit_mb %d > ceiling %d", newLimit, ceiling)}
        }
        amount := ceiling - newLimit
        if err := p.fcClient.PatchBalloon(ctx, vmID, amount); err != nil {
            return fmt.Errorf("patch balloon: %w", err)
        }
        info.LimitMemMib = newLimit
    }

    if req.CPULimit != nil {
        // The pinned firecracker-go-sdk (v1.0.0) does not expose
        // PatchMachineConfiguration; only PutMachineConfiguration, which is
        // pre-boot only. There is no path to hot-plug vCPUs on a running VM
        // from the current SDK. Any CPU change request on a running FC VM is
        // rejected. CPU resize lands in a follow-up spec that bumps the SDK
        // (or calls Firecracker's HTTP API directly).
        return &domain.ProviderResizeError{Reason: "cpu_resize_unsupported_by_backend", Detail: "Firecracker provider in this build does not support live vCPU resize"}
    }

    return p.saveVMInfo(vmID, info)
}
```

`supportsCPUHotplug()` is removed from this spec — see §3.6 note. The `CeilingCPU` field is still recorded on `VMInfo` for future use; spec #1 just never applies a live CPU change.

### 3.6 Error reasons

`ProviderResizeError.Reason` is one of:

| Reason                              | HTTP | Meaning                                                      |
|-------------------------------------|------|--------------------------------------------------------------|
| `exceeds_ceiling`                   | 409  | Request exceeds boot-time ceiling on Firecracker             |
| `cpu_resize_unsupported_by_backend` | 409  | Firecracker SDK in this build cannot hot-plug vCPUs on a running VM |
| `backend_rejected`                  | 409  | Backend returned an error from the live API call             |

> **CPU resize on running Firecracker VMs is not supported in spec #1.** The pinned `firecracker-go-sdk@v1.0.0` only exposes `PutMachineConfiguration` (pre-boot) — there is no hot-plug method. Memory resize works fully (via `PatchBalloon`). CPU resize on a *stopped* FC sandbox is fine: SQLite is updated and the new value applies on next start. CPU resize on a *running* FC sandbox returns 409 `cpu_resize_unsupported_by_backend`. Spec #1.5 (or whichever spec lands the SDK bump) lifts this restriction.

The API layer (`internal/api/sandbox.go`) maps `*domain.ProviderResizeError` → HTTP 409 with a JSON body:

```json
{"error": "exceeds_ceiling", "detail": "memory_limit_mb 4096 > ceiling 2048"}
```

### 3.7 Incus `UpdateResources`

```go
func (p *Provider) UpdateResources(ctx context.Context, name string, req domain.UpdateResourcesRequest) error {
    args := []string{}
    if req.CPULimit != nil      { args = append(args, fmt.Sprintf("limits.cpu=%d", *req.CPULimit)) }
    if req.MemoryLimitMB != nil { args = append(args, fmt.Sprintf("limits.memory=%dMB", *req.MemoryLimitMB)) }
    if len(args) == 0 { return nil }
    return p.runCmd(ctx, append([]string{"config", "set", name}, args...)...)
}
```

A single `incus config set` call applies both atomically. Live cgroup writes — no version constraints. Bounds already validated at the service layer.

### 3.8 API surface

```
PATCH /sandboxes/{id}/resources
Content-Type: application/json
```

Request body:

```json
{
  "cpu_limit": 4,
  "memory_limit_mb": 2048
}
```

Both fields optional; at least one required.

Response (200):

```json
{
  "sandbox_id": "sbx_...",
  "cpu_limit": 4,
  "memory_limit_mb": 2048,
  "applied_live": true
}
```

Error mapping:

| Status | Trigger                                                                |
|--------|------------------------------------------------------------------------|
| 400    | malformed JSON, both fields omitted, bounds violation (`domain.ErrInvalidArgument`, consistent with the existing API mapping) |
| 404    | unknown sandbox                                                        |
| 409    | `*domain.ProviderResizeError` (any reason); state is `destroyed`/`failed` |

### 3.9 Event stream

A new event type `sandbox.resources_updated` on the existing WebSocket event stream:

```json
{
  "type": "sandbox.resources_updated",
  "sandbox_id": "sbx_...",
  "cpu_limit": 4,
  "memory_limit_mb": 2048,
  "applied_live": true,
  "timestamp": "2026-04-25T12:34:56Z"
}
```

Emitted unconditionally on a successful update (live or persist-only), so the web UI and other subscribers can refresh without polling.

### 3.10 CLI

`navaris sandbox resize <id> [--cpu N] [--memory N]` — at least one of `--cpu` / `--memory` required. Thin wrapper over the PATCH endpoint. Output prints the new limits and `applied_live`.

### 3.11 Web UI

Sandbox detail page gains an inline "Resources" section with editable CPU / Memory fields and an Apply button. On submit, calls the PATCH endpoint and toasts the result. Out of scope to refactor the existing layout.

## 4. Testing

### 4.1 Unit tests

- `internal/service/sandbox_resize_test.go`:
    - both fields nil → 400
    - destroyed sandbox → ErrInvalidState
    - bounds violation → ErrInvalidArgument
    - stopped sandbox → SQLite updated, provider not called, `applied_live=false`
    - running sandbox → provider called, `applied_live=true`
    - provider error → SQLite rolled back to prior values
- `internal/provider/firecracker/sandbox_resize_test.go`:
    - `resolveMachineLimits` with multiplier 1.0 / 2.0 / 4.0
    - clamp to FC max bounds
    - `UpdateResources` rejects above ceiling
    - `UpdateResources` rejects any CPU change on a running FC VM with `cpu_resize_unsupported_by_backend` (CPU resize not supported by current FC SDK)
    - `UpdateResources` rolls back `info.LimitMemMib` on `saveVMInfo` failure (the in-memory `info` struct should not be left in an inconsistent state when persistence fails)
- `internal/provider/incus/sandbox_resize_test.go`:
    - emits exactly one `incus config set` with both args
    - emits one `incus config set` with single arg when only one field supplied

### 4.2 API tests

- `internal/api/sandbox_test.go`:
    - 400 / 404 / 409 / 422 paths
    - happy path returns the expected JSON shape
    - emits `sandbox.resources_updated` event

### 4.3 Integration tests

- **Incus leg** (`docker-compose.integration.yml` and `docker-compose.integration-incus-cow.yml`):
    1. Create container with `cpu_limit=1, memory_limit_mb=512`.
    2. PATCH to `cpu_limit=2, memory_limit_mb=1024`.
    3. Assert `incus config show <name>` reflects new values.
    4. Assert cgroup files inside the container (`/sys/fs/cgroup/...`) match.
- **Firecracker leg** (`docker-compose.integration-firecracker.yml`):
    1. Daemon flag `--firecracker-mem-headroom-mult=2.0`.
    2. Create VM with `memory_limit_mb=256` → boots at `mem_size_mib=512`, balloon inflated to 256.
    3. PATCH `memory_limit_mb=384` → balloon = 128. Assert via `GET /balloon`.
    4. PATCH `memory_limit_mb=600` → expect 409 `exceeds_ceiling`.
    5. PATCH `cpu_limit=2` on a running FC VM: 409 `cpu_resize_unsupported_by_backend` (deferred to a future spec).
    6. PATCH `cpu_limit=2` on a *stopped* FC sandbox: 200 `applied_live=false`. Start it; FC reports the new vcpu count.

## 5. Open Questions

None at the time of writing. CPU live-resize on Firecracker is deferred to a follow-up spec that bumps the SDK or calls FC's HTTP API directly (see §3.6).

## 6. Migration / Compatibility

- Existing sandboxes (booted before this change): VMInfo lacks `CeilingCPU` / `CeilingMemMib`. The resize path treats `ceiling = MemSizeMib` (i.e. the boot value) — so memory can shrink but not grow. CPU on a running FC VM is rejected as in §3.6 regardless.
- No SQLite schema change. The existing `cpu_limit` and `memory_limit_mb` columns are reused.
- API additions are pure-additive (new endpoint, new event type). No client breakage.
- The `--firecracker-*-headroom-mult` flags default to `1.0` — meaning sandboxes boot at exactly the user's limit by default (no headroom). This preserves prior allocation behavior. Operators who want grow-resize headroom set the flags to a value `> 1.0` explicitly.

## 7. Out-of-scope notes for spec #2 and spec #3

These are recorded here so the next-stage specs can reference them:

- **Spec #2 (boost):** The boost is a wrapper over `SandboxService.UpdateResources` plus a SQLite-backed timer. Boost stores `(sandbox_id, original_cpu, original_mem, expires_at)` and a background reverter calls `UpdateResources` with the originals when the timer fires. Survives daemon restart by replaying outstanding boost rows on startup.
- **Spec #3 (in-sandbox channel):** A Firecracker-side path uses the existing vsock + `navaris-agent` (extending the protocol with a `request_boost` message). An Incus-side path likely uses a host-mounted UDS the daemon listens on. Both paths land at a new `BoostService` method on the daemon, with rate limiting + per-sandbox boost policy.
