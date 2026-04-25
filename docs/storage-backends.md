# Storage Backends

Navaris clones rootfs files via a pluggable storage backend. Stage 1 of the
copy-on-write effort introduces capability-aware cloning so that a single
template image can spawn many sandboxes without paying N × rootfs in disk.

## Modes

Selected via `--storage-mode=<mode>` on `navarisd`. Default: `auto`.

- `auto` — probe each storage root at startup; use reflink (FICLONE) when
  the host filesystem supports it (XFS with `reflink=1`, btrfs, bcachefs);
  fall back to byte copy otherwise. Probe failure is non-fatal.
- `copy` — always full copy. Largest disk footprint, no FS prerequisites.
  Use this for deterministic test harnesses or when you want to disable
  CoW deliberately.
- `reflink` — explicit reflink. Fails startup if any storage root is on a
  filesystem that doesn't support `ioctl(FICLONE)`. Use this in
  production deployments where you've provisioned the right filesystem
  and want a hard guard against silent fallback.
- `btrfs-subvol`, `zfs` — accepted by the flag parser but not wired in
  v1; they fail startup with an explicit "not supported in v1" message.
  Reserved as future seams for non-Firecracker providers and for storage
  models that operate on directories or block devices instead of files.

## Host filesystem prerequisites

| Filesystem | Reflink? | Notes |
|---|---|---|
| ext4 | No | Use `auto` (falls back to copy) or `copy`. |
| XFS | Yes when `reflink=1` is enabled at `mkfs` time. Cannot be enabled in place — needs fresh `mkfs.xfs -m reflink=1`. |
| btrfs | Yes | Native CoW. Consider `nodatacow` on the storage subvolume to avoid fragmentation when guest workloads do heavy random writes inside their rootfs images. |
| bcachefs | Yes | Recent kernels only. |
| tmpfs / NFS / "dir"-based | No | Auto mode falls back to copy. |

## Storage roots

Reflink requires src and dst to share a filesystem. Navaris's three storage
roots are configurable via daemon flags:

- `--chroot-base` (default `/srv/firecracker`) — Firecracker jail roots.
- `--image-dir` — published rootfs images (templates).
- `--snapshot-dir` (default `/srv/firecracker/snapshots`) — sandbox snapshots.

Place all three on the same filesystem to maximise CoW coverage. The
registry probes each root independently and falls back per-root, so a
network-mounted `--image-dir` next to a local btrfs `--snapshot-dir`
still works — the snapshot path uses reflink, the image path falls back
to copy.

## What gets recorded

Each snapshot's metadata (`<snapshot-dir>/<snap-id>/snapinfo.json`) and
each image's metadata (`<image-dir>/<ref>.json`) gain a `storage_backend`
field at clone time. It records which backend actually performed the clone
(observability only — nothing in the runtime depends on it).

Metrics (OTel):

- `storage.clone.duration{backend, source_root}` — histogram of per-clone wall time.
- `fork.pause.duration{host_cow_capable}` — parent-VM pause window during fork-point materialization.
- `fork.child.spawn.duration` — per-child spawn time.

## Troubleshooting

**`storage: root "...": reflink not available at ...: unsupported` at startup with `--storage-mode=reflink`**
The named root is not on a reflink-capable filesystem. Either drop to
`--storage-mode=auto`, switch to `--storage-mode=copy`, or remount/`mkfs`
the affected root.

**A clone that succeeded at startup probe but fails at op time**
Rare; typically caused by cross-mount operations the probe didn't catch
(EXDEV at runtime). The registry automatically falls back to copy for
that op. Look for `storage_backend=copy` in the per-op log to confirm.

**Disk usage doesn't shrink after switching to `reflink`**
Existing files were full copies. Reflink only saves space for files
created or cloned AFTER the switch. To rewrite an existing rootfs as a
reflinked clone, destroy and recreate the sandbox or take a fresh
snapshot.

## Incus pool capability

Incus uses its own native storage pools — navaris does NOT implement CoW
for Incus directly. The daemon checks the configured pool driver at
startup:

- `dir`, `lvm` (non-thin) → warning logged ("not CoW-capable").
- `btrfs`, `zfs`, `lvm-thin`, `ceph`, `cephfs` → CoW-capable (no warning).
- Any other driver → assumed capable (no warning); navaris doesn't second-guess.

`--incus-strict-pool-cow` upgrades the warning to a startup error.

The pool inspected is the one named `default` by default; configure via
`incus.Pool` if you use a different pool name.

## How CoW is verified in CI

Three layers run on every pull request:

1. **`integration-incus-cow`** — brings up an Incus container preseeded
   with a btrfs-driver pool, runs the integration suite against navarisd
   with `--incus-strict-pool-cow=true`. Failure = the navaris pool
   advisory regressed, or Incus's btrfs driver is broken on this host.

2. **`integration-firecracker-cow`** — mounts a btrfs loop file at
   `/srv/firecracker` inside the navarisd container and runs navarisd
   with `--storage-mode=reflink`. Failure to start = `ioctl(FICLONE)`
   was rejected on a storage root, so every clone in the suite that
   followed must have used `ReflinkBackend`.

3. **`check-reflink-sharing.sh`** runs after the integration suite
   passes (in the firecracker-cow leg). It does
   `cp --reflink=always` on a known image and inspects the result with
   `btrfs filesystem du --raw`. The clone must report shared bytes
   ≥ 80% of source size, otherwise the leg fails. This catches the
   silent-full-copy regression where the kernel returns success from
   `FICLONE` without actually deduplicating extents — something the
   strict-mode startup probe alone cannot detect.

Local reproduction is via the Makefile:

- `make integration-test-incus-cow` — Incus btrfs leg.
- `make integration-test-firecracker-cow` — Firecracker reflink leg
  (requires `/dev/kvm`).
- `make integration-env-firecracker-cow` / `integration-env-incus-cow`
  bring up the stack without the test-runner so you can poke at it
  manually.
