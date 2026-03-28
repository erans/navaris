# Sandbox Control Plane Design

## 1. Overview

Navaris is a sandbox control plane that runs locally inside a Linux VM on macOS for developer use, and later on dedicated Linux servers to manage one or more execution backends.

The v1 deployment:

- A macOS client (CLI and Go SDK)
- A Linux VM on the Mac
- The control plane running inside that Linux VM
- Incus running inside that Linux VM
- Sandboxes running inside Incus as system containers

The control plane is the product surface. Execution backends are hidden behind it.

The design reserves a clean path for later support of Firecracker, dedicated Linux servers, fleets of mixed execution hosts, and image baking tools.

---

## 2. Goals

### 2.1 Primary goals

- Provide a consistent sandbox API for local and remote environments
- Run locally on macOS by hosting a Linux VM that contains the control plane and Incus
- Support creating sandboxes from reusable base images
- Support snapshotting stopped or running sandboxes where supported
- Support branching by booting new sandboxes from snapshots
- Support promoting selected snapshots into reusable base images
- Support persistent, reconnectable shell sessions inside sandboxes
- Keep the client contract stable regardless of backend

### 2.2 Secondary goals

- Make local development closely resemble remote deployment
- Support fast sandbox creation for common developer workflows
- Make it possible to add image baking tools later without changing the core control-plane API

---

## 3. Non-goals for v1

- Base-image baking
- Firecracker support
- Fleet scheduling
- Multi-host coordination
- Cross-backend live migration
- Strong hostile multi-tenant security guarantees
- A full workstation management product
- Arbitrary nested execution environments on macOS

---

## 4. Decisions

| Area | Choice |
|---|---|
| Language | Go |
| Architecture | Hexagonal / ports-and-adapters |
| Binaries | `navaris` (CLI) + `navarisd` (daemon) |
| Wire protocol | HTTP+JSON (CRUD) + WebSocket (streaming) |
| Metadata store | SQLite v1, interface-swappable to Postgres |
| Provider | Incus v1, interface-swappable |
| Operations | Async by default, dispatcher with goroutine pool |
| CLI | AI-agent-friendly, JSON default in non-TTY, `--wait`/`--timeout` |
| SDK | `pkg/client` in same repo, typed Go, `AndWait` convenience methods |
| Auth | Static local token v1 |
| Base images | Global only in v1 |
| Projects | Multi-project from v1 |
| Sessions | First-class domain object, backed by direct PTY or tmux |
| Observability | Structured slog, request IDs, health endpoint |

---

## 5. v1 Deployment Model

### 5.1 Local macOS developer appliance

```text
macOS
├── navaris CLI
│
└── Linux VM (Apple Virtualization.framework / Lima)
    ├── navarisd (control plane daemon)
    ├── SQLite metadata store
    ├── worker goroutines
    └── Incus
         └── system containers (sandboxes)
```

- The client communicates only with the control plane
- The client never talks directly to Incus
- All sandbox state lives inside the Linux VM

### 5.2 Linux VM provisioning

For v1, the Linux VM on macOS is provisioned using a thin wrapper over Apple Virtualization.framework, with Lima as the default implementation.

Assumptions for v1:

- One Ubuntu or Debian VM per developer machine
- Fixed CPU and memory allocation configured at install time
- A single VM disk containing control-plane state, Incus state, and sandbox storage
- No dependency on writable macOS host mounts for sandbox root state

---

## 6. Terminology

| Term | Definition |
|---|---|
| Sandbox | An isolated execution environment created by the control plane. The user-visible abstraction. |
| Backend | An execution provider such as Incus or Firecracker. |
| Base image | A reusable boot source used to create new sandboxes. May represent a fresh OS template or a promoted snapshot. |
| Snapshot | A point-in-time capture of sandbox state. |
| Session | A persistent shell session inside a sandbox that outlives client connections and supports reconnection. |
| Host | A machine or VM that can run one or more backends. |
| Operation | A record tracking the progress of an asynchronous mutating API call. |
| Project | A namespace for organizing sandboxes and related resources. |

---

## 7. Architecture

### 7.1 Project structure

```
navaris/
├── cmd/
│   ├── navaris/               CLI binary
│   │   └── main.go
│   └── navarisd/              Daemon binary
│       └── main.go
├── pkg/
│   └── client/                Public Go SDK
│       ├── client.go          Client struct, connection, auth
│       ├── sandbox.go         Sandbox operations
│       ├── snapshot.go        Snapshot operations
│       ├── image.go           Image operations
│       ├── session.go         Session operations
│       ├── operation.go       Operation polling/streaming
│       ├── project.go         Project operations
│       └── types.go           Public API types
├── internal/
│   ├── domain/                Core types and interfaces (no external deps)
│   │   ├── sandbox.go
│   │   ├── snapshot.go
│   │   ├── image.go
│   │   ├── session.go
│   │   ├── operation.go
│   │   ├── project.go
│   │   └── errors.go
│   ├── service/               Business logic orchestration
│   │   ├── sandbox.go
│   │   ├── snapshot.go
│   │   ├── image.go
│   │   ├── session.go
│   │   ├── operation.go
│   │   └── project.go
│   ├── provider/              Backend abstraction
│   │   ├── provider.go        Provider interface
│   │   └── incus/             Incus adapter
│   │       ├── incus.go
│   │       ├── sandbox.go
│   │       ├── snapshot.go
│   │       ├── image.go
│   │       ├── exec.go
│   │       └── network.go
│   ├── store/                 Metadata persistence
│   │   ├── store.go           Store interface
│   │   └── sqlite/            SQLite adapter
│   │       ├── sqlite.go
│   │       ├── migrations/
│   │       ├── sandbox.go
│   │       ├── snapshot.go
│   │       ├── image.go
│   │       ├── session.go
│   │       ├── operation.go
│   │       └── project.go
│   ├── api/                   HTTP + WebSocket handlers
│   │   ├── server.go
│   │   ├── sandbox.go
│   │   ├── snapshot.go
│   │   ├── image.go
│   │   ├── session.go
│   │   ├── operation.go
│   │   ├── project.go
│   │   ├── exec.go
│   │   ├── events.go
│   │   └── middleware/
│   ├── eventbus/              In-memory event pub/sub
│   │   └── memory.go
│   └── worker/                Background task execution
│       ├── dispatcher.go
│       ├── handlers.go
│       └── gc.go
├── go.mod
└── go.sum
```

