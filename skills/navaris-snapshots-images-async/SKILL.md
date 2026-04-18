---
name: navaris-snapshots-images-async
description: Use when the user wants to snapshot a sandbox, restore from a snapshot, promote a snapshot to a reusable base image, register an external image, or manage long-running async operations (wait-state, operation wait/get/cancel). Covers stopped vs live snapshots and the async operation lifecycle.
---

# Navaris CLI — Snapshots, Images & Async Operations

Covers snapshots, images, and the async operation surface that underpins every long-running CLI call.

## Reference

### Snapshots

| Command | Flags |
|---|---|
| `navaris snapshot create --sandbox <sandbox-id> --label <name>` | `--consistency stopped` (default) or `live`; `--wait`, `--timeout` |
| `navaris snapshot list --sandbox <id>` | — |
| `navaris snapshot get <snapshot-id>` | — |
| `navaris snapshot restore <snapshot-id>` | `--wait`, `--timeout` |
| `navaris snapshot delete <snapshot-id>` | `--wait`, `--timeout` |

`stopped` consistency requires the sandbox to be in the **stopped** state — the service enforces this and returns an error if the sandbox is running. `live` captures a running sandbox (memory + disk) and is only supported by backends that implement it (Firecracker supports live; Incus supports stateful snapshots).

### Images

| Command | Flags |
|---|---|
| `navaris image promote --snapshot <snapshot-id> --name <n> --version <v>` | `--wait`, `--timeout`; promotes a snapshot into a reusable image |
| `navaris image register --name <n> --version <v> --backend <type> --backend-ref <ref>` | Register a pre-existing external image (rootfs/kernel) |
| `navaris image list [--name <partial>]` | List images; filter by name |
| `navaris image get <image-id>` | — |
| `navaris image delete <image-id>` | `--wait`, `--timeout` |

### Operations

Every long-running call (create, start, stop, destroy, snapshot, restore) produces an operation. The CLI usually waits for it to finish (controlled by `--wait` / `--timeout` on each command); pass `--wait=false` to return immediately with the operation ID for later polling.

| Command | Flags |
|---|---|
| `navaris operation list` | `--sandbox <id>`, `--state <state>` (filter) |
| `navaris operation get <operation-id>` | — |
| `navaris operation wait <operation-id>` | `--timeout <duration>` |
| `navaris operation cancel <operation-id>` | — |

### Sandbox wait-state

```
navaris sandbox wait-state <sandbox-id> --state running [--timeout 60s] [--interval 500ms]
```

## Workflows

### 1. Safety net: snapshot → upgrade → restore on failure

```bash
SNAP_ID=$(navaris snapshot create --sandbox "$SANDBOX_ID" --label pre-upgrade --wait --output json | jq -r '.SnapshotID')
if ! navaris sandbox exec "$SANDBOX_ID" -- ./upgrade.sh; then
    echo "upgrade failed, restoring..." >&2
    navaris snapshot restore "$SNAP_ID" --wait
fi
```

### 2. Promote a working snapshot to a reusable base image

```bash
# after verifying the sandbox is in a good state
SNAP_ID=$(navaris snapshot create --sandbox "$SANDBOX_ID" --label base-candidate --wait --output json | jq -r '.SnapshotID')
IMAGE_ID=$(navaris image promote \
    --snapshot "$SNAP_ID" \
    --name my-service-base \
    --version 2026.04 \
    --wait --output json | jq -r '.ImageID')
# new sandboxes can now use --image my-service-base
```

### 3. Register an externally built rootfs/kernel as a Firecracker image

```bash
navaris image register \
  --name debian-minimal \
  --version 12.5 \
  --backend firecracker \
  --backend-ref /var/lib/firecracker/images/debian-12.5.rootfs
```

Use this when you build rootfs images yourself (outside navaris) and want them addressable by image reference.

### 4. Fire-and-forget async with polling and timeout

```bash
OP_ID=$(navaris sandbox create \
    --name long-boot \
    --image alpine-3.21 \
    --wait=false --output json | jq -r '.OperationID')
# do other work...
if ! navaris operation wait "$OP_ID" --timeout 5m; then
    echo "operation did not finish in 5m, cancelling" >&2
    navaris operation cancel "$OP_ID"
fi
```

## Common errors

Cobra's `SilenceErrors: true` setting plus a bare `os.Exit(1)` in `cmd/navaris/main.go` means client errors are not currently printed to stderr. Check `$?` to confirm the command failed, and read the daemon logs for the underlying cause. The symptom strings below are what the client *would* render if the suppression were lifted.

| Symptom | Cause | Fix |
|---|---|---|
| `sandbox must be stopped for stopped-consistency snapshot` | `snapshot create` called while the sandbox is running with the default `--consistency stopped` | Stop the sandbox first (`navaris sandbox stop <id> --wait`), then snapshot; or pass `--consistency live` if your backend supports it |
| `operation <id> failed: ...` after `image delete` | Backend rejected the delete (e.g. backend reference missing or I/O error) | `navaris operation get <id>` to read the wrapped error text; inspect backend logs for the root cause |
| `operation stuck in pending` | Worker backlog or dependency waiting | `navaris operation get <id>` to inspect; `navaris operation list --state running` to see what's busy |
| `snapshot restore` times out | Restore is longer than `--timeout` | Pass `--wait=false` and poll with `operation wait` at a longer timeout |
| `image register` succeeds but sandbox create fails to find it | `--backend` value doesn't match the backend navarisd is configured to use, or `--backend-ref` path is not reachable by the daemon | Verify the backend type (`incus` or `firecracker`) and that the path exists on the host running navarisd |
