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

Navaris supports two isolation backends. You choose one when starting the daemon — the API and CLI work identically regardless of which backend is running underneath.

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

You can also run both backends on different Navaris instances and route workloads based on trust level — containers for your own code, microVMs for user-submitted code.

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
# Incus backend (default)
go build -o navarisd ./cmd/navarisd
go build -o navaris ./cmd/navaris

# Firecracker backend
go build -tags firecracker -o navarisd ./cmd/navarisd
go build -o navaris ./cmd/navaris

# Guest agent (for Firecracker VMs)
GOOS=linux GOARCH=amd64 go build -o navaris-agent ./cmd/navaris-agent
```

## Running

### Start the daemon

```bash
# With Incus
./navarisd --incus-socket /var/lib/incus/unix.socket --auth-token mysecret

# With Firecracker
./navarisd \
  --firecracker-bin /usr/local/bin/firecracker \
  --jailer-bin /usr/local/bin/jailer \
  --kernel-path /var/lib/firecracker/vmlinux \
  --image-dir /var/lib/firecracker/images \
  --auth-token mysecret
```

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
| `--concurrency` | `8` | Max concurrent operations |
| `--gc-interval` | `5m` | Operation garbage collection interval |
| `--otlp-endpoint` | *(empty)* | OTLP collector endpoint (empty = telemetry disabled) |
| `--otlp-protocol` | `grpc` | OTLP transport: `grpc` or `http` |
| `--service-name` | `navarisd` | Service name in telemetry data |

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

# Create a sandbox
navaris sandbox create --project <project-id> --name dev --image alpine/3.21

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
make integration-env          # start environment
make integration-test         # run tests
make integration-env-down     # tear down

# Firecracker backend (requires /dev/kvm)
make integration-env-firecracker
make integration-test-firecracker
make integration-env-firecracker-down
```

### Running unit tests

```bash
go test ./...
```

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.
