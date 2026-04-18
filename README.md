# Navaris

A sandbox control plane for managing isolated execution environments across multiple backends.

## Why Navaris?

Running untrusted or experimental code safely requires strong isolation — but the tooling to manage that isolation is fragmented. Incus gives you system containers, Firecracker gives you microVMs, and each has its own API, lifecycle model, and operational quirks.

Navaris provides a single control plane that abstracts over these backends. You get a unified REST API and CLI for creating, snapshotting, and managing sandboxes regardless of whether they run as containers or microVMs. The same workflow that creates an Incus container also works for a Firecracker VM — same endpoints, same state machine, same tooling.

This matters when you need to:

- **Offer on-demand dev environments** where users shouldn't care about the underlying runtime
- **Run untrusted code** with microVM-level isolation without rewriting your orchestration layer
- **Snapshot and restore** execution state for reproducibility or checkpointing
- **Mix isolation levels** — containers for speed, microVMs for security — behind one API

## Backends

Navaris supports two isolation backends. You can enable one or both when starting the daemon — the API and CLI work identically regardless of which backend is running underneath. When both are enabled, each sandbox is routed to the correct backend automatically.

### Incus (system containers)

[Incus](https://linuxcontainers.org/incus/) runs sandboxes as system containers using LXC. Containers share the host kernel but get their own filesystem, process tree, and network namespace.

**Strengths:**
- **Fast startup** — containers launch in under a second
- **Low overhead** — no guest kernel, near-native CPU and memory performance
- **Mature ecosystem** — rich image library, live migration, storage pools, and clustering built in
- **Simpler operations** — no kernel or rootfs images to manage separately

**Best for:** development environments, CI runners, trusted workloads where speed and density matter more than hard isolation boundaries.

### Firecracker (microVMs)

[Firecracker](https://firecracker-microvm.github.io/) runs sandboxes as lightweight virtual machines using KVM. Each sandbox gets its own guest kernel, providing hardware-level isolation.

**Strengths:**
- **Strong isolation** — separate kernel per sandbox; a guest kernel exploit doesn't compromise the host
- **Minimal attack surface** — Firecracker's VMM is purpose-built with a small device model (no PCI, no USB, no graphics)
- **Predictable performance** — dedicated vCPUs and memory with no kernel sharing; no noisy-neighbor effects from cgroup contention
- **Jailer integration** — each VM runs in a chroot with seccomp filters and a dedicated UID

**Best for:** running untrusted code, multi-tenant environments, security-sensitive workloads where isolation guarantees matter.

### Choosing a backend

| Consideration | Incus | Firecracker |
|--------------|-------|-------------|
| Startup time | ~1s | ~2-3s |
| Memory overhead | Minimal | ~30MB per VM (guest kernel) |
| Isolation level | Namespace/cgroup | Hardware (KVM) |
| Host kernel shared | Yes | No |
| Requires `/dev/kvm` | No | Yes |
| Image management | Built-in image server | Manual rootfs + kernel |
| Live snapshots | Native support | Memory + disk snapshot |
| Density (sandboxes per host) | Higher | Lower |

You can run both backends in a single Navaris instance — containers for speed, microVMs for security — behind one API. Backend selection happens automatically based on image format, or you can specify it explicitly per sandbox.

## Features

- **Multi-backend**: Incus (system containers) and Firecracker (microVMs)
- **Full lifecycle management**: create, start, stop, destroy sandboxes
- **Snapshots**: point-in-time captures (stopped and live consistency modes)
- **Images**: promote snapshots into reusable base images
- **Interactive sessions**: persistent, reconnectable shell sessions (direct and tmux-backed)
- **Command execution**: run commands inside sandboxes with PTY support
- **Port forwarding**: publish sandbox ports to the host
- **Async operations**: long-running tasks with progress tracking and cancellation
- **Real-time events**: WebSocket event stream for sandbox lifecycle changes
- **Observability**: OpenTelemetry metrics and distributed tracing (OTLP export)
- **Projects**: organize sandboxes into logical groups
- **MCP server**: expose sandboxes as Model Context Protocol tools for AI agents — see [docs/mcp.md](docs/mcp.md)

## Architecture

```
navaris (CLI)  ──HTTP──▶  navarisd (daemon)  ──▶  Incus / Firecracker
                              │
                              ├── REST API + WebSocket events
                              ├── Service layer (business logic)
                              ├── SQLite (state persistence)
                              ├── Async dispatcher (operation queue)
                              └── OpenTelemetry (metrics + traces)
```

For Firecracker, a lightweight guest agent (`navaris-agent`) runs inside each VM and communicates with the daemon over vsock for command execution and session management.

## Building

### Prerequisites

- Go 1.26+
- For Incus backend: a running Incus daemon
- For Firecracker backend: Firecracker binary, jailer, a Linux kernel, and `/dev/kvm` access

### Build the daemon and CLI

```bash
# Incus backend only
go build -tags incus -o navarisd ./cmd/navarisd

# Firecracker backend only
go build -tags firecracker -o navarisd ./cmd/navarisd

# Both backends (recommended)
go build -tags firecracker,incus -o navarisd ./cmd/navarisd

# CLI (no build tags needed)
go build -o navaris ./cmd/navaris

# Guest agent (for Firecracker VMs)
GOOS=linux GOARCH=amd64 go build -o navaris-agent ./cmd/navaris-agent
```

## Running

### Start the daemon

```bash
# With Incus only
./navarisd --incus-socket /var/lib/incus/unix.socket --auth-token mysecret

# With Firecracker only (requires /dev/kvm)
./navarisd \
  --firecracker-bin /usr/local/bin/firecracker \
  --jailer-bin /usr/local/bin/jailer \
  --kernel-path /var/lib/firecracker/vmlinux \
  --image-dir /var/lib/firecracker/images \
  --auth-token mysecret

# Both backends simultaneously
./navarisd \
  --incus-socket /var/lib/incus/unix.socket \
  --firecracker-bin /usr/local/bin/firecracker \
  --jailer-bin /usr/local/bin/jailer \
  --kernel-path /var/lib/firecracker/vmlinux \
  --image-dir /var/lib/firecracker/images \
  --auth-token mysecret
```

When both `--incus-socket` and `--firecracker-bin` are provided, navarisd enables both backends. If KVM is not available, Firecracker is disabled with a warning and Incus continues to work.

Backend selection for new sandboxes follows this priority:
1. Explicit `backend` field in the API request
2. Auto-detection from image reference: `alpine/3.21` (slash) -> Incus, `alpine-3.21` (flat) -> Firecracker
3. Default fallback: Incus (when both are available)

### Daemon flags

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `:8080` | Address to listen on |
| `--db-path` | `navaris.db` | Path to SQLite database |
| `--log-level` | `info` | Log level (debug, info, warn, error) |
| `--auth-token` | *(empty)* | Bearer token for API auth (empty = no auth) |
| `--incus-socket` | | Path to Incus unix socket |
| `--firecracker-bin` | | Path to Firecracker binary |
| `--jailer-bin` | | Path to jailer binary |
| `--kernel-path` | | Path to vmlinux kernel |
| `--image-dir` | | Directory containing rootfs images |
| `--chroot-base` | `/srv/firecracker` | Jailer chroot base directory |
| `--snapshot-dir` | `/srv/firecracker/snapshots` | Snapshot storage directory |
| `--host-interface` | *(auto-detect)* | Network interface for masquerade |
| `--enable-jailer` | `true` | Use the Firecracker jailer (disable for Docker) |
| `--concurrency` | `8` | Max concurrent operations |
| `--gc-interval` | `5m` | Operation garbage collection interval |
| `--otlp-endpoint` | *(empty)* | OTLP collector endpoint (empty = telemetry disabled) |
| `--otlp-protocol` | `grpc` | OTLP transport: `grpc` or `http` |
| `--service-name` | `navarisd` | Service name in telemetry data |

## Web UI

Navaris ships with an optional web UI for inspecting projects, sandboxes, and events, triggering lifecycle actions, and attaching to sandbox terminals. It is disabled by default.

### Enabling the UI

Pass `--ui-password` to `navarisd`:

```bash
navarisd --ui-password "$(openssl rand -base64 24)" --auth-token your-token
```

The UI is served from the root of the listen address. Visit `http://HOST:8080/`, sign in with the password, and you're in. API endpoints for the UI itself live under `/ui/` (`/ui/login`, `/ui/logout`, `/ui/me`) and are not meant to be visited directly.

### UI flags

| Flag | Env var | Description |
|------|---------|-------------|
| `--ui-password` | `NAVARIS_UI_PASSWORD` | Password required to sign in. Leaving it unset disables the UI entirely. |
| `--ui-session-key` | `NAVARIS_UI_SESSION_KEY` | Base64-encoded 32-byte key used to sign session cookies. Auto-generated at startup if omitted — existing sessions are invalidated on every restart in that case. |
| `--ui-session-ttl` | `NAVARIS_UI_SESSION_TTL` | How long a session cookie is valid (default `24h`). |

### Security notes

- The UI does **not** support TLS termination itself. Put it behind a reverse proxy in any environment where network traffic can be observed.
- Login attempts are rate-limited to 5 failures per IP per minute.
- Session cookies are HMAC-signed, `HttpOnly`, `SameSite=Lax`, and flagged `Secure` when the request arrives over HTTPS.

### Building from source

```bash
make web-deps web-build build-ui
```

The SPA sources live in `web/`. See `web/MANUAL_TERMINAL_SMOKE.md` for a manual smoke-test procedure.

## CLI usage

Configure the CLI via flags or environment variables:

```bash
export NAVARIS_API_URL=http://localhost:8080
export NAVARIS_TOKEN=mysecret
```

### Quick start

```bash
# Create a project
navaris project create --name my-project

# Create an Incus container (auto-detected from image format)
navaris sandbox create --project <project-id> --name dev --image alpine/3.21

# Create a Firecracker VM (auto-detected from image format)
navaris sandbox create --project <project-id> --name secure --image alpine-3.21

# Or specify backend explicitly
navaris sandbox create --project <project-id> --name dev --image alpine/3.21 --backend incus

# Wait for it to start, then run a command
navaris sandbox exec --sandbox <sandbox-id> -- echo "hello from the sandbox"

# Create a snapshot
navaris snapshot create --sandbox <sandbox-id> --label checkpoint-1

# Stop and destroy
navaris sandbox stop <sandbox-id>
navaris sandbox destroy <sandbox-id>
```

### Commands

| Command | Description |
|---------|-------------|
| `project create/list/get/update/delete` | Manage projects |
| `sandbox create/list/get/start/stop/destroy/exec` | Manage sandboxes |
| `snapshot create/list/get/restore/delete` | Manage snapshots |
| `image list/get/promote/register/delete` | Manage base images |
| `session create/list/get/destroy` | Manage interactive sessions |
| `operation list/get/cancel` | Track async operations |
| `port create/list/delete` | Manage port bindings |

Use `navaris <command> --help` for detailed usage of each command.

## API

All endpoints are under `/v1/`. Authentication is via `Authorization: Bearer <token>` header when `--auth-token` is set.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/health` | Health check |
| `POST` | `/v1/projects` | Create project |
| `GET` | `/v1/projects` | List projects |
| `GET` | `/v1/projects/{id}` | Get project |
| `PUT` | `/v1/projects/{id}` | Update project |
| `DELETE` | `/v1/projects/{id}` | Delete project |
| `POST` | `/v1/sandboxes` | Create sandbox |
| `POST` | `/v1/sandboxes/from-snapshot` | Create sandbox from snapshot |
| `POST` | `/v1/sandboxes/from-image` | Create sandbox from image |
| `GET` | `/v1/sandboxes` | List sandboxes |
| `GET` | `/v1/sandboxes/{id}` | Get sandbox |
| `POST` | `/v1/sandboxes/{id}/start` | Start sandbox |
| `POST` | `/v1/sandboxes/{id}/stop` | Stop sandbox |
| `POST` | `/v1/sandboxes/{id}/destroy` | Destroy sandbox |
| `POST` | `/v1/sandboxes/{id}/exec` | Execute command |
| `POST` | `/v1/sandboxes/{id}/snapshots` | Create snapshot |
| `GET` | `/v1/sandboxes/{id}/snapshots` | List snapshots |
| `GET` | `/v1/snapshots/{id}` | Get snapshot |
| `POST` | `/v1/snapshots/{id}/restore` | Restore snapshot |
| `DELETE` | `/v1/snapshots/{id}` | Delete snapshot |
| `POST` | `/v1/images` | Promote snapshot to image |
| `POST` | `/v1/images/register` | Register external image |
| `GET` | `/v1/images` | List images |
| `GET` | `/v1/images/{id}` | Get image |
| `DELETE` | `/v1/images/{id}` | Delete image |
| `POST` | `/v1/sandboxes/{id}/sessions` | Create session |
| `GET` | `/v1/sandboxes/{id}/sessions` | List sessions |
| `GET` | `/v1/sessions/{id}` | Get session |
| `DELETE` | `/v1/sessions/{id}` | Delete session |
| `POST` | `/v1/sandboxes/{id}/ports` | Publish port |
| `GET` | `/v1/sandboxes/{id}/ports` | List ports |
| `DELETE` | `/v1/sandboxes/{id}/ports/{target_port}` | Remove port |
| `GET` | `/v1/operations/{id}` | Get operation |
| `GET` | `/v1/operations` | List operations |
| `POST` | `/v1/operations/{id}/cancel` | Cancel operation |
| `GET` | `/v1/events` | Event stream (WebSocket) |

## Development

### Running integration tests

Integration tests use Docker Compose to spin up the full stack.

```bash
# Incus backend
make integration-test

# Firecracker backend (requires /dev/kvm)
make integration-test-firecracker

# Mixed: both backends in one navarisd (requires /dev/kvm)
make integration-test-mixed
```

Dev environments for manual testing:

```bash
make integration-env                # Incus
make integration-env-firecracker    # Firecracker
make integration-env-down           # tear down Incus
make integration-env-firecracker-down  # tear down Firecracker
```

### Running unit tests

```bash
go test ./...
```

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.
