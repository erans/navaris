# Sandbox Resource Limits Design

**Status:** Draft
**Date:** 2026-04-25
**Scope:** Wire CPU and memory limits end-to-end on the Firecracker provider (Incus already works) and add backend-agnostic validation.

## 1. Goals

The fields `CPULimit` and `MemoryLimitMB` already exist on `domain.CreateSandboxRequest` and flow through API → service → provider untouched. Today:

- **Incus** (`internal/provider/incus/sandbox.go:31-35`, `:195-199`) reads them correctly and emits `limits.cpu` / `limits.memory` on the instance config.
- **Firecracker** (`internal/provider/firecracker/sandbox.go:141-144`) **ignores them silently** and hardcodes `VcpuCount: 1, MemSizeMib: 256`. Any value the caller passes is dropped on the floor.

This spec closes the silent-no-op on Firecracker and adds backend-agnostic validation so out-of-range values fail fast at HTTP 400 rather than producing failed sandboxes or, worse, OOM kills.

## 2. Non-Goals

- **Storage / disk size limit.** Different cost profile (Incus is one config line; Firecracker requires `resize2fs` interacting with reflink). Separate brainstorm.
- **Hot-resize.** Vanilla Firecracker has no balloon device; resize requires destroy + recreate.
- **Per-child resource overrides on fork.** Children always inherit the parent's `MachineCfg` from `vmstate.bin` — Firecracker constraint.
- **Restoring a snapshot with new limits.** Same Firecracker constraint; rejected at validation.
- **Renaming `MemoryLimitMB` → `MemoryLimitMiB`.** API/SDK breaking change. Documentation note instead.
- **Changing Incus behavior.** Today nil = unlimited (host default). Stays.

## 3. Architecture

### 3.1 New error sentinel

In `internal/domain/errors.go`:

```go
ErrInvalidArgument = errors.New("invalid argument")
```

`internal/api/response.go` `mapErrorCode` adds an `errors.Is(err, domain.ErrInvalidArgument) → http.StatusBadRequest` branch alongside the existing sentinels. Existing `ErrInvalidState` is reserved for state-machine transitions and is not reused for argument validation.

### 3.2 Service-layer validation

A new file `internal/service/limits.go` provides:

```go
func validateLimits(opts CreateSandboxOpts, fromSnapshot bool) error
```

Called at the top of both `SandboxService.Create` and `SandboxService.CreateFromSnapshot`, before any sandbox row is written or operation enqueued. Failing here produces an HTTP 400 cleanly with no DB or worker side effects.

**Rules:**

| Field | Nil | Set, normal create | Set, from-snapshot |
|---|---|---|---|
| `CPULimit` | OK — provider applies its default | Must be `1 ≤ n ≤ 32` | Reject: "cpu_limit cannot be set on from-snapshot create; vCPU count is baked into the snapshot" |
| `MemoryLimitMB` | OK — provider applies its default | Must be `128 ≤ n ≤ 8192` | Reject: "memory_limit_mb cannot be set on from-snapshot create; memory size is baked into the snapshot" |

**Bounds rationale:**

- `CPULimit` upper bound 32 = Firecracker's `MAX_SUPPORTED_VCPUS`. Lower bound 1 = obvious.
- `MemoryLimitMB` upper bound 8192 (8 GiB) = a sane sandbox ceiling. Operators who need more can patch the constant; we did not expose this as a flag because the brainstorm answer was "hard bounds."
- `MemoryLimitMB` lower bound 128 = the floor where most modern guest kernels boot without panic.

**Notably NOT validated:**

- **Host fitness** ("does this host have 8 GiB free?") — kernel-level OOM, oversubscription policy, and concurrency are operator concerns. A sandbox that can't physically run will fail at boot with a recognizable error, not at create.
- **Memory unit drift** — see §3.5.