### 7.2 Hexagonal design principles

- `internal/domain` has zero external dependencies. Pure Go types and interfaces.
- `internal/service` depends on domain interfaces, never on concrete adapters.
- `pkg/client` has its own types that mirror but do not import internal domain types.
- `cmd/navarisd` wires everything together via dependency injection in `main.go`.
- `cmd/navaris` imports only `pkg/client`.

---

## 8. Domain Model

### 8.1 State machines

**Sandbox:**

```
Pending → Starting → Running → Stopping → Stopped → Destroyed
                  ↘   Failed   ↙
```

`Failed` is reachable from `Starting`, `Running`, or `Stopping`. `Stopping` is a transitional state entered when a stop is requested on a running sandbox.

**Snapshot:**

```
Pending → Creating → Ready → Deleted
                  ↘ Failed
```

**Session:**

```
Active ⇄ Detached → Exited → Destroyed
```

`Active` and `Detached` are semantically equivalent (shell is running). `Detached` is a hint that no client is attached. `Exited` means the shell process terminated.

**Operation:**

```
Pending → Running → Succeeded | Failed | Cancelled
```

**Base image:**

```
Pending → Ready → Deleted
       ↘ Failed
```

### 8.2 Core records

#### Project

| Field | Type | Notes |
|---|---|---|
| project_id | UUID | |
| name | string | unique |
| created_at | time | |
| updated_at | time | |
| metadata | JSON | |

#### Sandbox

| Field | Type | Notes |
|---|---|---|
| sandbox_id | UUID | |
| project_id | UUID | FK to project |
| name | string | unique within project |
| state | enum | see state machine |
| backend | string | "incus" |
| backend_ref | string | backend-native identifier |
| host_id | string | |
| source_image_id | UUID | nullable |
| parent_snapshot_id | UUID | nullable, set when created from snapshot |
| created_at | time | |
| updated_at | time | |
| expires_at | time | nullable |
| cpu_limit | int | nullable |
| memory_limit_mb | int | nullable |
| network_mode | enum | `isolated` or `published` |
| metadata | JSON | |

#### Snapshot

| Field | Type | Notes |
|---|---|---|
| snapshot_id | UUID | |
| sandbox_id | UUID | FK to sandbox |
| backend | string | |
| backend_ref | string | backend-native reference |
| label | string | e.g., `before-task`, `checkpoint-1` |
| state | enum | |
| created_at | time | |
| updated_at | time | |
| parent_image_id | UUID | nullable |
| publishable | bool | |
| consistency_mode | enum | `stopped` or `live` — records how the snapshot was taken |
| metadata | JSON | |

#### Session

| Field | Type | Notes |
|---|---|---|
| session_id | UUID | |
| sandbox_id | UUID | FK to sandbox |
| backing | enum | `direct` or `tmux` |
| shell | string | e.g., `/bin/bash` |
| state | enum | active, detached, exited, destroyed |
| created_at | time | |
| last_attached_at | time | nullable |
| updated_at | time | |
| idle_timeout | duration | nullable |
| metadata | JSON | |

#### BaseImage

| Field | Type | Notes |
|---|---|---|
| image_id | UUID | |
| project_scope | UUID | nullable (global in v1) |
| name | string | |
| version | string | unique with name |
| source_type | enum | `imported` or `snapshot_promoted` |
| source_snapshot_id | UUID | nullable |
| backend | string | |
| backend_ref | string | |
| architecture | string | |
| state | enum | |
| created_at | time | |
| metadata | JSON | |

#### Operation

| Field | Type | Notes |
|---|---|---|
| operation_id | UUID | |
| resource_type | string | e.g., `sandbox`, `snapshot`, `port_binding` |
| resource_id | string | |
| sandbox_id | UUID | nullable, convenience reference |
| snapshot_id | UUID | nullable, convenience reference |
| type | string | e.g., `create_sandbox`, `create_snapshot` |
| state | enum | pending, running, succeeded, failed, cancelled |
| started_at | time | |
| finished_at | time | nullable |
| error_text | string | nullable |
| metadata | JSON | |

#### PortBinding

| Field | Type | Notes |
|---|---|---|
| sandbox_id | UUID | FK to sandbox |
| target_port | int | port inside sandbox |
| published_port | int | unique, allocated by control plane |
| host_address | string | reachable from macOS |
| created_at | time | |

### 8.3 Relationships

- A Project has many Sandboxes
- A Sandbox has many Snapshots
- A Sandbox has many Sessions
- A Sandbox has many PortBindings
- A Snapshot belongs to one Sandbox
- A Sandbox may have a parent_snapshot_id (branching)
- A BaseImage may originate from an imported image or a promoted Snapshot
- An Operation tracks lifecycle activity across all objects

