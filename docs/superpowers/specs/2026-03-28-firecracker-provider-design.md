# Firecracker Provider — Phase 1 Design Spec

## Goal

Add a Firecracker-based provider to navaris that implements `domain.Provider`, giving sandboxes hardware-level microVM isolation while maintaining feature parity with the Incus provider for core operations (VM lifecycle, exec, sessions). Phase 2 (separate spec) will cover snapshots, images, and port publishing.

## Motivation

- **Isolation**: Firecracker microVMs provide hardware virtualization boundaries (KVM), stronger than container-based isolation from Incus
- **Fast boot**: Firecracker boots in ~125ms, suitable for ephemeral sandbox workloads
- **Deployment flexibility**: Some environments can run Firecracker but not Incus

## Scope — Phase 1

**In scope:**
- Firecracker provider implementing `domain.Provider`
- VM lifecycle: create, start, stop, destroy, get state (full domain state machine compliance)
- Custom Go guest agent communicating over vsock
- Exec, interactive exec, and shell sessions via guest agent
- Basic networking: tap devices with configurable internet access respecting `NetworkMode`
- Jailer integration for chroot, cgroup, and UID isolation
- Build-tag gating (`//go:build firecracker`)
- Crash-safe resource recovery on restart

**Out of scope (Phase 2):**
- Snapshot create/restore/delete
- Image publish/promote/delete
- Port publishing (host port forwarding to VM)
- OCI image conversion
- Multi-node / remote Firecracker hosts

---

## Architecture

### Package Layout

```
internal/provider/firecracker/
  firecracker.go      — Provider struct, New(), Health(), config, recovery
  sandbox.go          — Create/Start/Stop/Destroy/GetState
  exec.go             — Exec/ExecDetached/AttachSession (delegates to vsock client)
  stubs.go            — Phase 2 stubs returning ErrNotImplemented

internal/provider/firecracker/vsock/
  client.go           — Host-side vsock client: dial, exec request/response, session mux
  protocol.go         — Shared message types (host client + guest agent)

internal/provider/firecracker/network/
  tap.go              — Create/delete tap devices, assign IPs from /30 pool
  iptables.go         — Masquerade rule for internet access

internal/provider/firecracker/jailer/
  jailer.go           — Jailer config: chroot, cgroup, seccomp, UID/GID

cmd/navaris-agent/
  main.go             — Guest agent entry point
  agent/
    server.go         — vsock listener, request dispatcher
    exec.go           — Command execution with PTY support
    session.go        — Interactive shell sessions
```

### Build Tags & Wiring

Build-gated with `//go:build firecracker`, same pattern as Incus.

New files:
- `cmd/navarisd/provider_firecracker.go` (`//go:build firecracker`) — implements `newFirecrackerProvider(cfg) (domain.Provider, error)` calling `firecracker.New(cfg)`
- `cmd/navarisd/provider_firecracker_stub.go` (`//go:build !firecracker`) — stub returning error

Existing files updated:
- `cmd/navarisd/provider_mock.go` — build tag changes to `//go:build !incus && !firecracker`
- `cmd/navarisd/main.go` — provider selection block extended:

```go
// Provider selection (runtime, gated by build tags)
var prov domain.Provider
switch {
case cfg.firecrackerBin != "":
    p, err := newFirecrackerProvider(cfg)
    if err != nil {
        return fmt.Errorf("firecracker provider: %w", err)
    }
    prov = p
    logger.Info("using firecracker provider")
case cfg.incusSocket != "":
    p, err := newIncusProvider(cfg.incusSocket)
    if err != nil {
        return fmt.Errorf("incus provider: %w", err)
    }
    prov = p
    logger.Info("using incus provider", "socket", cfg.incusSocket)
default:
    prov = provider.NewMock()
    logger.Info("using mock provider")
}
```

New flags in `parseFlags()`:
- `--firecracker-bin` — path to Firecracker binary (triggers Firecracker provider)
- `--jailer-bin` — path to jailer binary
- `--kernel-path` — path to vmlinux
- `--image-dir` — directory for rootfs images
- `--chroot-base` — base directory for jailer chroots (default: `/srv/firecracker`)
- `--host-interface` — network interface for masquerade (default: auto-detect from default route)

