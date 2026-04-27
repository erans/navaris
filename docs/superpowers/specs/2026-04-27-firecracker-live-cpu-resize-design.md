# Firecracker Live CPU Resize Design

**Status:** Draft
**Date:** 2026-04-27
**Cross-reference:** completes the deferred work in [2026-04-25-runtime-resource-resize-design.md](2026-04-25-runtime-resource-resize-design.md) §3.5 ("CPU resize lands in a follow-up spec").

## 1. Background

Three prior specs in the resource-management series:

- [Resource limits](2026-04-25-resource-limits-design.md): `cpu_limit` and `memory_limit_mb` honored at create time. FC boots with `vcpu_count = limit` and `mem_size_mib = limit`.
- [Runtime resource resize](2026-04-25-runtime-resource-resize-design.md): `PATCH /v1/sandboxes/{id}/resources` on a running sandbox. Memory works via the virtio-balloon device. **CPU on running FC is rejected** with `ResizeReasonCPUUnsupportedByBackend`; the spec deferred the work to a "follow-up spec that bumps the SDK."
- [Time-bounded boost](2026-04-26-sandbox-boost-design.md) and [In-sandbox boost channel](2026-04-26-in-sandbox-boost-channel-design.md): operator and guest paths to call into resize. Both inherit the FC CPU rejection.

The runtime-resize spec also introduced **memory headroom**: `--firecracker-mem-headroom-mult` lets a VM boot with extra memory beyond the user's limit, with a balloon pre-inflated at boot so the live limit is enforced. This pattern enables grow boosts within the boot ceiling. The companion `--firecracker-vcpu-headroom-mult` flag exists with identical semantics, but **the matching enforcement is missing**: when `vcpu_headroom_mult > 1.0`, FC boots with extra vCPUs, but `LimitCPU` is recorded only — never enforced. A sandbox created with `cpu_limit=2, vcpu_headroom_mult=2.0` boots with 4 vCPUs and can use 400% host CPU. This is a quiet bug.

The original spec assumed live vCPU resize would arrive via FC SDK upgrade. **It will not** — Firecracker the VMM has no live vCPU hot-plug path in any SDK version. `vcpu_count` is set once via `PUT /machine-config` before `InstanceStart` and cannot be patched on a running VM.

## 2. Scope

This spec adds:

1. A per-VM Linux cgroup that enforces a CPU bandwidth limit on the FC process (and its vCPU threads), in both jailer and non-jailer modes.
2. Initial cgroup setup at sandbox start, sized to `LimitCPU * period`.
3. Live update of the cgroup quota when `UpdateResources` (and therefore `BoostService.Start`) runs against a Firecracker sandbox with a non-nil `CPULimit`.
4. Validation against the boot-time vCPU ceiling, mirroring memory exactly.
5. Cgroup teardown on sandbox destroy.

The change closes a behavioral gap (FC CPU resize unblocked) and a correctness gap (`LimitCPU` actually enforced when `vcpu_headroom_mult > 1.0`).

## 2.1 Out of scope

- **`POST /v1/sandboxes/{id}/resize-with-restart`** — a stop-and-restart path that would change the guest-visible vCPU count. Useful for rare cases where a workload genuinely needs `nproc` to change, but multi-second downtime + process kill makes it a separate decision. Tracked as a future spec.
- **Sub-core (fractional) CPU limits** — `cpu_limit_millicores` or similar. The existing API accepts integer cores; a millicores axis can be added later without breaking back-compat (it slots in next to `cpu_limit`).
- **CPU pinning / NUMA awareness** — `cpuset.cpus` to bind a VM to specific physical cores. Orthogonal feature; valuable but out of scope here.
- **Memory cgroup enforcement** — FC's balloon already covers live memory. Adding cgroup memory.max would be redundant and potentially conflict with the balloon's accounting.

## 3. Design

### 3.1 Cgroup mechanics

Linux cgroup CPU bandwidth (CFS quota/period) limits how much CPU **time** a cgroup of processes can consume per scheduling period, regardless of how many vCPUs the guest sees.

| Concept | cgroup v2 | cgroup v1 |
|---|---|---|
| Quota file | `cpu.max` | `cpu.cfs_quota_us` |
| Period file | (same line as quota) | `cpu.cfs_period_us` |
| Quota format | `<quota> <period>` (or `max <period>`) | integer microseconds |
| Period default | 100_000 µs | 100_000 µs |

