# Sandbox Fork

Stage 2 adds memory + disk copy-on-write forking for running Firecracker
sandboxes. Use it to spawn N children from a single parent in well under
a second per child on a CoW-capable host.

## API

```
POST /v1/sandboxes/{id}/fork
Content-Type: application/json

{ "count": 3 }
```

Response: an Operation envelope (HTTP 202). The fork is asynchronous —
each child sandbox is created in `pending` state and transitions to
`starting` and then `running` on the same worker dispatcher used by
ordinary creates.

Children appear via `GET /v1/sandboxes`. To find children of a specific
parent, filter by `metadata.fork_parent_id == <parentID>` (the field is
set when the child is enqueued).

The Go SDK wraps this as `client.Fork(ctx, parentID, count)`.

## How it works

1. The parent VM is paused briefly. Firecracker writes its memory state
   to `vmstate.bin` in an internal "fork-point" directory under
   `/srv/firecracker/forkpoints/<fp-id>/`. The parent's rootfs is reflinked
   into the same directory.
2. The parent is resumed. On a reflink-capable host, the pause is
   memory-size-proportional (typically tens to low hundreds of milliseconds
   for a 1–4 GB VM) plus a metadata-only disk reflink.
3. Each child is spawned as a fresh Firecracker microVM that restores
   from the fork-point's `vmstate.bin` and `snapshot.meta`. Firecracker
   opens `vmstate.bin` with `mmap(MAP_PRIVATE)`; the kernel handles
   per-page CoW automatically — clean memory pages stay shared via the
   page cache across all siblings of one fork-point.

The fork-point directory is reference-counted: it stays on disk as long
as any descendant sandbox is alive (clean memory pages may still be
page-faulted from the backing file at any time during a child's
lifetime), and is GC'd when the last descendant is destroyed. A daemon
restart sweeps orphans (TTL 1 h for fork-points whose spawn phase never
completed) and unreferenced fork-points (no descendants and no spawns
pending).

## Per-child identity

At the moment of fork, all children are byte-identical VMs. Three things
are re-rolled by navaris before each child runs its first instruction:

- **CID, MAC, IP, network namespace** — each child gets a unique
  per-VM allocation, just like a regular `CreateSandboxFromSnapshot`.
- **Hostname, machine-id, SSH host keys, in-guest randomness** — the
  caller's responsibility. Treat fork like any other restore: regenerate
  in-guest identity in your sandbox init script if you need it distinct.

## Latency expectations

| Host filesystem | Pause window | Per-child spawn |
|---|---|---|
| XFS reflink / btrfs / bcachefs | `vmstate.bin` write only (≈100–300 ms / GB of RAM) | Restore + reflink, sub-second |
| ext4 / dir / network FS | `vmstate.bin` write + full disk copy (multi-second) | Restore + copy (multi-second) |

The `fork.pause.duration{host_cow_capable=true|false}` metric exposes
the actual numbers in production.

## Limits

- Per-fork hard cap: 64 children (provider-side guard in firecracker).
- API-level default: callers may request larger counts; the firecracker
  provider rejects above 64.
- Fork is supported only by the Firecracker provider. The Incus provider
  returns `domain.ErrNotSupported` (containers don't have VM memory to
  CoW). Mixed-backend sandboxes ignore fork on the Incus side.
- Memory-CoW currently uses `MAP_PRIVATE`. UFFD-based no-pause forks
  (`memoryMode=uffd`) are not implemented — the pause is brief on
  CoW-capable hosts, and UFFD is reserved for a future stage if a
  no-pause requirement surfaces.
