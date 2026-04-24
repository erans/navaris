# CoW Sandbox Fork Design

**Status:** Draft
**Date:** 2026-04-24
**Scope:** Copy-on-write cloning of sandbox filesystems (stage 1) and running Firecracker VM memory (stage 2).

## 1. Goals

**Stage 1 — storage-efficient CoW for cold sandbox cloning.**
Replace every `copyFile(rootfs.ext4)` in the Firecracker provider with a pluggable CoW operation. The instant win is storage: N sandboxes spawned from one template share blocks instead of costing N × rootfsSize. A secondary win is latency: a reflink is microseconds vs. hundreds of milliseconds for a multi-GB copy.

Applies to four existing code paths:
- `internal/provider/firecracker/sandbox.go:506-511` — clone rootfs from snapshot on `CreateSandboxFromSnapshot`.
- `internal/provider/firecracker/snapshot.go:116-123` — stopped-mode snapshot disk copy.
- `internal/provider/firecracker/snapshot.go:169-189` — live-mode snapshot disk copy (while paused).
- Image creation path that copies a rootfs into `/var/lib/firecracker/images/`.

**Stage 2 — memory CoW fork for running Firecracker sandboxes.**
A "fork" of a running sandbox = pause parent briefly → take snapshot (memory file + reflink disk) → resume parent → restore N children, each via `MAP_PRIVATE` of the shared memory file + its own reflinked disk. Clean children of one fork-point share clean memory pages via the page cache.

## 2. Non-Goals

- **UFFD live-fork** (parent never pauses). Deferred. The fork flow leaves a seam so it can be added without touching the API or on-disk layout.
- **Changing Firecracker's rootfs model** away from a single `.ext4` file — no virtiofs, no subvolume-as-rootfs.
- **Full `BtrfsSubvolBackend` / `ZfsBackend` for Firecracker.** They are defined and stubbed in the `storage` package but not wired into the Firecracker provider: for a single-file template they duplicate `ReflinkBackend` with more complexity. Primarily retained as a seam for future non-Firecracker providers.
- **Navaris-level CoW code for Incus.** Incus already has first-class CoW via its native storage pools (btrfs/zfs/lvm-thin). Navaris only verifies the pool is CoW-capable at startup.
- **Incremental / diff memory snapshots**, **cross-host fork**, **deduplication / compression / backup** — all out of scope.

## 3. Architecture

### 3.1 `internal/storage` package

A new package owns CoW primitives. Providers call through it; they do not learn about reflinks, subvolumes, or ioctls.

```go
// internal/storage/backend.go
type Backend interface {
    Name() string
    CloneFile(ctx context.Context, src, dst string) error
    Capabilities() Capabilities
}

type Capabilities struct {
    InstantClone   bool  // O(1) metadata op, not O(size) data copy
    SharesBlocks   bool  // clones share physical blocks until written
    RequiresSameFS bool  // src and dst must be on the same filesystem
}
```

`CloneFile` must produce a standalone, writable `dst` or no `dst` at all (atomic rename from `dst.tmp`).

### 3.2 Backend implementations