### 3.3 Daemon flags

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--firecracker-default-vcpu` | int | `1` | Used when `req.CPULimit == nil` |
| `--firecracker-default-memory-mb` | int | `256` | Used when `req.MemoryLimitMB == nil` |

Defaults preserve current behavior exactly. The `firecracker-` prefix is deliberate — Incus has no need for a default (nil = unlimited, host default).

Plumbing: flags → `config` struct in `cmd/navarisd/main.go` → `firecracker.Config{DefaultVcpuCount, DefaultMemoryMib}` → `firecracker.Provider{config}`. One field added per layer; no signature changes anywhere else.

### 3.4 Firecracker provider wire-up

`firecracker.Config` gains:

```go
DefaultVcpuCount int // default 1 (set by Config.defaults() if zero)
DefaultMemoryMib int // default 256 (set by Config.defaults() if zero)
```

`Config.defaults()` extends:

```go
if c.DefaultVcpuCount == 0 {
    c.DefaultVcpuCount = 1
}
if c.DefaultMemoryMib == 0 {
    c.DefaultMemoryMib = 256
}
```

This is defense-in-depth: if a caller forgets the flag and passes 0, the provider still produces a bootable VM rather than a 0-vCPU panic.

`internal/provider/firecracker/sandbox.go:141-144` becomes:

```go
vcpu := int64(p.config.DefaultVcpuCount)
if req.CPULimit != nil {
    vcpu = int64(*req.CPULimit)
}
mem := int64(p.config.DefaultMemoryMib)
if req.MemoryLimitMB != nil {
    mem = int64(*req.MemoryLimitMB)
}
// ...
MachineCfg: models.MachineConfiguration{
    VcpuCount:  fcsdk.Int64(vcpu),
    MemSizeMib: fcsdk.Int64(mem),
},
```

The provider does not re-validate; service-layer validation already rejected bad values.

`cmd/navarisd/provider_firecracker.go` gains two field assignments to forward the flags.

### 3.5 Memory unit semantics (MB ≈ MiB)

The API field is named `memory_limit_mb` but is interpreted differently by each backend: Firecracker uses it as MiB (1024-based, fed straight into `MemSizeMib`); Incus consumes it as `<N>MB` (1000-based, the literal `limits.memory: "<N>MB"` is decimal megabytes per Incus's documented unit syntax). For the same integer N, Firecracker's VM has `N × 1024 × 1024` bytes of RAM while Incus's container has `N × 1000 × 1000` bytes — a ~5% drift *between* backends, not within either.

**Decision:** accept the drift, document clearly. Each backend is internally consistent; the cross-backend difference is at the noise floor for sandbox sizing. Renaming the API field to `memory_limit_mib` is a breaking change for callers and SDK consumers, and the precision matters less than the convenience of a stable field.

The validation bounds (§3.2) are applied to the bare integer; both backends receive the same number.

### 3.6 Incus side

No code changes. `internal/provider/incus/sandbox.go` already reads both fields. Nil = no `limits.cpu` / `limits.memory` key emitted = container is unlimited (host policy applies). No Incus-specific defaults are introduced.

### 3.7 Fork inheritance (no change)

Children spawned via `POST /v1/sandboxes/{id}/fork` inherit the parent's CPU/memory through `firecracker.SpawnFromForkPoint`, which restores from `vmstate.bin`. The `MachineCfg` literal in `sandbox.go` is not on this path, so the wire-up in §3.4 has no effect on fork.

`SandboxService.Fork` (`internal/service/sandbox.go:228+`) creates child sandbox rows directly via `s.sandboxes.Create(ctx, child)`, bypassing the `Create` method and therefore `validateLimits`. This is correct: the child's `CPULimit` / `MemoryLimitMB` are copied from the parent, and the parent already passed validation when it was created. Re-validating at fork time would be redundant and would produce confusing errors if validation bounds ever change between the parent's creation and the fork.

A fork request body does not carry per-child limit fields and never will — a Firecracker constraint, not navaris policy.

## 4. Testing

### 4.1 Unit tests

1. **`internal/service/limits_test.go` (new)** — table-driven `validateLimits`. One row per case:
   - All-nil → no error (both Create and CreateFromSnapshot).
   - CPU 0 / 33 / -1 → wrapped `ErrInvalidArgument`.
   - CPU 1 / 16 / 32 → no error.
   - Memory 127 / 8193 / 0 → wrapped `ErrInvalidArgument`.
   - Memory 128 / 1024 / 8192 → no error.
   - Any non-nil on from-snapshot → wrapped `ErrInvalidArgument` with field-specific message.
   - All errors verified via `errors.Is(err, domain.ErrInvalidArgument)`.

2. **`internal/service/sandbox_test.go` (extend)** — two end-to-end-through-the-service tests:
   - `Create` with `MemoryLimitMB: ptr(8193)` → returns wrapped `ErrInvalidArgument`; no sandbox row created; no operation enqueued.
   - `CreateFromSnapshot` with `CPULimit: ptr(2)` → same.

3. **`internal/api/response_test.go` (extend)** — assert `mapErrorCode(err)` returns `http.StatusBadRequest` when `errors.Is(err, domain.ErrInvalidArgument)`.

4. **`internal/provider/firecracker/storage_wiring_test.go` (extend)** — verify `Config.defaults()` sets the new fields when zero, and respects non-zero values. Plus a smoke test that requests with `req.CPULimit = ptr(4)` produce a `MachineCfg.VcpuCount` of 4 (mock the SDK or extract a small helper for this assertion).

5. **`cmd/navarisd/storage_test.go` (extend, or new `flags_test.go`)** — assert `--firecracker-default-vcpu` / `--firecracker-default-memory-mb` parse correctly and flow into `firecracker.Config` via `newFirecrackerProvider`.

### 4.2 Integration test

`test/integration/limits_test.go` (new, `//go:build integration`):

```go
func TestSandbox_HonorsRequestedMemoryLimit(t *testing.T) {
    c := newClient()
    ctx := context.Background()
    proj := createTestProject(t, c)

    mem := 512
    op, err := c.CreateSandbox(ctx, client.CreateSandboxRequest{
        ProjectID:     proj.ProjectID,
        Name:          "limits-mem-512",
        ImageID:       baseImage(),
        MemoryLimitMB: &mem,
    })
    // ... wait for running, exec `cat /proc/meminfo | awk '/MemTotal:/ {print $2}'`,
    // assert reported MemTotal (in KiB) is in 480..520 MiB band.
}
```