### 8.4 Domain interfaces (ports)

```go
type SandboxStore interface {
    Create(ctx context.Context, s *Sandbox) error
    Get(ctx context.Context, id string) (*Sandbox, error)
    List(ctx context.Context, f SandboxFilter) ([]*Sandbox, error)
    Update(ctx context.Context, s *Sandbox) error
    Delete(ctx context.Context, id string) error
    ListExpired(ctx context.Context, now time.Time) ([]*Sandbox, error)
}

// Similar interfaces: SnapshotStore, ImageStore, SessionStore,
// OperationStore, ProjectStore, PortBindingStore

type Provider interface {
    // Sandbox lifecycle
    CreateSandbox(ctx context.Context, req CreateSandboxRequest) (BackendRef, error)
    StartSandbox(ctx context.Context, ref BackendRef) error
    StopSandbox(ctx context.Context, ref BackendRef, force bool) error
    DestroySandbox(ctx context.Context, ref BackendRef) error
    GetSandboxState(ctx context.Context, ref BackendRef) (SandboxState, error)

    // Exec
    Exec(ctx context.Context, ref BackendRef, req ExecRequest) (ExecHandle, error)
    ExecDetached(ctx context.Context, ref BackendRef, req ExecRequest) (DetachedExecHandle, error)
    AttachSession(ctx context.Context, ref BackendRef, req SessionRequest) (SessionHandle, error)

    // Snapshots
    CreateSnapshot(ctx context.Context, ref BackendRef, label string, mode ConsistencyMode) (BackendRef, error)
    RestoreSnapshot(ctx context.Context, sandboxRef BackendRef, snapshotRef BackendRef) error
    DeleteSnapshot(ctx context.Context, snapshotRef BackendRef) error

    // Branching and images
    CreateSandboxFromSnapshot(ctx context.Context, snapshotRef BackendRef, req CreateSandboxRequest) (BackendRef, error)
    PublishSnapshotAsImage(ctx context.Context, snapshotRef BackendRef, req PublishImageRequest) (BackendRef, error)
    // Images
    DeleteImage(ctx context.Context, imageRef BackendRef) error
    GetImageInfo(ctx context.Context, imageRef BackendRef) (ImageInfo, error)

    // Networking
    PublishPort(ctx context.Context, ref BackendRef, targetPort int, opts PublishPortOptions) (PublishedEndpoint, error)
    UnpublishPort(ctx context.Context, ref BackendRef, publishedPort int) error

    // Health
    Health(ctx context.Context) ProviderHealth
}

type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(ctx context.Context, filter EventFilter) (<-chan Event, func(), error)
}
```

### 8.5 BackendRef

```go
type BackendRef struct {
    Backend string // "incus"
    Ref     string // backend-native identifier
}
```

The control plane stores BackendRef but never interprets it. The bridge between control-plane UUIDs and backend-native names.

### 8.5a ConsistencyMode

```go
type ConsistencyMode string
const (
    ConsistencyStopped ConsistencyMode = "stopped" // default: sandbox must be stopped
    ConsistencyLive    ConsistencyMode = "live"     // explicit opt-in: snapshot while running
)
```

### 8.5b Handle types

```go
type ExecHandle struct {
    Stdout io.ReadCloser
    Stderr io.ReadCloser
    Wait   func() (exitCode int, err error) // blocks until process exits
    Cancel func() error
}

type DetachedExecHandle struct {
    Stdin  io.WriteCloser        // write to the shell
    Stdout io.ReadCloser         // read from the shell
    Resize func(w, h int) error  // resize PTY
    Close  func() error          // terminate the process
}

type SessionHandle struct {
    Conn   io.ReadWriteCloser    // bidirectional PTY stream
    Resize func(w, h int) error
    Close  func() error
}
```

`DetachedExecHandle` is used by the `direct` session backing. The SessionService wraps Stdout in a per-session ring buffer (configurable size, default 1MB) to provide scrollback. The ring buffer is owned by SessionService and lives in the control plane process memory.

### 8.6 Event types

```
EventSandboxStateChanged
EventSnapshotStateChanged
EventImageStateChanged
EventSessionStateChanged
EventOperationStateChanged
EventExecOutput
EventExecCompleted
```

---

## 9. Service Layer

### 9.1 Service structure

```go
type SandboxService struct {
    store    domain.SandboxStore
    ops      domain.OperationStore
    provider domain.Provider
    events   domain.EventBus
    workers  *worker.Dispatcher
}
```

Same pattern for SnapshotService, ImageService, ProjectService. OperationService reads/cancels operations only.

**Exception: SessionService** does not use the Dispatcher or create Operation records. Session create, attach, and destroy are synchronous — the provider calls are fast (start a shell, connect to tmux) and do not benefit from async queuing. This keeps the session API simple for agents that create-and-immediately-attach.

### 9.2 Mutating operation flow

Every mutating call follows the same pattern:

```
Client request
  → API handler validates input
    → Service creates Operation record (state=Pending)
      → Service enqueues operation to Dispatcher
        → Returns Operation to client immediately

Dispatcher picks up operation
  → Sets operation state=Running
    → Calls provider method
      → On success: updates domain record + operation state=Succeeded
      → On failure: updates operation state=Failed, error_text set
    → Publishes state-change events to EventBus
```

### 9.3 Read operations

Reads bypass the dispatcher entirely. Service calls the store directly and returns the result.

### 9.4 Exec flow

Exec produces streaming output:

