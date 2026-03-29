# Firecracker Phase 2: Snapshots, Images, and Port Forwarding

## Goal

Implement the 8 stubbed provider operations plus `CreateSandboxFromSnapshot`, giving the Firecracker backend full parity with Incus for snapshots, images, and port forwarding.

## Success Criteria

All integration tests pass against Firecracker (remove `NAVARIS_SKIP_SNAPSHOTS` and `NAVARIS_SKIP_PORTS` from the compose file). The e2e lifecycle test runs the full path including snapshot, from-snapshot, and port operations.

## Overview

Three subsystems, all using file-based storage with JSON metadata:

1. **Snapshots** — file copy of rootfs + optional Firecracker memory snapshot for live mode
2. **Images** — rootfs copied from snapshot, stored in image dir with metadata
3. **Port forwarding** — iptables DNAT/SNAT rules on the host tap interface

---

## 1. Snapshots

### Storage Layout

```
<snapshot-dir>/<snapshot-id>/
├── rootfs.ext4          # Copy of VM's rootfs at snapshot time
├── vmstate.bin          # Firecracker memory snapshot (live mode only)
├── snapshot.meta        # Firecracker snapshot metadata (live mode only)
└── snapinfo.json        # Navaris metadata
```

`snapinfo.json` schema:
```json
{
  "id": "snapshot-id",
  "source_vm": "nvrs-fc-abc12345",
  "label": "user-label",
  "mode": "stopped|live",
  "created_at": "2026-03-29T12:00:00Z"
}
```

### CreateSnapshot

**Stopped mode (`ConsistencyStopped`):**
1. VM must be stopped (service layer enforces this).
2. Create snapshot directory under `<snapshot-dir>/<snapshot-id>/`.
3. Copy the VM's `rootfs.ext4` to the snapshot directory.
4. Write `snapinfo.json` with `mode: "stopped"`.
5. Return `BackendRef{Ref: snapshot-id}`.

**Live mode (`ConsistencyLive`):**
1. VM must be running.
2. Connect to the VM's Firecracker API socket via `fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})`.
3. Pause the VM: `machine.PauseVM(ctx)`.
4. Create Firecracker snapshot: `machine.CreateSnapshot(ctx, memFilePath, snapshotPath)` — writes `vmstate.bin` and `snapshot.meta` inside the VM directory.
5. Copy `rootfs.ext4` from VM dir to snapshot dir (disk is consistent since VM is paused).
6. Move `vmstate.bin` and `snapshot.meta` from VM dir to snapshot dir.
7. Resume the VM: `machine.ResumeVM(ctx)`.
8. Write `snapinfo.json` with `mode: "live"`.
9. Return `BackendRef{Ref: snapshot-id}`.

If any step after pause fails, resume the VM before returning the error.

### RestoreSnapshot

VM must be stopped. Two paths based on snapshot mode:

**Stopped snapshot:**
1. Copy snapshot's `rootfs.ext4` over the VM's `rootfs.ext4`.
2. Done. Next `StartSandbox` boots the VM normally from the restored disk.

**Live snapshot:**
1. Copy snapshot's `rootfs.ext4` to the VM directory.
2. Copy snapshot's `vmstate.bin` and `snapshot.meta` to the VM directory.
3. Update `vminfo.json` to set a `RestoreFromSnapshot: true` flag.
4. Next `StartSandbox` detects the flag and uses `fcsdk.WithSnapshot(memFilePath, snapshotPath)` instead of normal boot, then clears the flag. The VM resumes from the exact memory state captured at snapshot time.

### DeleteSnapshot

Remove the snapshot directory. Return `nil` if already absent.

### Recovery

On provider init, scan `<snapshot-dir>/` for `snapinfo.json` files. No runtime state to recover — snapshots are just files.

### Configuration

New CLI flag: `--snapshot-dir` (default: `/srv/firecracker/snapshots`). Added to `ProviderConfig`.

---

## 2. Images

### Storage Layout

```
<image-dir>/<image-ref>/
└── rootfs.ext4          # The image file
```

Plus an `imageinfo.json` alongside:
```
<image-dir>/<image-ref>.json
```

```json
{
  "ref": "image-ref",
  "name": "my-image",
  "version": "1.0",
  "architecture": "x86_64",
  "size": 536870912,
  "source_snapshot": "snapshot-id"
}
```

Note: this keeps the flat-file convention established in Phase 1, where `ImageRef` resolves to `filepath.Join(imageDir, imageRef+".ext4")`. The metadata file sits alongside at `imageRef+".json"`.

### PublishSnapshotAsImage

1. Generate an image ref (e.g., `img-<uuid-prefix>`).
2. Copy snapshot's `rootfs.ext4` to `<image-dir>/<image-ref>.ext4`.
3. Get file size of the copied ext4.
4. Get architecture via `runtime.GOARCH` (all VMs share host arch).
5. Write `<image-dir>/<image-ref>.json` with metadata.
6. Return `BackendRef{Ref: image-ref}`.

### GetImageInfo

Read `<image-dir>/<image-ref>.json`. Return `ImageInfo{Architecture, Size}`.

### DeleteImage

Remove `<image-dir>/<image-ref>.ext4` and `<image-dir>/<image-ref>.json`.