### Configuration

```go
type Config struct {
    FirecrackerBin string   // path to firecracker binary
    JailerBin      string   // path to jailer binary
    KernelPath     string   // path to vmlinux kernel image
    ImageDir       string   // directory containing ext4 rootfs images
    ChrootBase     string   // base directory for jailer chroots
    VsockCIDBase   uint32   // starting CID (auto-incremented per VM)
    HostInterface  string   // network interface for masquerade (empty = auto-detect default route)
}
```

Port range fields are omitted — they will be added in Phase 2 when port publishing is implemented.

---

## Prerequisites

The service layer currently hardcodes `Backend: "incus"` when creating sandbox records (`internal/service/sandbox.go`, lines 75 and 119). While this is overwritten after the provider returns a `BackendRef`, it creates a window where the database record has the wrong backend.

**Fix:** Pass the backend name as configuration to `SandboxService` (e.g., a `DefaultBackend string` field on the service struct, set during wiring in `main.go` based on the selected provider). This avoids changing the `domain.Provider` interface. The service uses this value when creating the initial sandbox record. Both `Create` and `CreateFromSnapshot` paths must be updated.

---

## VM Lifecycle

### Domain State Machine Compliance

`SandboxPending` is a service-layer state set before the provider is ever called. The provider's `GetSandboxState` never returns it — the service layer manages the `Pending → Starting` transition internally. The provider only needs to handle states from `Starting` onward.

Firecracker VMs map to the domain's seven-state model as follows:

| Firecracker reality | Domain state | Condition |
|---|---|---|
| Rootfs prepared, VM never launched | `SandboxStopped` | Chroot exists, no process (just created) |
| VM launching, agent not yet responding | `SandboxStarting` | Process running, no pong from agent |
| VM running, agent healthy | `SandboxRunning` | Process running, agent responds to ping |
| `SendCtrlAltDel` sent, process still alive | `SandboxStopping` | Shutdown signal sent, process not yet exited |
| Process exited, chroot/rootfs still on disk | `SandboxStopped` | No process, chroot directory exists |
| VM process crashed or agent unresponsive after successful start | `SandboxFailed` | Process died unexpectedly (no clean shutdown) |
| Chroot cleaned up | `SandboxDestroyed` | No process, no chroot directory |

The key insight: `CreateSandbox` prepares the rootfs but does NOT launch the Firecracker process — matching the Incus pattern where `CreateSandbox` creates an instance without starting it. `StartSandbox` launches the VM. A stopped VM retains its rootfs on disk, so `StartSandbox` can relaunch from the same rootfs, preserving filesystem state. This makes `SandboxStopped → SandboxStarting` a valid transition.

### CreateSandbox

Prepares the VM but does NOT launch it (matching the Incus pattern where the service layer calls `StartSandbox` separately):

1. Copy rootfs image from `ImageDir` to per-VM directory at `<ChrootBase>/firecracker/<vmID>/`
2. Allocate vsock CID (atomic counter, crash-safe — see Recovery section)
3. Pre-allocate UID for jailer
4. Write `vminfo.json` to `<ChrootBase>/firecracker/<vmID>/vminfo.json` (CID, UID — tap/subnet allocated on start)
5. Return `BackendRef{Backend: "firecracker", Ref: vmID}` where `vmID = "nvrs-fc-<random8>"`

After CreateSandbox returns, the VM is in `SandboxStopped` state (chroot exists, no process). The service layer then calls `StartSandbox`.