```
Exec request
  → Service creates Operation (type=Exec, state=Pending)
    → Dispatcher calls provider.Exec()
      → Provider returns ExecHandle with stdout/stderr readers
        → Dispatcher reads output, publishes EventExecOutput to EventBus
          → WebSocket handler streams EventExecOutput to client
            → On completion: records exit code, publishes EventExecCompleted
```

In-memory output buffer per operation. Fixed size cap (configurable, default 1MB). Ring buffer — older output evicted on overflow. Lost on restart.

### 9.5 Session flow

#### Create session

**Precondition:** Sandbox must be in `Running` state. Returns `ErrInvalidState` otherwise.

```
CreateSession request
  → Validate sandbox state is Running
  → Service determines backing strategy (direct, tmux, or auto-detect)
  → Creates Session record (state=Active)
  → For direct: calls provider.ExecDetached() to start shell, wraps in ring buffer
  → For tmux: calls provider.Exec() with "tmux new-session -d -s <id>"
  → Returns Session (synchronous, no Operation)
```

#### Attach to session

```
AttachToSession request (WebSocket upgrade)
  → API handler validates session is Active
  → For direct: bridges WebSocket to server-side PTY, replays scrollback first
  → For tmux: calls provider.AttachSession() with "tmux attach -t <id>", bridges WebSocket
  → Updates last_attached_at
```

#### Scrollback

```
GetScrollback request (synchronous GET)
  → For direct: reads ring buffer, returns text
  → For tmux: runs "tmux capture-pane -t <id> -p -S -<lines>" via provider.Exec(), returns text
```

#### Send input

```
SendInput request (POST)
  → For direct: writes to PTY stdin
  → For tmux: runs "tmux send-keys -t <id> -l '<input>'" via provider.Exec()
    (the -l flag sends literal characters, avoiding tmux key-name interpretation)
    Input is base64-encoded on the wire and decoded by the API handler before passing to tmux.
    Newlines in the input are sent as-is; the caller explicitly includes \n if Enter is intended.
```

#### Behaviors

- Multiple sessions per sandbox: yes
- Multiple clients on same session: tmux supports natively; direct allows one at a time in v1
- Session outlives sandbox stop: no. Stopping kills processes. Session moves to exited.
- Sandbox destroy cascades: all sessions destroyed. All pending/running operations on the sandbox are cancelled first, then destruction proceeds.
- CP restart with tmux backing: sessions survive. Reconciliation re-discovers via `tmux list-sessions`.
- CP restart with direct backing: PTY process may still run but buffer and tracking are lost. Session marked exited on reconciliation.

### 9.6 AttachSession (legacy, non-persistent)

The original `AttachSession` on the sandbox (not via a Session object) remains for quick one-off PTY access without creating a persistent session. This is a direct WebSocket-to-PTY bridge with no scrollback or reconnection.

### 9.7 Cancellation

`CancelOperation` sets the operation context to done. The dispatcher handler checks `ctx.Err()` at safe points. Provider calls that support cancellation propagate the context.

### 9.8 Service dependencies

```
SandboxService  → SandboxStore, OperationStore, Provider, EventBus, Dispatcher
SnapshotService → SnapshotStore, SandboxStore, OperationStore, Provider, EventBus, Dispatcher
ImageService    → ImageStore, SnapshotStore, OperationStore, Provider, EventBus, Dispatcher
SessionService  → SessionStore, SandboxStore, Provider, EventBus
ProjectService  → ProjectStore
OperationService → OperationStore, Dispatcher
```

---

## 10. Provider: Incus Adapter

### 10.1 Connection

```go
type IncusProvider struct {
    client incus.InstanceServer
    config IncusConfig
}
```

Connects via Unix socket (`/var/lib/incus/unix.socket`). No TLS for local v1.

### 10.2 Implementation mapping

| Operation | Incus API |
|---|---|
| CreateSandbox | `CreateInstance` with source image alias/fingerprint. Container name: `nvrs-{short-uuid}`. |
| Start/Stop | `UpdateInstanceState` with action start/stop |
| Destroy | `DeleteInstance` |
| Exec | Incus exec API with stdin/stdout/stderr streams |
| ExecDetached | Incus exec with detached flag, returns process reference |
| AttachSession | Incus exec with PTY allocation |
| CreateSnapshot | `CreateInstanceSnapshot`. Live snapshots use `--stateful`. |
| RestoreSnapshot | `RestoreInstanceSnapshot` |
| CreateSandboxFromSnapshot | `CopyInstance` from snapshot source |
| PublishSnapshotAsImage | `Publish` creates reusable image, returns fingerprint |
| PublishPort | Incus proxy device: `host:allocatedPort → container:targetPort` |
| UnpublishPort | Remove proxy device |
| GetImageInfo | `GetImage` to fetch image metadata by fingerprint |

### 10.3 Port allocation

Port range: 40000-49999 (configurable). Allocations tracked in the control-plane store (port_bindings table) to avoid collisions. The provider allocates the next available port in the range.

### 10.4 Resource limits

Mapped to Incus config keys: `limits.cpu`, `limits.memory`. If requested limits exceed configured local capacity, creation fails rather than silently overcommitting.

---

## 11. Metadata Store: SQLite Adapter

### 11.1 Implementation

```go
type SQLiteStore struct {
    db *sql.DB
}
```

Single struct implements all store interfaces. Driver: `modernc.org/sqlite` (pure Go, no CGO).

Connection settings: WAL mode, `_busy_timeout=5000`, foreign keys enabled.

### 11.2 Schema