Quota math: `quota = LimitCPU * period`. So `LimitCPU = 2, period = 100000` → `cpu.max = 200000 100000` (200% = 2 cores worth of time).

The `Provider.cgroupVersion` field already detects v1 vs v2 at provider construction (`firecracker.go:392`). The new cgroup helpers branch on this once.

### 3.2 Cgroup paths

**Non-jailer mode** (CI, dev): the daemon owns the cgroup tree. Path:

```
v2:  <CgroupRoot>/<vmID>/cpu.max
v1:  <CgroupRoot>/cpu/<vmID>/cpu.cfs_quota_us
```

`CgroupRoot` is a new `firecracker.Config` field, default `/sys/fs/cgroup/navaris-fc`. It's a flag (`--firecracker-cgroup-root`) so operators on systems with a non-default cgroup mount can adjust.

**Jailer mode** (production): the FC SDK Jailer creates `firecracker/<vm-id>/` under the cgroup root by default (`JailerCfg.CgroupArgs` is the SDK escape hatch for setting initial values). Path:

```
v2:  /sys/fs/cgroup/firecracker/<vmID>/cpu.max
v1:  /sys/fs/cgroup/cpu/firecracker/<vmID>/cpu.cfs_quota_us
```

Both modes converge on the same operation for live resize: open the quota file, write the new quota.

### 3.3 Boot-time setup

**Non-jailer:** after `machine.Start()` succeeds and we have `pid := machine.PID()`:

1. `os.MkdirAll(<CgroupRoot>/<vmID>, 0755)`.
2. **(v2 only)** Idempotently enable the `cpu` controller on the parent: write `+cpu` to `<CgroupRoot>/cgroup.subtree_control`. Tolerate `EBUSY`/"already enabled" errors. Required so child cgroups can have `cpu.max`.
3. Write `pid` to `<CgroupRoot>/<vmID>/cgroup.procs`. All FC threads (incl. vCPUs) inherit by default.
4. Write the initial `cpu.max` (or `cfs_quota_us`/`cfs_period_us`) per `LimitCPU * period`.

If any of steps 1–4 fail, **log a warning and proceed**. Set `info.CgroupActive = false` on `VMInfo`. The sandbox starts; live CPU resize on it returns `ResizeReasonCgroupUnavailable`. Boot-time enforcement is best-effort because in some sandboxed CI environments the daemon may lack permission to create cgroups, and we don't want to break sandbox creation over what's ultimately a "limits not enforced" issue (the same situation as today). On success, set `info.CgroupActive = true`.

**Jailer:** pass the initial limit via `JailerCfg.CgroupArgs` so the limit is applied by the jailer before the FC binary is exec'd. Format:

```go
v2: []string{fmt.Sprintf("cpu.max=%d %d", quota, period)}
v1: []string{
    fmt.Sprintf("cpu.cfs_quota_us=%d", quota),
    fmt.Sprintf("cpu.cfs_period_us=%d", period),
}
```

Jailer setup failures fail the sandbox start (existing behavior); no new error path needed.

### 3.4 Live resize (the meat of the change)

In `internal/provider/firecracker/sandbox_resize.go::UpdateResources`:

```go
if req.CPULimit != nil {
    // 1. Look up info; if !ok return ErrNotFound.
    // 2. ceiling := info.CeilingCPU; if ceiling==0 (pre-headroom sandbox), ceiling = info.VcpuCount.
    // 3. if int64(*req.CPULimit) > ceiling: return ProviderResizeError(reason=exceeds_ceiling).
    // 4. if !info.CgroupActive: return ProviderResizeError(reason=cgroup_unavailable).
    // 5. quota := int64(*req.CPULimit) * cpuPeriod.
    // 6. err := p.writeCPUMax(p.cgroupCPUDir(vmID), quota, cpuPeriod).
    //    On error, wrap as ProviderResizeError(reason=cgroup_write_failed).
    // 7. p.vmMu.Lock(); info.LimitCPU = int64(*req.CPULimit); p.vmMu.Unlock();
    //    info.Write(p.vmInfoPath(vmID))
}
// Existing memory path unchanged.
```

The CPU branch is independent of the memory branch; both can be set in the same request. Memory failure does not roll back CPU and vice versa — but the service layer already wraps both in a single-resource-at-a-time PATCH (see `internal/service/sandbox_resize.go`), so in practice each call only sets one.