| Backend | `CloneFile` mechanism | v1 wiring |
|---|---|---|
| `CopyBackend` | `io.Copy` (today's behavior) | Fallback on all providers. |
| `ReflinkBackend` | `ioctl(FICLONE)` via `golang.org/x/sys/unix.IoctlFileClone` | Wired to Firecracker provider when host FS is XFS or btrfs. |
| `BtrfsSubvolBackend` | `btrfs subvolume snapshot` | Defined, stubbed, unit-tested. Not wired to Firecracker. |
| `ZfsBackend` | `zfs clone` of a snapshot | Defined, stubbed, unit-tested. Not wired to Firecracker. |

Stubs return `Capabilities{}` with all fields false and an explicit error from `CloneFile` describing why they are not available in v1 (single-file template model). They exist so the interface is honestly general.

### 3.3 Detection & selection

At daemon startup, probe each CoW-relevant root directory (`/srv/firecracker/vms`, `/srv/firecracker/snapshots`, `/var/lib/firecracker/images`):

1. `statfs()` → filesystem type.
2. For XFS / btrfs: attempt a 1-byte reflink into a temp file in that root; on success, capability confirmed; on `EOPNOTSUPP` / `EXDEV`, fall back.
3. Record the resolved backend per root in a `storage.Registry`.

### 3.4 Configuration

```yaml
storage:
  mode: auto           # auto | copy | reflink | btrfs-subvol | zfs
  roots:               # optional per-root overrides
    /srv/firecracker/vms: auto
```

- `mode: auto` — probe; use best available per root. Probe failure is non-fatal; the affected root falls back to copy.
- `mode: <explicit>` — probe is a hard precondition. If the configured backend cannot run at a root, startup exits non-zero. Deterministic for tests/prod.
- Per-root overrides in `roots:` take precedence over the global `mode` and are evaluated under the same rules (an explicit per-root value is a hard precondition for that root; `auto` per-root probes). This lets operators mix (e.g. vms on XFS/reflink, images on a network-mounted dir/copy).

### 3.5 Wiring into the Firecracker provider

Each `copyFile(src, dst)` call site becomes:

```go
backend := s.storage.BackendFor(dst) // resolve from destination root
if err := backend.CloneFile(ctx, src, dst); err != nil { ... }
```

No other behavior changes — error handling, metadata writes, permissions are untouched. Error handling:

1. **Capability mismatch** (`EOPNOTSUPP` / `EXDEV` at op time) — log + automatic fallback to `CopyBackend` for that op; metadata records the backend that actually ran.
2. **Source missing / permission / `ENOSPC`** — returned as-is through the existing provider error path.
3. **Partial clone on error** — guaranteed avoided by the temp-file-plus-rename contract in `CloneFile`.

### 3.6 Incus integration

`internal/provider/incus/provider.go` gains a startup check only:

- Query the Incus storage pool driver (`incus storage info <pool>`).
- If the driver is `dir` (full-copy), log a warning pointing at the navaris docs for configuring a `btrfs` / `zfs` / `lvm` pool.
- Optional strict mode (config flag) refuses startup.

No code changes to the Incus clone path — Incus already does the right thing when the pool is CoW-capable.

## 4. Stage 1 Behavior & Lifecycle

### 4.1 Create sandbox from snapshot

On an XFS or btrfs host: metadata-only reflink, < 10 ms, zero additional disk consumed at t = 0. On a `dir`-mode host: current full copy, unchanged. The new `rootfs.ext4` is a standalone writable file — guest sees identical semantics to today. No guest-side changes.

### 4.2 Create snapshot (stopped or live)

Same `CloneFile` substitution for the disk-copy step. Live-mode already pauses the VM to copy the disk; with reflink that pause shrinks from "copy multi-GB file" to a metadata op. Live-mode memory snapshot (`vmstate.bin`) is unchanged — already efficient.

### 4.3 Register template / image creation

When promoting a snapshot into a reusable image under `/var/lib/firecracker/images/{ref}.ext4`, same `CloneFile` call. If the source and destination roots are on different filesystems (a user-configurable edge case), the backend reports `RequiresSameFS` incompatibility and we fall back to copy for that specific transition.

### 4.4 Destroy

A reflinked clone is just a file. `os.Remove(rootfs.ext4)` frees the blocks held only by this clone; blocks still referenced by other clones or the template remain. The filesystem handles extent refcounts — navaris keeps no CoW dependency graph. A template can be deleted while reflinked children exist; their blocks live on.

This is explicitly why ZFS is stubbed and not wired: `zfs clone` holds its parent snapshot immutable for the clone's lifetime, which would force navaris to model a lifecycle dependency graph it does not have today.

### 4.5 Disk accounting & observability

- `Size` in metadata continues to report logical size (`stat(rootfs.ext4).Size()`). Users care about logical size.
- Image and snapshot metadata JSON gain one observability-only field: `storage_backend: "reflink" | "copy" | ...`, written at clone time.
- New metrics:
  - `navaris_storage_clone_duration_seconds{backend,source_root}`
  - `navaris_storage_clone_bytes_saved_total{backend}` — approximated as "logical size of clones beyond the first".
- Existing logs gain a `storage_backend` field on clone ops.

### 4.6 Migration / backwards compatibility

Zero on-disk format changes. Existing sandboxes, snapshots, and images work unchanged. Turning the feature on for an existing deployment is a daemon restart with `storage.mode: auto`. Turning it off is a restart with `storage.mode: copy`. No dataset conversion, no one-way doors.

### 4.7 Testing

1. **Unit** — each backend's `CloneFile` against a tmp dir with a synthetic 1 MB file: content equality, post-clone divergence (write to src does not leak to dst and vice versa), cleanup on error.
2. **Integration** — Firecracker provider's four call sites, parameterized over backend, using a loopback btrfs image mounted in the test.
3. **Smoke on real hardware** — a CI job on a host with XFS reflink enabled; existing integration suite re-run with `storage.mode: reflink`.

## 5. Stage 2: Memory CoW Fork (Firecracker)

### 5.1 API

```
POST /v1/sandboxes/{id}/fork
Body: { "count": 1, "metadata": {...} }
Response: [ { "sandboxID": "...", "state": "pending" }, ... ]
```

Async; returns child IDs immediately. Creation proceeds on the existing worker dispatcher. `count` is capped by config (default 16) to bound per-request work.

### 5.2 Mechanism

A fork is three steps:

1. **Materialize a fork-point.** If the parent is running, take a live snapshot: pause → write `vmstate.bin` + disk via `storage.Backend.CloneFile` → resume. If the parent is already stopped, reuse its last snapshot.
2. **Register the fork-point** in `/srv/firecracker/forkpoints/{fpID}/` containing `vmstate.bin`, `snapshot.meta`, `rootfs.ext4`. Fork-points are distinct from user-facing snapshots — they are internal, reference-counted, and GC'd automatically.
3. **Spawn each child.** For each child:
   - `storage.Backend.CloneFile(fpDir/rootfs.ext4, childDir/rootfs.ext4)`.
   - Launch a Firecracker VM pointed at the fork-point's `vmstate.bin` and `snapshot.meta`. Firecracker opens the memory file with `mmap(MAP_PRIVATE)`; the kernel provides CoW pages. All children of one fork-point share clean pages via the page cache.

No UFFD, no custom fault handler, no write-protection dance. Kernel + Firecracker do the work.

### 5.3 Parent pause budget

Pause duration = live-snapshot time ≈ `vmstate.bin` write + reflink disk. On XFS/btrfs with reflink: dominated by `vmstate.bin` (memory-size-proportional, typically tens-to-low-hundreds of ms for 1–4 GB VMs). On dir/copy hosts: adds the full disk copy — fork responses emit a warning when the host is not CoW-capable.

### 5.4 Per-child identity

Children are byte-identical VMs at t = 0. Three pieces are re-rolled by navaris at restore time, using Firecracker's existing network config API:

- **CID** (vsock) — already unique per VM.
- **MAC address** — newly allocated per child.
- **IP + network namespace** — new tap + IP per child.

In-guest identity (hostname, SSH host keys, machine-id) is the **user's responsibility** — same as `CreateSandboxFromSnapshot` today. Documented explicitly in the public API docs.

### 5.5 Fork-point lifecycle

A fork-point has two independent lifecycle rules, tracked as two separate counters on the fork-point record:

**Rule A — spawn-phase refcount (orphan cleanup).** When a fork request is accepted, increment once per placed child; decrement when each child reaches a terminal spawn state (`running` or `failed`). When the counter reaches zero, the spawn phase is complete. This exists solely so the daemon can identify fork-points whose spawn never completed (e.g. daemon crash mid-fork) and GC them. On daemon start, any fork-point whose on-disk record indicates an incomplete spawn and whose `created_at` is older than 1 h is deleted.

**Rule B — backing-file retention (correctness).** Every running child holds a `MAP_PRIVATE` mapping of the fork-point's `vmstate.bin`; clean pages can still be page-faulted from this file at any time during the child's lifetime. The fork-point directory must therefore remain on disk as long as any descendant sandbox is alive. Concretely: track the set of live descendant sandbox IDs per fork-point; when the set becomes empty, delete the fork-point directory.

Children are otherwise fully independent sandboxes from birth — no parent-pointer in the DB exposed to API callers, no "parent destroyed → children die" coupling. Destroying the original parent sandbox is unrelated to the fork-point lifetime; destroying a child only affects Rule B.

### 5.6 UFFD seam (not implemented)

Memory CoW is a separate concern from the `storage.Backend` interface. The fork flow is structured so a future `memory.Backend` could swap `MAP_PRIVATE`-based restore for UFFD-based restore at step 3 without touching the API, the endpoint, or the fork-point on-disk layout. The child-spawn helper takes a `memoryMode` enum (`map_private` now; `uffd` future). Today it accepts only `map_private`.

### 5.7 Observability (stage 2)

- `navaris_fork_pause_duration_seconds{host_cow_capable}`
- `navaris_fork_child_spawn_duration_seconds`
- Structured log per fork request: `forkpoint_id`, `parent_id`, `children=[...]`.

### 5.8 Testing

1. **Unit** — fork-point lifecycle and refcount (in-memory), API validation.
2. **Integration (Firecracker, reflink-capable FS)** — create sandbox → write a sentinel file inside → fork 3 → verify all three see the sentinel, each diverges independently, destroying one does not affect others, destroying the parent does not affect children.
3. **Integration (copy-only host)** — same scenario, correctness only; latency regression is expected.
4. **Stress** — fork count = 16 from a 2 GB VM; assert total wall time and per-child RSS (should share most clean pages).

## 6. Risks & Open Questions

- **XFS reflink prerequisite.** XFS filesystems created without `reflink=1` at `mkfs` time cannot be converted in place. Operators upgrading from `copy` to `reflink` on XFS may need a fresh filesystem. Documented in install docs; probe detects and falls back cleanly.
- **btrfs and VM images.** btrfs has known performance quirks with VM image files (fragmentation under random writes). The reflink backend is indifferent to this — `rootfs.ext4` is still a file, and its internal behavior is unchanged. If operators see degradation they can mount with `nodatacow` on the VMs root; call out in docs.
- **Cross-mount `CloneFile`.** Reflink fails across filesystem boundaries. The startup probe tests each root independently; runtime fallback handles the rare cross-mount op.
- **Fork-point retention vs disk pressure.** A fork-point stays on disk as long as any descendant lives. For long-lived descendants this can add up. Tracked as an observability metric; TTL policy for auto-cleanup of ancient fork-points is future work.

## 7. Delivery Stages

1. **Stage 1a** — `internal/storage` package with `CopyBackend` and `ReflinkBackend`, probe, config, Firecracker provider wired through, unit + integration tests. Stub the btrfs-subvol and zfs backends but do not register them.
2. **Stage 1b** — Incus startup pool-capability check, docs.
3. **Stage 2** — fork endpoint, fork-point lifecycle, child-spawn helper with `MAP_PRIVATE` memory mode, tests.
4. **Stage 3 (future, not in this spec)** — UFFD `memory.Backend`, if a "no parent pause" requirement surfaces.