```sql
CREATE TABLE projects (
    project_id   TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    metadata     TEXT
);

CREATE TABLE sandboxes (
    sandbox_id         TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL REFERENCES projects(project_id),
    name               TEXT NOT NULL,
    state              TEXT NOT NULL,
    backend            TEXT NOT NULL,
    backend_ref        TEXT,
    host_id            TEXT,
    source_image_id    TEXT,
    parent_snapshot_id TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    expires_at         TEXT,
    cpu_limit          INTEGER,
    memory_limit_mb    INTEGER,
    network_mode       TEXT NOT NULL DEFAULT 'isolated',
    metadata           TEXT,
    UNIQUE(project_id, name)
);

CREATE TABLE snapshots (
    snapshot_id      TEXT PRIMARY KEY,
    sandbox_id       TEXT NOT NULL REFERENCES sandboxes(sandbox_id),
    backend          TEXT NOT NULL,
    backend_ref      TEXT,
    label            TEXT NOT NULL,
    state            TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    parent_image_id  TEXT,
    publishable      INTEGER NOT NULL DEFAULT 0,
    consistency_mode TEXT NOT NULL DEFAULT 'stopped',
    metadata         TEXT
);

CREATE TABLE sessions (
    session_id       TEXT PRIMARY KEY,
    sandbox_id       TEXT NOT NULL REFERENCES sandboxes(sandbox_id),
    backing          TEXT NOT NULL,
    shell            TEXT NOT NULL DEFAULT '/bin/bash',
    state            TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    last_attached_at TEXT,
    idle_timeout_sec INTEGER,
    metadata         TEXT
);

CREATE TABLE base_images (
    image_id           TEXT PRIMARY KEY,
    project_scope      TEXT,
    name               TEXT NOT NULL,
    version            TEXT NOT NULL,
    source_type        TEXT NOT NULL,
    source_snapshot_id TEXT,
    backend            TEXT NOT NULL,
    backend_ref        TEXT,
    architecture       TEXT NOT NULL,
    state              TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    metadata           TEXT,
    UNIQUE(name, version)
);

CREATE TABLE operations (
    operation_id   TEXT PRIMARY KEY,
    resource_type  TEXT NOT NULL,
    resource_id    TEXT NOT NULL,
    sandbox_id     TEXT,
    snapshot_id    TEXT,
    type           TEXT NOT NULL,
    state          TEXT NOT NULL,
    started_at     TEXT NOT NULL,
    finished_at    TEXT,
    error_text     TEXT,
    metadata       TEXT
);

CREATE TABLE port_bindings (
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(sandbox_id),
    target_port    INTEGER NOT NULL,
    published_port INTEGER NOT NULL UNIQUE,
    host_address   TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    PRIMARY KEY (sandbox_id, target_port)
);

CREATE INDEX idx_sandboxes_project ON sandboxes(project_id);
CREATE INDEX idx_sandboxes_state ON sandboxes(state);
CREATE INDEX idx_sandboxes_expires ON sandboxes(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_snapshots_sandbox ON snapshots(sandbox_id);
CREATE INDEX idx_sessions_sandbox ON sessions(sandbox_id);
CREATE INDEX idx_sessions_state ON sessions(state);
CREATE INDEX idx_operations_resource ON operations(resource_type, resource_id);
CREATE INDEX idx_operations_sandbox ON operations(sandbox_id) WHERE sandbox_id IS NOT NULL;
CREATE INDEX idx_operations_state ON operations(state);
```

### 11.3 Migrations

Numbered `.sql` files in `internal/store/sqlite/migrations/`. Simple runner tracks applied migrations in `schema_migrations` table. No external tool needed.

### 11.4 Store swappability

The service layer only sees domain store interfaces. No SQLite-specific types leak past the adapter.

- Timestamps: `time.Time` in domain, RFC3339 text in SQLite, `timestamptz` in future Postgres
- JSON metadata: `map[string]any` in domain, TEXT in SQLite, JSONB in future Postgres
- Migrations: per-adapter directory
- Filter-to-query translation: adapter-internal

To swap stores: write `internal/store/postgres/`, implement the same interfaces, change wiring in `cmd/navarisd/main.go`.

---

## 12. API Layer

### 12.1 HTTP server

Standard library `net/http` with a lightweight router.

```go
type Server struct {
    sandbox   *service.SandboxService
    snapshot  *service.SnapshotService
    image     *service.ImageService
    session   *service.SessionService
    operation *service.OperationService
    project   *service.ProjectService
    events    domain.EventBus
    auth      *middleware.Auth
}
```

### 12.2 Route map