`cpuPeriod` is a package constant (100_000 µs).

### 3.5 Cleanup on destroy

**Non-jailer:** in `DestroySandbox`, after FC has exited cleanly, `os.Remove(<CgroupRoot>/<vmID>)`. Failure is logged but does not fail destroy. A periodic GC pass (out of scope) could clean stragglers from crashed VMs.

**Jailer:** the jailer's own teardown removes its cgroup subtree; no navarisd action needed.

### 3.6 Recovery (daemon restart)

A live cgroup persists across navarisd restarts as long as the FC process holding pids in `cgroup.procs` is alive. Existing `recover()` rebuilds `VMInfo` from `vminfo.json` on disk; we add no new recovery work for cgroups.

If a navarisd version older than this spec wrote `vminfo.json` without `CgroupActive`, recovery treats it as `false` (zero value) — live CPU resize on those VMs returns `cgroup_unavailable` until they're restarted. Acceptable.

### 3.7 Error semantics

| Reason (new or existing) | When | HTTP |
|---|---|---|
| `ResizeReasonExceedsCeiling` (existing) | `cpu_limit > info.CeilingCPU` | 409 |
| `ResizeReasonCgroupUnavailable` (NEW) | `info.CgroupActive == false` (boot-time setup failed, or pre-spec sandbox) | 503 |
| `ResizeReasonCgroupWriteFailed` (NEW) | Write to `cpu.max` returns EIO/EACCES at resize time | 500 |
| (removed) `ResizeReasonCPUUnsupportedByBackend` | n/a — replaced by the above | — |

`ResizeReasonCPUUnsupportedByBackend` stays in `internal/domain/errors.go` as a constant (other providers may use it in the future), but FC stops returning it.

### 3.8 Configuration

| Flag | Default | Notes |
|---|---|---|
| `--firecracker-cgroup-root` (NEW) | `/sys/fs/cgroup/navaris-fc` | Non-jailer only. Operators can change to e.g. `/sys/fs/cgroup/sandbox` to integrate with their host's cgroup layout. |
| `--firecracker-vcpu-headroom-mult` (existing) | `1.0` | When `> 1.0`, VMs boot with extra vCPUs; live grow boosts up to that ceiling work. **This flag has been a no-op for CPU enforcement until this spec.** |

Only one new flag.

### 3.9 Domain / API surface

Zero changes to `domain.UpdateResourcesRequest`, `service.UpdateResourcesOpts`, or the API request shape. Callers that previously got 409 + `cpu_unsupported_by_backend` on a running FC sandbox now get 200 (or 409 + `exceeds_ceiling` if they asked for too much).

`VMInfo` gains one field:

```go
CgroupActive bool `json:"cgroup_active,omitempty"` // false if boot-time cgroup setup failed
```

## 4. Validation & edge cases