The band tolerance accounts for kernel + initramfs reserved memory; we assert "approximately 512 MiB," not equality. The test runs against both Incus and Firecracker via the existing matrix, so both backends are covered by one test.

### 4.3 Validation tests through the API

`internal/api/sandbox_test.go` gets two thin tests:

- POST `/v1/sandboxes` with `cpu_limit: 33` → 400.
- POST `/v1/sandboxes/from-snapshot` with `memory_limit_mb: 1024` → 400.

These confirm the `mapErrorCode` wiring end-to-end.

## 5. Rollout & Behavior Changes

**Zero-config:** new flags default to today's hardcoded values. Existing deployments see no flag-driven behavior change.

**Three surfaced behavior changes worth a release note:**

1. **Firecracker honors `memory_limit_mb` and `cpu_limit`.** Previously these were silently ignored on Firecracker; callers got 256 MiB / 1 vCPU regardless. After this PR, callers get the value they asked for (subject to §3.2 bounds). The change is "doing what the API documentation always implied," but anyone happening to send these fields today without noticing will see real allocations now.

2. **`POST /v1/sandboxes/from-snapshot` rejects `cpu_limit` / `memory_limit_mb`.** Previously these were silently ignored on Firecracker (snapshots restore with `vmstate.bin`-baked values). After this PR, sending them produces HTTP 400. Same release-note caveat — surfacing a misuse instead of swallowing it.

3. **Memory upper bound is now 8192 MB.** Previously navaris applied no upper bound on `memory_limit_mb`; large values flowed through to the backend. **Incus deployments today running containers with `memory_limit_mb > 8192` will see HTTP 400 after this PR.** (Firecracker callers are unaffected — they were silently capped at 256 MiB regardless of what they sent.) Operators who need higher must edit the constant in `internal/service/limits.go`. A daemon flag was deliberately not exposed to keep policy decisions out of operator hands at this stage; revisit if real users hit this.

**Migration path:** none required for callers using ≤ 8192 MB. Operators who set `--firecracker-default-vcpu` / `--firecracker-default-memory-mb` get the new defaults; everyone else preserves the status quo.

## 6. Documentation

- `README.md` daemon-flags table: two new rows for the new flags.
- `docs/native-install.md` Firecracker config block: two new commented env vars (`NAVARIS_FIRECRACKER_DEFAULT_VCPU`, `NAVARIS_FIRECRACKER_DEFAULT_MEMORY_MB`).
- `packaging/systemd/navarisd.env.example` + `navarisd-launch.sh`: same pattern as the storage flags.
- API reference (wherever `memory_limit_mb` / `cpu_limit` are documented in the README's Running section): note the validation bounds and the from-snapshot rejection.
- A short paragraph in the API/MCP doc surface noting that limits are inherited on fork and not overridable per child.

No new top-level docs file is created; this is a per-field detail, not a subsystem.

## 7. Risks & Open Questions

- **Snapshot/restore semantics on Incus.** Incus snapshots can in principle be restored with a new `limits.memory` because Incus containers don't have memory baked in the way Firecracker VMs do. Today our from-snapshot rejection treats both backends identically. This is more conservative than necessary on Incus — but consistency with Firecracker is more valuable than pulling at a separate behavior thread per backend. If real users hit this, lift the restriction on Incus only.
- **Memory granularity.** Firecracker requires `MemSizeMib` to be set; we treat the request as MiB directly. A user requesting 1024 (the API says MB) gets a Firecracker VM with 1024 MiB ≈ 1.07 GB. The 5% drift is documented but never auto-converted. If user feedback indicates confusion, revisit by either (a) renaming the field with a deprecation cycle or (b) adding an explicit `memory_limit_unit` enum.
- **Default cap on memory.** The 8 GiB ceiling is conservative for "sandbox" workloads but might be too low for some legitimate uses. Operators who need more must patch the constant — we deliberately did not expose this as a flag to keep validation policy decisions out of operator hands. Revisit if real users hit it.

## 8. Delivery

Single PR, ~250 LOC across:

- `internal/domain/errors.go` (sentinel)
- `internal/api/response.go` (mapErrorCode)
- `internal/service/limits.go` (new) + `limits_test.go` (new)
- `internal/service/sandbox.go` (call validateLimits in two places)
- `internal/service/sandbox_test.go` (two new tests)
- `internal/provider/firecracker/firecracker.go` (Config fields + defaults)
- `internal/provider/firecracker/sandbox.go` (`MachineCfg` wire-up)
- `internal/provider/firecracker/storage_wiring_test.go` (defaults + plumbing tests)
- `cmd/navarisd/main.go` (two flags + config field)
- `cmd/navarisd/provider_firecracker.go` (forward to firecracker.Config)
- `cmd/navarisd/storage_test.go` (or new flags_test.go) (flag parsing test)
- `internal/api/sandbox_test.go` (two API-layer 400 tests)
- `test/integration/limits_test.go` (new)
- `internal/api/response_test.go` (extend)
- README + packaging + native-install doc updates