```
POST   /v1/projects                              → CreateProject
GET    /v1/projects                              → ListProjects
GET    /v1/projects/:id                          → GetProject
PUT    /v1/projects/:id                          → UpdateProject
DELETE /v1/projects/:id                          → DeleteProject

POST   /v1/sandboxes                             → CreateSandbox
POST   /v1/sandboxes/from-snapshot               → CreateSandboxFromSnapshot
POST   /v1/sandboxes/from-image                  → CreateSandboxFromImage (sugar: CreateSandbox with image_id)
GET    /v1/sandboxes                             → ListSandboxes (?project_id= required)
GET    /v1/sandboxes/:id                         → GetSandbox
POST   /v1/sandboxes/:id/start                   → StartSandbox
POST   /v1/sandboxes/:id/stop                    → StopSandbox
DELETE /v1/sandboxes/:id                         → DestroySandbox
POST   /v1/sandboxes/:id/exec                    → Exec
GET    /v1/sandboxes/:id/attach                  → AttachSession (WS, non-persistent)
POST   /v1/sandboxes/:id/ports                   → PublishPort
DELETE /v1/sandboxes/:id/ports/:port             → UnpublishPort
GET    /v1/sandboxes/:id/ports                   → ListPortBindings

POST   /v1/sandboxes/:id/sessions               → CreateSession
GET    /v1/sandboxes/:id/sessions               → ListSessions
GET    /v1/sessions/:id                          → GetSession
GET    /v1/sessions/:id/attach                   → AttachToSession (WS)
GET    /v1/sessions/:id/scrollback               → GetScrollback
POST   /v1/sessions/:id/input                    → SendInput
DELETE /v1/sessions/:id                          → DestroySession

POST   /v1/sandboxes/:id/snapshots               → CreateSnapshot
GET    /v1/sandboxes/:id/snapshots               → ListSnapshots
GET    /v1/snapshots/:id                         → GetSnapshot
POST   /v1/sandboxes/:id/snapshots/:sid/restore  → RestoreSnapshot
DELETE /v1/snapshots/:id                         → DeleteSnapshot

POST   /v1/images                                → CreateBaseImageFromSnapshot
POST   /v1/images/register                       → RegisterBaseImage
GET    /v1/images                                → ListBaseImages
GET    /v1/images/:id                            → GetBaseImage
DELETE /v1/images/:id                            → DeleteBaseImage

GET    /v1/operations                            → ListOperations
GET    /v1/operations/:id                        → GetOperation
POST   /v1/operations/:id/cancel                 → CancelOperation
GET    /v1/operations/:id/stream                 → StreamOperation (WS)

GET    /v1/events                                → StreamEvents (WS)
GET    /v1/health                                → Health
```

### 12.3 Request/response convention

```json
// Synchronous success (reads)
{
  "data": { ... }
}

// Asynchronous success (mutations)
{
  "operation": {
    "operation_id": "...",
    "type": "create_sandbox",
    "state": "pending",
    "resource_type": "sandbox",
    "resource_id": "..."
  }
}

// Error
{
  "error": {
    "code": "sandbox_not_found",
    "message": "sandbox abc-123 not found"
  }
}
```

Error codes are machine-readable strings. HTTP status codes are set appropriately. Agents parse the `code` field.

### 12.4 Auth middleware

Bearer token from `Authorization` header. In v1, a static secret generated at install time. Rejects unauthenticated requests with 401.

For WebSocket upgrade requests, the token may be passed as a `?token=` query parameter, since some WebSocket clients cannot set Authorization headers. The server accepts either mechanism.

### 12.4a Pagination

v1 list endpoints return all matching records without pagination. Response lists are expected to be small for local development. The response envelope reserves space for pagination metadata:

```json
{
  "data": [ ... ],
  "pagination": null
}
```

v2 will populate `pagination` with cursor/limit fields. SDK and CLI code should not assume the list is complete if `pagination` is non-null.

### 12.4b Project filtering

List endpoints that return project-scoped resources (`ListSandboxes`, `ListSessions`, `ListSnapshots`) require a `?project_id=` query parameter. Omitting it returns a 400 error. The CLI uses the configured `default_project` automatically. `ListOperations` accepts an optional `?project_id=` filter but does not require it.

### 12.5 Request ID

Every request gets a UUID set in `X-Request-ID` response header. Propagated through context to all layers. Logged on every log line. Returned in error responses for correlation.

### 12.6 WebSocket: exec streaming

```
Client → POST /sandboxes/:id/exec → 200 { operation }
Client → WS /operations/:id/stream
Server → { type: "stdout", data: "..." }
Server → { type: "stderr", data: "..." }
Server → { type: "exit", code: 0 }
(connection closes)
```

### 12.7 WebSocket: session attach

```
Client → WS /sessions/:id/attach
Server → (scrollback replay in binary frames)
(bidirectional PTY stream: binary frames for data, text frames for control)
Client → { type: "resize", width: 120, height: 40 }
```

### 12.8 WebSocket: event stream

```
GET /v1/events?project_id=xxx&type=sandbox_state_changed
```

```json
{
  "type": "sandbox_state_changed",
  "timestamp": "2026-03-28T15:00:00Z",
  "data": {
    "sandbox_id": "...",
    "old_state": "starting",
    "new_state": "running"
  }
}
```

### 12.9 Streaming surfaces

- `StreamEvents(scope)` — general-purpose event stream for sandbox, project, and system events
- `StreamOperation(id)` — convenience stream scoped to one operation, implemented as a filtered view over the general event stream

---

## 13. Worker System

### 13.1 Dispatcher

In-process operation queue using goroutines. No external queue dependency.

```go
type Dispatcher struct {
    store    domain.OperationStore
    events   domain.EventBus
    handlers map[string]OperationHandler
    sem      chan struct{}          // concurrency limiter
    cancel   context.CancelFunc
}
```

Concurrency: semaphore channel, configurable (default 10). Operations exceeding the limit stay in `Pending`.

### 13.2 Registered handlers

```
create_sandbox, start_sandbox, stop_sandbox, destroy_sandbox
create_snapshot, restore_snapshot, delete_snapshot
create_sandbox_from_snapshot
promote_snapshot, register_image, delete_image
exec
publish_port, unpublish_port
```

### 13.3 Startup recovery

On start, the dispatcher queries for operations in `Running` state (leftover from a crash):

- If backend shows completed → update to Succeeded/Failed
- If backend shows still running → re-attach monitoring
- If backend has no record → mark Failed