### CreateSandboxFromSnapshot

Currently stubbed in `sandbox.go`. The service layer passes the snapshot's `BackendRef`.

1. Generate new VM ID, create VM directory.
2. Copy snapshot's `rootfs.ext4` to `<vmDir>/rootfs.ext4`.
3. Allocate CID and UID, write `vminfo.json`.
4. Return `BackendRef{Ref: vmID}`.
5. The service layer then calls `StartSandbox`, which boots the VM normally from the copied rootfs.

Note: even for live snapshots, `CreateSandboxFromSnapshot` always boots fresh. Live restore (resuming from memory state) only makes sense for `RestoreSnapshot` on the same VM.

---

## 3. Port Forwarding

### Mechanism

iptables DNAT + SNAT rules forwarding host ports to guest IPs on the tap network.

### Port Allocation

Sequential from range 40000-49999. In-memory map tracked by the provider, recovered from `vminfo.json` on init.

### PublishPort

1. Allocate next available host port from the 40000-49999 range.
2. Read VM's `vminfo.json` to get `SubnetIdx`, derive guest IP via `subnets.GuestIP(subnetIdx)`.
3. Add iptables rules:

```bash
# External traffic: forward to guest
iptables -t nat -A PREROUTING -p tcp --dport <hostPort> -j DNAT --to-destination <guestIP>:<targetPort>
# Local traffic: forward to guest (connections from the host itself)
iptables -t nat -A OUTPUT -p tcp -o lo --dport <hostPort> -j DNAT --to-destination <guestIP>:<targetPort>
# Allow forwarded traffic through
iptables -A FORWARD -p tcp -d <guestIP> --dport <targetPort> -j ACCEPT
```

4. Update `vminfo.json`: add to `Ports` map (`publishedPort → targetPort`).
5. Return `PublishedEndpoint{HostAddress: "0.0.0.0", PublishedPort: hostPort}`.

### UnpublishPort

1. Read VM's `vminfo.json` to get guest IP and the target port for this published port.
2. Remove the three iptables rules (same commands with `-D` instead of `-A`).
3. Remove entry from `Ports` map in `vminfo.json`.
4. Release host port back to allocator.

### VMInfo Changes

Add a `Ports` field to `VMInfo`:

```go
type VMInfo struct {
    // ... existing fields ...
    Ports map[int]int `json:"ports,omitempty"` // publishedPort → targetPort
}
```

`ClearRuntime()` should also clear `Ports` (ports are torn down when VM stops).

### Port Allocator

New type in `network/` package:

```go
type PortAllocator struct {
    mu   sync.Mutex
    used map[int]bool
    next int // starts at 40000
}

func (a *PortAllocator) Allocate() (int, error)  // returns next free port
func (a *PortAllocator) Release(port int)         // returns port to pool
```

### Recovery

On provider init, scan all `vminfo.json` files. For running VMs with `Ports`, re-establish iptables rules and mark ports as used in the allocator.

### Cleanup

The service layer already calls `UnpublishPort` for each port before `DestroySandbox`. No extra work needed in the provider. `ClearRuntime()` clears the Ports map when the VM stops.

---

## 4. StartSandbox Changes

`StartSandbox` needs a small addition for live snapshot restore:

1. Check `vminfo.json` for `RestoreFromSnapshot` flag.
2. If set: use `fcsdk.NewMachine(ctx, cfg, fcsdk.WithSnapshot(memPath, snapPath))` instead of normal boot. After successful start, clear the flag and delete the snapshot files from the VM directory.
3. If not set: boot normally (existing path).

---

## Files to Create/Modify

| File | Action |
|------|--------|
| `internal/provider/firecracker/snapshot.go` | Create — CreateSnapshot, RestoreSnapshot, DeleteSnapshot, snapshot helpers |
| `internal/provider/firecracker/image.go` | Create — PublishSnapshotAsImage, GetImageInfo, DeleteImage |
| `internal/provider/firecracker/port.go` | Create — PublishPort, UnpublishPort |
| `internal/provider/firecracker/network/port_allocator.go` | Create — PortAllocator type |
| `internal/provider/firecracker/network/dnat.go` | Create — iptables DNAT/SNAT rule helpers |
| `internal/provider/firecracker/stubs.go` | Delete — all methods move to their own files |
| `internal/provider/firecracker/sandbox.go` | Modify — implement CreateSandboxFromSnapshot, add snapshot restore to StartSandbox |
| `internal/provider/firecracker/vminfo.go` | Modify — add Ports and RestoreFromSnapshot fields |
| `internal/provider/firecracker/firecracker.go` | Modify — add snapshotDir config, port allocator init, snapshot recovery |
| `cmd/navarisd/provider_firecracker.go` | Modify — add --snapshot-dir flag |
| `docker-compose.integration-firecracker.yml` | Modify — remove NAVARIS_SKIP_SNAPSHOTS and NAVARIS_SKIP_PORTS |
| `test/integration/e2e_test.go` | Modify — remove snapshot skip guard |
| `test/integration/snapshot_test.go` | Modify — remove snapshot skip guard |
| `test/integration/image_test.go` | Modify — remove snapshot skip guard |
| `test/integration/port_test.go` | Modify — remove port skip guard |