The `nvrs-fc-` prefix (vs Incus's `nvrs-`) is deliberate: it lets operators identify provider type from the VM ID and prevents naming collisions if both providers run on the same host in the future.

`CreateSandboxRequest.Metadata` is stored in `vminfo.json` but not used by the provider — consistent with Incus, which also ignores it.

### StartSandbox

Launches the Firecracker process:

1. Read `vminfo.json` for CID and UID
2. Create tap device, assign /30 subnet from private range
3. If `NetworkMode == "published"`: add per-VM masquerade rule. If `"isolated"`: no masquerade
4. Build Firecracker machine config: kernel, rootfs drive, vsock device, network interface
5. Launch VM via `firecracker-go-sdk` with jailer config
6. Update `vminfo.json` with PID, tap device, subnet index
7. Add entry to in-memory vmInfo map
8. Poll guest agent over vsock until `pong` received (30s timeout)

If the VM is already running: no-op. If the chroot doesn't exist: return error.

### StopSandbox

1. Set internal state to `SandboxStopping`
2. If `force == false`: send `SendCtrlAltDel` via Firecracker API, wait up to 30s for process exit
3. If `force == true` or graceful timeout exceeded: kill the Firecracker process (SIGKILL)
4. Delete tap device, remove per-VM masquerade rule if present, return subnet to pool
5. Update `vminfo.json`: clear PID, tap_device, subnet_idx fields (CID and UID retained for restart)
6. Remove entry from in-memory vmInfo map
7. State becomes `SandboxStopped` (chroot/rootfs remains on disk)

Note: stopped VMs release their tap device and subnet. On `StartSandbox`, new ones are allocated. This means a restarted VM may get a different IP address.

### DestroySandbox

1. If process is running: kill it (SIGKILL)
2. Delete tap device if it exists, remove masquerade rule if present
3. Remove the entire VM directory: `<ChrootBase>/firecracker/<vmID>/` (includes `root/` chroot, `vminfo.json`, socket files)
4. Remove in-memory vmInfo entry
5. State becomes `SandboxDestroyed`

Must work without the in-memory vmInfo map (for crash recovery). All state is derivable from the filesystem:
- VM directory: `<ChrootBase>/firecracker/<vmID>/`
- Chroot: `<ChrootBase>/firecracker/<vmID>/root/`
- VM metadata: `<ChrootBase>/firecracker/<vmID>/vminfo.json` (outside chroot, not visible to guest)
- Tap device: read from `vminfo.json`
- PID: read from `vminfo.json`, or scan `/proc` for Firecracker processes in this chroot

### GetSandboxState

1. Check if chroot directory exists at `<ChrootBase>/firecracker/<vmID>/`
   - No → `SandboxDestroyed`
2. Read `vminfo.json` for PID
3. Check if PID is alive (`kill(pid, 0)`)
   - Not alive → check if shutdown was clean (flag in vminfo.json): `SandboxStopped` if clean, `SandboxFailed` if unexpected
4. If alive, check agent vsock health (ping with 2s timeout)
   - Responds → `SandboxRunning`
   - No response → `SandboxStarting`
5. If shutdown in progress (flag set by StopSandbox) → `SandboxStopping`

### VM Metadata

Running VMs are tracked in an in-memory map (`sync.RWMutex`-protected):

```go
type vmInfo struct {
    ID         string `json:"id"`
    PID        int    `json:"pid"`
    CID        uint32 `json:"cid"`
    TapDevice  string `json:"tap_device"`
    SubnetIdx  int    `json:"subnet_idx"`
    UID        int    `json:"uid"`
    ChrootPath string `json:"chroot_path"`
    Stopping   bool   `json:"stopping"`
}
```

This struct is also persisted as `vminfo.json` inside each VM's chroot directory. On restart, the in-memory map is rebuilt from disk (see Recovery section).

---

## Crash Recovery

On provider init (`New()`), scan `<ChrootBase>/firecracker/` for existing VM directories:

1. For each `<vmID>/vminfo.json` found, read CID, UID, PID, subnet index, tap device
2. Rebuild the in-memory vmInfo map for VMs that have a PID set (running or recently crashed)
3. Initialize CID counter past the highest in-use CID
4. Initialize UID counter past the highest in-use UID
5. For VMs with a PID that is still alive: leave in map (reconciler will handle state sync)
6. For VMs with a dead PID and PID field set: mark as stopped in map, clear stale PID (reconciler/GC will clean up)
7. Stopped VMs (PID field empty/zero in vminfo.json) have no tap/subnet to reclaim — subnet allocator does not need to account for them

This ensures CID, UID, and subnet allocations never collide with orphaned VMs after a restart.

---

## Guest Agent

### Overview

`navaris-agent` is a statically compiled Go binary baked into every Firecracker rootfs image. It starts on boot, listens on vsock port 1024, and handles exec/session requests from the host.

### vsock Protocol

Length-prefixed JSON over vsock. Each message:

```go
type Message struct {
    Version uint8           `json:"v"`       // protocol version (currently 1)
    Type    string          `json:"type"`    // "exec", "session", "resize", "ping", "signal"
    ID      string          `json:"id"`      // request correlation ID
    Payload json.RawMessage `json:"payload"`
}
```

The `Version` field enables forward compatibility. On ping/pong, both sides exchange their version. If versions differ, the host logs a warning. Breaking protocol changes increment the version; the agent should handle older versions gracefully.

Message types:
- **ping/pong** — health check (includes version exchange)
- **exec** — run command, stream stdout/stderr, return exit code
- **session** — allocate PTY, spawn shell, bidirectional streaming
- **resize** — resize PTY (width, height)
- **signal** — send signal to process (e.g., SIGTERM)
- **stdout/stderr** — output data chunks (agent → host)
- **stdin** — input data chunks (host → agent)
- **exit** — process exit code (agent → host)

### Exec Flow

1. Host sends `{v: 1, type: "exec", id: "abc", payload: {command: ["/bin/ls"], env: {}, workdir: "/"}}`
2. Agent spawns process with separated stdout/stderr pipes
3. Agent streams `{type: "stdout", id: "abc", payload: "<base64>"}` and `{type: "stderr", id: "abc"}` messages
4. On completion: `{type: "exit", id: "abc", payload: {code: 0}}`
5. Host's `ExecHandle.Wait()` blocks on exit message

### Session Flow (Interactive/PTY)

1. Host sends `{v: 1, type: "session", id: "xyz", payload: {shell: "/bin/bash"}}`
2. Agent allocates PTY, spawns shell
3. Bidirectional: host stdin messages → PTY input, PTY output → host stdout messages
4. Resize: `{type: "resize", id: "xyz", payload: {w: 80, h: 24}}`
5. Close: `{type: "signal", id: "xyz", payload: {signal: "HUP"}}` or connection close

### Multiplexing

Multiple concurrent exec/session requests share one vsock connection per VM. Requests are distinguished by correlation ID. The host-side vsock client maintains `map[string]chan Message` for routing responses.

---

## Networking (Phase 1)

### Per-VM Tap Device

On StartSandbox:
1. Create tap device named `fc-<8chars>` where the suffix is the last 8 chars of the vmID (total: 11 chars, well within Linux's 15-char `IFNAMSIZ` limit)
2. Allocate /30 subnet from `172.26.0.0/16` range. Host gets `.1`, guest gets `.2`
3. Configure tap with host IP, bring it up
4. Pass tap name and guest IP to Firecracker network interface config

### NetworkMode Handling

- `NetworkMode: "published"` — tap device with masquerade rule: VM has full internet access
- `NetworkMode: "isolated"` — tap device without masquerade: VM can communicate with host only (e.g., for vsock and host-local services, but no outbound internet)

Per-VM masquerade rule (only for published mode):
```
iptables -t nat -A POSTROUTING -s 172.26.X.2/32 -o <host-iface> -j MASQUERADE
```

Note: `iptables` is a runtime dependency. The provider uses the `iptables` binary (either `iptables-legacy` or `iptables-nft` shim). On systems using `nftables` natively, the `iptables` compatibility shim must be installed. If the binary is not found on provider init, startup fails with a clear error.

### Host Interface Detection

`Config.HostInterface` specifies the outbound network interface for masquerade rules. If empty, auto-detect by querying the default route (`ip route get 1.1.1.1`). Fail provider init if neither is set and auto-detection fails.

### Guest IP Configuration

Passed via Firecracker kernel boot args:
```
ip=172.26.X.2::172.26.X.1:255.255.255.252::eth0:off
```

No DHCP needed. Guest networking is configured by the kernel before init runs.

### IP Allocation

Counter-based allocator: each VM gets the next /30 block. With a /16, supports ~16k VMs. On startup, scan existing VMs to initialize counter past highest in-use subnet (see Crash Recovery).

### Cleanup

On StopSandbox/DestroySandbox: delete tap device, remove per-VM masquerade rule if present, return subnet to pool.

Requires `net.ipv4.ip_forward=1` (checked on init, error if not set).

---

## Jailer Configuration

Each VM runs through the Firecracker jailer for defense-in-depth:

- **Chroot**: `<ChrootBase>/firecracker/<vmID>/root/`. Jailer hard-links Firecracker binary, kernel, and rootfs into chroot. Process cannot see host filesystem after launch.
- **Cgroups**: Each VM in its own cgroup (`/firecracker/<vmID>`). CPU/memory limits from `CreateSandboxRequest` map to cgroup constraints.
- **UID/GID**: Base UID (10000) + sequential offset. Each VM runs as unique unprivileged user. On startup, scan existing VMs to initialize offset past highest in-use UID (see Crash Recovery).
- **Seccomp**: Firecracker's default seccomp filter applied automatically by jailer.

Jailer invocation via Go SDK:
```go
jailerCfg := firecracker.JailerConfig{
    GID:            gid,
    UID:            uid,
    ID:             vmID,
    NumaNode:       0,
    ExecFile:       cfg.FirecrackerBin,
    JailerBinary:   cfg.JailerBin,
    ChrootBaseDir:  cfg.ChrootBase,
    ChrootStrategy: firecracker.NewNaiveChrootStrategy(cfg.KernelPath),
}
```

---

## Phase 2 Stubs

All Phase 2 methods return a standalone error (not wrapping any domain sentinel, to avoid false positives with `errors.Is`):

```go
var ErrNotImplemented = errors.New("firecracker provider: operation not implemented (phase 2)")
```

Stubbed methods:
- `CreateSnapshot`, `RestoreSnapshot`, `DeleteSnapshot`
- `CreateSandboxFromSnapshot`, `PublishSnapshotAsImage`, `DeleteImage`, `GetImageInfo`
- `PublishPort`, `UnpublishPort`

---

## Testing Strategy

**Unit tests (no Firecracker binary needed):**
- `vsock/protocol_test.go` — message serialization, round-trip encoding, version negotiation
- `network/tap_test.go` — IP allocation logic, /30 math, IFNAMSIZ validation (no actual tap creation)
- `jailer/jailer_test.go` — config struct generation, UID allocation, crash recovery scanning
- `cmd/navaris-agent/agent/` — exec and session handler tests with mock PTY

**Integration tests (`//go:build integration,firecracker`):**
- Full VM lifecycle: create → exec → stop → start → exec → destroy
- Guest agent health check timeout (agent not running)
- Concurrent VM creation (3 VMs in parallel)
- Graceful vs forced shutdown
- Exec against a stopped VM (expect error)
- NetworkMode isolated: verify no outbound internet
- Resource limit enforcement (CPU/memory cgroup constraints)
- Crash recovery: kill navarisd, restart, verify orphaned VMs are detected

---

## Dependencies

- `github.com/firecracker-microvm/firecracker-go-sdk` — official Firecracker Go SDK
- `golang.org/x/sys/unix` — vsock support (`AF_VSOCK`, `VMADDR_CID_*`)
- Firecracker binary (v1.x) and jailer — runtime dependencies, not Go dependencies
- Linux kernel image (vmlinux) — provided by operator
- ext4 rootfs images with `navaris-agent` baked in — provided by operator

---

## Error Handling

- VM launch failures: clean up chroot, tap device, return error with context
- Guest agent timeout: kill VM process, clean up, return error
- vsock connection failures: return error to caller, VM stays running (caller can retry or destroy)
- Tap device creation failures (e.g., no permissions): fail CreateSandbox with clear error
- iptables setup failure: fail the operation with clear error
- Host interface auto-detection failure: fail provider initialization
- `ip_forward` not enabled: fail provider initialization with instructions

---

## Phase 2 Preview

For context, Phase 2 will add:
- **Snapshots**: Firecracker's native VM snapshot (captures full memory + disk state)
- **Images**: Publish snapshot rootfs as reusable ext4 image
- **Port publishing**: iptables DNAT rules forwarding host ports to VM tap IPs, `PortRangeMin`/`PortRangeMax` config fields