### 13.4 Garbage collection sweeper

Runs as a goroutine on a configurable ticker (default 5 minutes).

```go
type GCConfig struct {
    SweepInterval       time.Duration // default 5m
    SandboxTTL          time.Duration
    OperationRetention  time.Duration // default 7 days
    OrphanSnapshotTTL   time.Duration // default 24h
}
```

Each sweep:

1. Expired sandboxes: destroy via provider, update records
2. Orphaned snapshots: parent sandbox destroyed, not referenced by images. Delete after TTL.
3. Stale operations: terminal state older than retention period. Delete records.
4. Expired images: past retention policy. Delete from provider and store.

GC publishes events for observability but does not create Operation records.

### 13.5 Graceful shutdown

On SIGTERM/SIGINT:

1. Stop accepting new HTTP connections
2. Cancel dispatcher context
3. Wait for running handlers (hard timeout 30s)
4. Stop GC sweeper
5. Close store
6. Exit

---

## 14. CLI Design

### 14.1 Command structure

```
navaris
├── project create|list|get|update|delete
├── sandbox create|list|get|start|stop|destroy|exec|attach|port
├── session create|list|get|attach|scrollback|input|destroy
├── snapshot create|list|get|restore|delete
├── image list|get|promote|register|delete
├── operation list|get|cancel|stream
└── config
```

### 14.2 AI-agent-friendly design

**Structured output:** JSON to stdout in non-TTY mode. Human-readable table in TTY. Override with `--output json` or `--output text`.

**Exit codes:**

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Invalid arguments / usage error |
| 3 | Authentication failure |
| 4 | Resource not found |
| 5 | Conflict |
| 6 | Operation failed (after `--wait`) |
| 7 | Timeout (with `--wait --timeout`) |

**No interactive prompts** in non-TTY mode. Missing required arguments fail with exit 2.

**`--wait` and `--timeout`** on all mutating commands:

```bash
# Fire and forget (default)
navaris sandbox create --image ubuntu-24.04 --name dev-1
# → {"operation_id":"op-abc","state":"pending","resource_id":"sbx-123"}

# Wait for completion
navaris sandbox create --image ubuntu-24.04 --name dev-1 --wait
# → {"sandbox_id":"sbx-123","name":"dev-1","state":"running",...}

# Wait with timeout
navaris sandbox create --image ubuntu-24.04 --name dev-1 --wait --timeout 60s
# → exits 7 if timeout exceeded
```

`--wait` output: the final resource, not the operation.

**Exec:** propagates child exit code as CLI exit code.

```bash
# Non-interactive
navaris sandbox exec dev-1 -- ls -la /workspace

# Streaming JSON
navaris sandbox exec dev-1 --output json -- make build

# Interactive
navaris sandbox attach dev-1
```

**Sessions:**

```bash
navaris session create dev-1 --backing auto --shell /bin/bash
navaris session attach <session-id>
navaris session scrollback <session-id> --lines 100
navaris session input <session-id> "make build"
navaris session destroy <session-id>
```

### 14.3 Configuration

Config at `~/.config/navaris/config.json` (or `$NAVARIS_CONFIG`):

```json
{
  "api_url": "http://192.168.64.2:9400",
  "token": "...",
  "default_project": "default"
}
```

Priority: flags > env vars (`NAVARIS_API_URL`, `NAVARIS_TOKEN`, `NAVARIS_PROJECT`) > config file.

### 14.4 Framework

`cobra` for command parsing.

---

## 15. Go SDK (`pkg/client`)

### 15.1 Client construction

```go
client, err := navaris.NewClient(
    navaris.WithURL("http://192.168.64.2:9400"),
    navaris.WithToken("..."),
)
```

Functional options. Env vars as defaults if options not provided.

### 15.2 API surface

Mutating calls return `*Operation`. Read calls return the resource directly.

```go
// Projects
func (c *Client) CreateProject(ctx, req) (*Project, error)
func (c *Client) ListProjects(ctx) ([]*Project, error)
// ... Get, Update, Delete

// Sandboxes
func (c *Client) CreateSandbox(ctx, req) (*Operation, error)
func (c *Client) GetSandbox(ctx, id) (*Sandbox, error)
func (c *Client) ListSandboxes(ctx, filter) ([]*Sandbox, error)
func (c *Client) StartSandbox(ctx, id) (*Operation, error)
func (c *Client) StopSandbox(ctx, id, force) (*Operation, error)
func (c *Client) DestroySandbox(ctx, id) (*Operation, error)

// Exec
func (c *Client) Exec(ctx, sandboxID, req) (*Operation, error)
func (c *Client) AttachSession(ctx, sandboxID, opts) (*DirectSession, error)

// Sessions
func (c *Client) CreateSession(ctx, sandboxID, opts) (*Session, error)
func (c *Client) ListSessions(ctx, sandboxID) ([]*Session, error)
func (c *Client) GetSession(ctx, sessionID) (*Session, error)
func (c *Client) AttachToSession(ctx, sessionID) (*SessionConn, error)
func (c *Client) GetScrollback(ctx, sessionID, lines) (string, error)
func (c *Client) SendInput(ctx, sessionID, input) error
func (c *Client) DestroySession(ctx, sessionID) error

// Snapshots, Images, Operations, Ports
// ... same pattern
```

### 15.3 Waiting for operations

```go
// Poll-based
result, err := client.WaitForOperation(ctx, opID, WaitOptions{Timeout: 60*time.Second})

// Stream-based
err := client.StreamOperation(ctx, opID, func(event OperationEvent) error { ... })

// Convenience: create-and-wait
sandbox, err := client.CreateSandboxAndWait(ctx, req, WaitOptions{Timeout: 60*time.Second})
```