- **Resize on stopped FC:** existing behavior — write to persisted `sandboxes.cpu_limit`, applied at next start. No cgroup operation.
- **Headroom = 1.0 (default):** `CeilingCPU == LimitCPU` after boot. Live grow is rejected (`exceeds_ceiling`). Live shrink works (`LimitCPU=1` → 100% throttle on the same vCPU count). Same as memory today.
- **Race between boot-time cgroup setup and the first vCPU thread spawning:** non-jailer mode writes the FC PID to `cgroup.procs` after `machine.Start()` returns, which means there's a small window where vCPU threads may have already spawned outside the cgroup. We accept this — vCPU threads are descendants of the FC process, and cgroup membership is inherited at clone-time, but moving the parent `pid` into the cgroup also moves all existing threads (kernel's [migration logic](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html#processes)). Worst case: a few µs of unrestricted CPU at boot — negligible. Jailer mode avoids this entirely (cgroup is set before exec).
- **Two operators racing on resize:** existing service-layer locking + persistence behavior is unchanged.
- **Sandbox forks (CoW):** child sandbox inherits the parent's `LimitCPU` and `CeilingCPU` via the existing fork copy logic in `service.SandboxService.Fork`. Each fork gets its own cgroup at boot (new `<vmID>`).
- **Jailer + non-default cgroup root:** the jailer SDK accepts `--cgroup-base-dir` if needed. We do **not** expose this via a flag now; jailer's default (`<chroot-base>/<jailer-id>/firecracker/<vm-id>`) is fine for the operator deployment target.

## 5. Testing

### 5.1 Unit tests (new)

- `internal/provider/firecracker/cgroup_test.go`:
  - `TestCgroupPath_NoJailer_v1` / `_v2` — verify path math.
  - `TestCgroupPath_Jailer_v1` / `_v2`.
  - `TestSetupCgroup_NonJailer_WritesQuota` — uses `t.TempDir()` as a fake cgroup root, verifies the directory is created, `cgroup.subtree_control` is written (idempotent on re-run), `cgroup.procs` and `cpu.max` reflect the inputs.
  - `TestWriteCPUMax_v2` / `_v1` — direct file format checks.
  - `TestSetupCgroup_PermissionDenied_Tolerated` — verify the helper logs and returns a sentinel error rather than panicking when the cgroup root is read-only.

- `internal/provider/firecracker/sandbox_resize_test.go` (modify):
  - Existing `TestUpdateResources_CPU_Rejected_OnFC` → flip to `TestUpdateResources_CPU_AppliedViaCgroup`. Asserts the cgroup file (in a tempdir-rooted Provider) is written with the new quota.
  - Add `TestUpdateResources_CPU_ExceedsCeiling` — request `LimitCPU > CeilingCPU`, expect `ResizeReasonExceedsCeiling`.
  - Add `TestUpdateResources_CPU_NoCgroup_ReturnsUnavailable` — set `info.CgroupActive=false`, expect `ResizeReasonCgroupUnavailable`.

### 5.2 Integration tests

- `test/integration/limits_test.go` — extend existing `TestSandbox_HonorsRequestedMemoryLimit` pattern with `TestSandbox_HonorsRequestedCPULimit` (FC only): create a sandbox with `cpu_limit=2`, run `cat /sys/fs/cgroup/cpu.max` from inside the guest (FC mounts cgroupfs by default), assert it shows `200000 100000`.

- `test/integration/boost_test.go` (or `boost_e2e_test.go`) — add `TestBoost_FC_CPU_AppliesToGuest` (FC only, no env gate needed): create with `cpu_limit=1`, boost to `cpu_limit=2`, assert guest's `cpu.max` reflects the change. Cancel and assert revert. Mirrors the memory test from PR #17.

- `test/integration/boost_e2e_local_test.go` — the previously-local-only `TestBoost_E2E_Incus_CPU_VisibleInGuest` test stays as-is. The Incus side is independent of this work.

### 5.3 What's not testable

- Actual host CPU consumption under load — requires synthetic CPU pressure from inside the guest plus a mechanism to measure host time slice (e.g., `cpu.stat`'s `usage_usec`). Possible to add later; not table stakes for "the limit is wired."

## 6. Migration

Database / schema: **no changes**. `VMInfo.CgroupActive` is a JSON-tagged Go struct field, not a SQLite column.

Existing on-disk `vminfo.json` files (pre-spec): on first read by the new code, `CgroupActive` deserializes as `false`. Affected sandboxes are still functional; live CPU resize on them returns 503 until they're restarted, at which point the new boot-time setup kicks in. No backfill required.

## 7. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Cgroup permission denied on docker-in-docker CI | Best-effort setup at boot; sandbox still starts. Live CPU resize cleanly returns `cgroup_unavailable` (503). CI integration test for CPU limit application is already gated on FC only (where it works). |
| Operator's host has cgroup v1 (rare in 2026) | v1 path implemented and tested. |
| FC SDK changes the jailer cgroup path | The path is constructed from `firecracker/<vm-id>` — we hard-code the same convention. SDK upgrade in a future spec would re-verify. |
| Race between setup PID write and vCPU thread spawn | Non-jailer only; window is microseconds; documented. Jailer mode is race-free. |

## 8. Documentation updates

- `README.md`: remove "FC CPU resize on running VMs not supported" caveat from the boost feature line; add note that `cpu_limit` is enforced via cgroup CPU bandwidth on FC and via `limits.cpu` cgroup on Incus.
- `docs/superpowers/specs/2026-04-25-runtime-resource-resize-design.md`: add a one-line "see [...]-firecracker-live-cpu-resize-design.md" cross-reference at §3.5 (the section that originally deferred this).
- This file (`2026-04-27-firecracker-live-cpu-resize-design.md`): committed as part of the same PR that lands the implementation.