### 15.4 Session handle

`AttachToSession` returns `SessionConn`:

```go
type SessionConn struct { ... }
func (s *SessionConn) Read(p []byte) (int, error)   // stdout
func (s *SessionConn) Write(p []byte) (int, error)  // stdin
func (s *SessionConn) Resize(width, height int) error
func (s *SessionConn) Close() error
```

Implements `io.ReadWriteCloser`.

### 15.5 Types

SDK types in `pkg/client/types.go` are standalone. They mirror but do not import `internal/domain`. Clean public API boundary.

---

## 16. Error Handling and Observability

### 16.1 Error model

**Domain errors:** typed sentinels (`ErrNotFound`, `ErrConflict`, `ErrInvalidState`, `ErrCapacityExceeded`, `ErrUnauthorized`).

**Provider errors:** wrap backend failures with domain meaning. Internal Incus errors do not leak to clients.

**API mapping:**

| Domain Error | HTTP | Code |
|---|---|---|
| ErrNotFound | 404 | `resource_not_found` |
| ErrConflict | 409 | `conflict` |
| ErrInvalidState | 409 | `invalid_state` |
| ErrCapacityExceeded | 422 | `capacity_exceeded` |
| ErrUnauthorized | 401 | `unauthorized` |
| validation | 400 | `invalid_request` |
| unexpected | 500 | `internal_error` |

### 16.2 Logging

Structured JSON via `log/slog` (stdlib). Levels: Error, Warn, Info, Debug. Configurable via `navarisd --log-level`.

### 16.3 Health endpoint

`GET /v1/health` reports: control-plane status, provider health and latency, store health, worker queue depth.

### 16.4 Reconciliation

On startup:

1. Orphaned backend resources: log warning, do not auto-delete
2. Stale running sandbox states: update to match backend reality
3. Incomplete operations: re-evaluate against backend state
4. Tmux sessions: re-discover via `tmux list-sessions` in each running sandbox, restore session records

Runs once at startup. Normal operation keeps state in sync via event flow.

---

## 17. Networking

### 17.1 Control-plane networking

The control plane is exposed to macOS over a stable port on the VM (default 9400). The macOS client connects only to the control plane.

### 17.2 Sandbox access modes

- `isolated`: no inbound exposure to macOS, outbound only as allowed by backend
- `published`: selected ports published through host-level forwarding

### 17.3 Port publishing

Requested at creation time or runtime. The control plane allocates from a port range (40000-49999), maps via Incus proxy devices, and returns the reachable endpoint.

---

## 18. Resource Limits

Per-sandbox resource requests for CPU, memory, optional disk. If limits exceed configured capacity, creation fails. No silent overcommit.

---

## 19. Auth

v1: static local token, generated at install time. Preserves an auth boundary. Future: token-based auth with tenant scoping.

---

## 20. Storage Model

All state inside the Linux VM. Snapshots and images use Incus-native storage. Workspace volumes reserved in the model but not implemented in v1.

### 20.1 Retention and garbage collection

Control plane tracks orphaned snapshots, expired images, expired sandboxes, stale operations. GC runs as a periodic sweep (configurable interval and TTLs).

---

## 21. Security

- Client requests sandbox operations, not backend primitives
- Incus provides shared-kernel isolation
- Local Mac appliance is for development and testing
- Hostile multi-tenant isolation out of scope for v1

---

## 22. Snapshot and Image Model

### 22.1 Workflows

**Branching:** Snapshot a sandbox, create new sandboxes from that snapshot.

**Image promotion:** Snapshot a sandbox, promote the snapshot to a reusable base image.

### 22.2 Snapshot defaults

- Default: require sandbox to be stopped before snapshot
- Optional: live snapshots when explicitly requested (`consistency_mode: live`)

### 22.3 Compatibility

- Snapshots reusable within the same backend family only
- Cross-backend conversion out of scope for v1

---

## 23. Roadmap

### 23.1 v1

- Linux VM on Mac with control plane + Incus
- macOS client (CLI + SDK)
- SQLite metadata store
- Async-by-default mutating API with operation polling and streaming
- Sandbox CRUD from base images
- Persistent reconnectable sessions (direct PTY and tmux-backed)
- Snapshots with stop-before-snapshot default, optional live
- Restore, branch from snapshot, promote snapshot to image
- Exec with streaming, interactive attach
- Port publishing
- Multi-project
- Local auth token

### 23.2 v2

- Dedicated Linux server deployment
- PostgreSQL metadata store
- Firecracker provider
- Host registration
- Mixed fleet support
- Stronger auth and tenant scoping

### 23.3 v3

- Image baking pipeline
- Warm pools
- Image caching and replication
- Richer scheduling and placement

---

## 24. Future Architecture

The same control plane can later run on a Linux server managing Incus, Firecracker, or both. Fleet deployment adds a host model with capacity, health, and scheduling. Backend routing by isolation tier (`fast` → Incus, `strong` → Firecracker, `auto` → scheduler decides).

---

## 25. Open Questions (resolved)

| Question | Decision |
|---|---|
| Wire protocol | HTTP+JSON for CRUD, WebSocket for streaming |
| Base image scope | Global only in v1 |
| Promoted snapshot metadata | Inherits from snapshot record + name/version from promotion request |
| Workspace volumes | Reserved in model, not implemented in v1 |
| Backend debug detail | Not exposed to clients. Internal logs only. |
