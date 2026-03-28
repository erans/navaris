# Navaris Sandbox Control Plane Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a sandbox control plane with Incus backend, async operations, persistent sessions, and AI-agent-friendly CLI/SDK.

**Architecture:** Hexagonal / ports-and-adapters. Domain types and interfaces in `internal/domain` (zero deps). SQLite and Incus are adapters behind interfaces. Services orchestrate business logic. HTTP+JSON API with WebSocket streaming. Two binaries: `navarisd` (daemon) and `navaris` (CLI).

**Tech Stack:** Go 1.24+, `modernc.org/sqlite` (pure Go SQLite), `github.com/lxc/incus/client` (Incus Go SDK), `github.com/spf13/cobra` (CLI), `nhooyr.io/websocket` (WebSocket), `log/slog` (logging).

**Spec:** `docs/superpowers/specs/2026-03-28-sandbox-control-plane-design.md`

---

## File Structure

```
navaris/
├── cmd/
│   ├── navaris/
│   │   └── main.go                          CLI entry point
│   └── navarisd/
│       └── main.go                          Daemon entry point, DI wiring
├── pkg/
│   └── client/
│       ├── client.go                        Client struct, NewClient, options
│       ├── types.go                         Public API types (Sandbox, Snapshot, etc.)
│       ├── sandbox.go                       Sandbox CRUD + start/stop/destroy
│       ├── snapshot.go                      Snapshot CRUD + restore
│       ├── image.go                         Image CRUD + promote
│       ├── session.go                       Session CRUD + attach + scrollback + input
│       ├── operation.go                     Operation get/list/cancel/wait/stream
│       ├── project.go                       Project CRUD
│       ├── exec.go                          Exec + streaming
│       ├── port.go                          Port publish/unpublish/list
│       └── client_test.go                   SDK integration tests (against real server)
├── internal/
│   ├── domain/
│   │   ├── sandbox.go                       Sandbox type, states, SandboxFilter
│   │   ├── snapshot.go                      Snapshot type, states, ConsistencyMode
│   │   ├── image.go                         BaseImage type, states, ImageFilter
│   │   ├── session.go                       Session type, states, backing modes
│   │   ├── operation.go                     Operation type, states, OperationFilter
│   │   ├── project.go                       Project type
│   │   ├── port.go                          PortBinding type
│   │   ├── event.go                         Event types, EventFilter
│   │   ├── errors.go                        Sentinel errors
│   │   ├── store.go                         All store interfaces
│   │   ├── provider.go                      Provider interface, handle types
│   │   └── eventbus.go                      EventBus interface
│   ├── store/
│   │   ├── store.go                         Store aggregate (holds all sub-stores)
│   │   └── sqlite/
│   │       ├── sqlite.go                    SQLiteStore, Open, Close, migration runner
│   │       ├── project.go                   ProjectStore implementation
│   │       ├── sandbox.go                   SandboxStore implementation
│   │       ├── snapshot.go                  SnapshotStore implementation
│   │       ├── session.go                   SessionStore implementation
│   │       ├── image.go                     ImageStore implementation
│   │       ├── operation.go                 OperationStore implementation
│   │       ├── port.go                      PortBindingStore implementation
│   │       ├── sqlite_test.go               Shared test setup (in-memory DB)
│   │       ├── project_test.go              ProjectStore tests
│   │       ├── sandbox_test.go              SandboxStore tests
│   │       ├── snapshot_test.go             SnapshotStore tests
│   │       ├── session_test.go              SessionStore tests
│   │       ├── image_test.go                ImageStore tests
│   │       ├── operation_test.go            OperationStore tests
│   │       ├── port_test.go                 PortBindingStore tests
│   │       └── migrations/
│   │           └── 001_initial.sql          Initial schema
│   ├── eventbus/
│   │   ├── memory.go                        In-memory EventBus implementation
│   │   └── memory_test.go                   EventBus tests
│   ├── worker/
│   │   ├── dispatcher.go                    Operation dispatcher, semaphore, handler registry
│   │   ├── dispatcher_test.go               Dispatcher unit tests
│   │   ├── gc.go                            GC sweeper
│   │   └── gc_test.go                       GC tests
│   ├── provider/
│   │   ├── mock.go                          Mock provider for testing
│   │   └── incus/
│   │       ├── incus.go                     IncusProvider constructor, Health
│   │       ├── sandbox.go                   Sandbox lifecycle methods
│   │       ├── snapshot.go                  Snapshot methods
│   │       ├── image.go                     Image methods
│   │       ├── exec.go                      Exec + ExecDetached + AttachSession
│   │       ├── network.go                   Port publish/unpublish
│   │       └── incus_test.go                Integration tests (require live Incus)
│   ├── service/
│   │   ├── project.go                       ProjectService
│   │   ├── project_test.go                  ProjectService tests
│   │   ├── sandbox.go                       SandboxService
│   │   ├── sandbox_test.go                  SandboxService tests
│   │   ├── snapshot.go                      SnapshotService
│   │   ├── snapshot_test.go                 SnapshotService tests
│   │   ├── image.go                         ImageService
│   │   ├── image_test.go                    ImageService tests
│   │   ├── session.go                       SessionService
│   │   ├── session_test.go                  SessionService tests
│   │   ├── operation.go                     OperationService
│   │   └── operation_test.go                OperationService tests
│   └── api/
│       ├── server.go                        Server struct, routing, ListenAndServe
│       ├── middleware.go                     Auth, request ID, logging, error mapping
│       ├── response.go                      JSON envelope helpers (data, operation, error)
│       ├── project.go                       Project handlers
│       ├── project_test.go                  Project handler tests
│       ├── sandbox.go                       Sandbox handlers
│       ├── sandbox_test.go                  Sandbox handler tests
│       ├── snapshot.go                      Snapshot handlers
│       ├── snapshot_test.go                 Snapshot handler tests
│       ├── image.go                         Image handlers
│       ├── image_test.go                    Image handler tests
│       ├── session.go                       Session handlers
│       ├── session_test.go                  Session handler tests
│       ├── operation.go                     Operation handlers
│       ├── operation_test.go                Operation handler tests
│       ├── port.go                          Port handlers
│       ├── port_test.go                     Port handler tests
│       ├── exec.go                          Exec handler + operation stream WS
│       ├── exec_test.go                     Exec handler tests
│       ├── events.go                        Event stream WS handler
│       ├── events_test.go                   Event stream tests
│       └── health.go                        Health endpoint
├── go.mod
└── go.sum
```

---

## Phase 1: Project Setup and Domain Types

### Task 1: Initialize Go module and dependencies

**Files:**
- Create: `go.mod`
- Create: `go.sum`

- [ ] **Step 1: Initialize Go module**

```bash
cd /home/eran/work/navaris
go mod init github.com/navaris/navaris
```

- [ ] **Step 2: Add core dependencies**

```bash
go get modernc.org/sqlite
go get nhooyr.io/websocket
go get github.com/spf13/cobra
go get github.com/google/uuid
```

Note: The Incus client library (`github.com/lxc/incus/v6/client`) will be added in the provider phase. It has a large dependency tree and is not needed for domain, store, or service tests.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "feat: initialize Go module with core dependencies"
```

---

### Task 2: Domain errors

**Files:**
- Create: `internal/domain/errors.go`
- Test: `internal/domain/errors_test.go`

- [ ] **Step 1: Write test for domain errors**

```go
// internal/domain/errors_test.go
package domain_test

import (
    "errors"
    "fmt"
    "testing"

    "github.com/navaris/navaris/internal/domain"
)

func TestErrorWrapping(t *testing.T) {
    wrapped := fmt.Errorf("sandbox abc: %w", domain.ErrNotFound)
    if !errors.Is(wrapped, domain.ErrNotFound) {
        t.Fatal("expected wrapped error to match ErrNotFound")
    }
}

func TestErrorsAreDistinct(t *testing.T) {
    if errors.Is(domain.ErrNotFound, domain.ErrConflict) {
        t.Fatal("ErrNotFound should not match ErrConflict")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/domain/ -run TestError -v
```
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Implement domain errors**

```go
// internal/domain/errors.go
package domain

import "errors"

var (
    ErrNotFound         = errors.New("not found")
    ErrConflict         = errors.New("conflict")
    ErrInvalidState     = errors.New("invalid state transition")
    ErrCapacityExceeded = errors.New("capacity exceeded")
    ErrUnauthorized     = errors.New("unauthorized")
)
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/domain/ -run TestError -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/errors.go internal/domain/errors_test.go
git commit -m "feat: add domain error sentinels"
```

---

### Task 3: Domain types — Project

**Files:**
- Create: `internal/domain/project.go`

- [ ] **Step 1: Write Project type**

```go
// internal/domain/project.go
package domain

import "time"

type Project struct {
    ProjectID string
    Name      string
    CreatedAt time.Time
    UpdatedAt time.Time
    Metadata  map[string]any
}
```

No test needed — pure data struct, will be tested through store tests.

- [ ] **Step 2: Commit**

```bash
git add internal/domain/project.go
git commit -m "feat: add Project domain type"
```

---

### Task 4: Domain types — Sandbox

**Files:**
- Create: `internal/domain/sandbox.go`
- Test: `internal/domain/sandbox_test.go`

- [ ] **Step 1: Write test for sandbox state validation**

```go
// internal/domain/sandbox_test.go
package domain_test

import (
    "testing"

    "github.com/navaris/navaris/internal/domain"
)

func TestSandboxStateValid(t *testing.T) {
    valid := []domain.SandboxState{
        domain.SandboxPending,
        domain.SandboxStarting,
        domain.SandboxRunning,
        domain.SandboxStopping,
        domain.SandboxStopped,
        domain.SandboxFailed,
        domain.SandboxDestroyed,
    }
    for _, s := range valid {
        if !s.Valid() {
            t.Errorf("expected %q to be valid", s)
        }
    }
    if domain.SandboxState("bogus").Valid() {
        t.Error("expected bogus state to be invalid")
    }
}

func TestSandboxCanTransitionTo(t *testing.T) {
    tests := []struct {
        from, to domain.SandboxState
        ok       bool
    }{
        {domain.SandboxPending, domain.SandboxStarting, true},
        {domain.SandboxStarting, domain.SandboxRunning, true},
        {domain.SandboxRunning, domain.SandboxStopping, true},
        {domain.SandboxStopping, domain.SandboxStopped, true},
        {domain.SandboxStopped, domain.SandboxStarting, true},
        {domain.SandboxStopped, domain.SandboxDestroyed, true},
        {domain.SandboxStarting, domain.SandboxFailed, true},
        {domain.SandboxRunning, domain.SandboxFailed, true},
        {domain.SandboxStopping, domain.SandboxFailed, true},
        // Invalid transitions
        {domain.SandboxPending, domain.SandboxRunning, false},
        {domain.SandboxDestroyed, domain.SandboxRunning, false},
        {domain.SandboxRunning, domain.SandboxPending, false},
    }
    for _, tt := range tests {
        got := tt.from.CanTransitionTo(tt.to)
        if got != tt.ok {
            t.Errorf("%s → %s: got %v, want %v", tt.from, tt.to, got, tt.ok)
        }
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/domain/ -run TestSandbox -v
```

- [ ] **Step 3: Implement Sandbox type**

```go
// internal/domain/sandbox.go
package domain

import "time"

type SandboxState string

const (
    SandboxPending   SandboxState = "pending"
    SandboxStarting  SandboxState = "starting"
    SandboxRunning   SandboxState = "running"
    SandboxStopping  SandboxState = "stopping"
    SandboxStopped   SandboxState = "stopped"
    SandboxFailed    SandboxState = "failed"
    SandboxDestroyed SandboxState = "destroyed"
)

func (s SandboxState) Valid() bool {
    switch s {
    case SandboxPending, SandboxStarting, SandboxRunning,
        SandboxStopping, SandboxStopped, SandboxFailed, SandboxDestroyed:
        return true
    }
    return false
}

var sandboxTransitions = map[SandboxState][]SandboxState{
    SandboxPending:  {SandboxStarting, SandboxFailed},
    SandboxStarting: {SandboxRunning, SandboxFailed},
    SandboxRunning:  {SandboxStopping, SandboxFailed},
    SandboxStopping: {SandboxStopped, SandboxFailed},
    SandboxStopped:  {SandboxStarting, SandboxDestroyed},
    SandboxFailed:   {SandboxDestroyed},
}

func (s SandboxState) CanTransitionTo(target SandboxState) bool {
    for _, allowed := range sandboxTransitions[s] {
        if allowed == target {
            return true
        }
    }
    return false
}

type NetworkMode string

const (
    NetworkIsolated  NetworkMode = "isolated"
    NetworkPublished NetworkMode = "published"
)

type Sandbox struct {
    SandboxID        string
    ProjectID        string
    Name             string
    State            SandboxState
    Backend          string
    BackendRef       string
    HostID           string
    SourceImageID    string
    ParentSnapshotID string
    CreatedAt        time.Time
    UpdatedAt        time.Time
    ExpiresAt        *time.Time
    CPULimit         *int
    MemoryLimitMB    *int
    NetworkMode      NetworkMode
    Metadata         map[string]any
}

type SandboxFilter struct {
    ProjectID *string
    State     *SandboxState
    Backend   *string
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/domain/ -run TestSandbox -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/domain/sandbox.go internal/domain/sandbox_test.go
git commit -m "feat: add Sandbox domain type with state machine"
```

---

### Task 5: Domain types — Snapshot, Session, BaseImage, Operation, PortBinding, Event

**Files:**
- Create: `internal/domain/snapshot.go`
- Create: `internal/domain/session.go`
- Create: `internal/domain/image.go`
- Create: `internal/domain/operation.go`
- Create: `internal/domain/port.go`
- Create: `internal/domain/event.go`
- Test: `internal/domain/snapshot_test.go`
- Test: `internal/domain/session_test.go`
- Test: `internal/domain/operation_test.go`

Follow the same pattern as Task 4 for each type. Key details:

- [ ] **Step 1: Write tests for Snapshot state machine**

```go
// internal/domain/snapshot_test.go
package domain_test

import (
    "testing"
    "github.com/navaris/navaris/internal/domain"
)

func TestSnapshotStateTransitions(t *testing.T) {
    tests := []struct {
        from, to domain.SnapshotState
        ok       bool
    }{
        {domain.SnapshotPending, domain.SnapshotCreating, true},
        {domain.SnapshotCreating, domain.SnapshotReady, true},
        {domain.SnapshotCreating, domain.SnapshotFailed, true},
        {domain.SnapshotReady, domain.SnapshotDeleted, true},
        {domain.SnapshotPending, domain.SnapshotReady, false},
    }
    for _, tt := range tests {
        if got := tt.from.CanTransitionTo(tt.to); got != tt.ok {
            t.Errorf("%s → %s: got %v, want %v", tt.from, tt.to, got, tt.ok)
        }
    }
}
```

- [ ] **Step 2: Implement Snapshot type**

```go
// internal/domain/snapshot.go
package domain

import "time"

type SnapshotState string

const (
    SnapshotPending  SnapshotState = "pending"
    SnapshotCreating SnapshotState = "creating"
    SnapshotReady    SnapshotState = "ready"
    SnapshotFailed   SnapshotState = "failed"
    SnapshotDeleted  SnapshotState = "deleted"
)

func (s SnapshotState) Valid() bool {
    switch s {
    case SnapshotPending, SnapshotCreating, SnapshotReady, SnapshotFailed, SnapshotDeleted:
        return true
    }
    return false
}

var snapshotTransitions = map[SnapshotState][]SnapshotState{
    SnapshotPending:  {SnapshotCreating, SnapshotFailed},
    SnapshotCreating: {SnapshotReady, SnapshotFailed},
    SnapshotReady:    {SnapshotDeleted},
    SnapshotFailed:   {SnapshotDeleted},
}

func (s SnapshotState) CanTransitionTo(target SnapshotState) bool {
    for _, allowed := range snapshotTransitions[s] {
        if allowed == target {
            return true
        }
    }
    return false
}

type ConsistencyMode string

const (
    ConsistencyStopped ConsistencyMode = "stopped"
    ConsistencyLive    ConsistencyMode = "live"
)

type Snapshot struct {
    SnapshotID      string
    SandboxID       string
    Backend         string
    BackendRef      string
    Label           string
    State           SnapshotState
    CreatedAt       time.Time
    UpdatedAt       time.Time
    ParentImageID   string
    Publishable     bool
    ConsistencyMode ConsistencyMode
    Metadata        map[string]any
}
```

- [ ] **Step 3: Write tests for Session state machine**

```go
// internal/domain/session_test.go
package domain_test

import (
    "testing"
    "github.com/navaris/navaris/internal/domain"
)

func TestSessionStateTransitions(t *testing.T) {
    tests := []struct {
        from, to domain.SessionState
        ok       bool
    }{
        {domain.SessionActive, domain.SessionDetached, true},
        {domain.SessionDetached, domain.SessionActive, true},
        {domain.SessionActive, domain.SessionExited, true},
        {domain.SessionDetached, domain.SessionExited, true},
        {domain.SessionExited, domain.SessionDestroyed, true},
        {domain.SessionExited, domain.SessionActive, false},
    }
    for _, tt := range tests {
        if got := tt.from.CanTransitionTo(tt.to); got != tt.ok {
            t.Errorf("%s → %s: got %v, want %v", tt.from, tt.to, got, tt.ok)
        }
    }
}
```

- [ ] **Step 4: Implement Session type**

```go
// internal/domain/session.go
package domain

import "time"

type SessionState string

const (
    SessionActive    SessionState = "active"
    SessionDetached  SessionState = "detached"
    SessionExited    SessionState = "exited"
    SessionDestroyed SessionState = "destroyed"
)

func (s SessionState) Valid() bool {
    switch s {
    case SessionActive, SessionDetached, SessionExited, SessionDestroyed:
        return true
    }
    return false
}

var sessionTransitions = map[SessionState][]SessionState{
    SessionActive:   {SessionDetached, SessionExited, SessionDestroyed},
    SessionDetached: {SessionActive, SessionExited, SessionDestroyed},
    SessionExited:   {SessionDestroyed},
}

func (s SessionState) CanTransitionTo(target SessionState) bool {
    for _, allowed := range sessionTransitions[s] {
        if allowed == target {
            return true
        }
    }
    return false
}

type SessionBacking string

const (
    SessionBackingDirect SessionBacking = "direct"
    SessionBackingTmux   SessionBacking = "tmux"
    SessionBackingAuto   SessionBacking = "auto"
)

type Session struct {
    SessionID      string
    SandboxID      string
    Backing        SessionBacking
    Shell          string
    State          SessionState
    CreatedAt      time.Time
    UpdatedAt      time.Time
    LastAttachedAt *time.Time
    IdleTimeout    *time.Duration
    Metadata       map[string]any
}
```

- [ ] **Step 5: Implement Operation, BaseImage, PortBinding, Event types**

```go
// internal/domain/operation.go
package domain

import "time"

type OperationState string

const (
    OpPending   OperationState = "pending"
    OpRunning   OperationState = "running"
    OpSucceeded OperationState = "succeeded"
    OpFailed    OperationState = "failed"
    OpCancelled OperationState = "cancelled"
)

func (s OperationState) Valid() bool {
    switch s {
    case OpPending, OpRunning, OpSucceeded, OpFailed, OpCancelled:
        return true
    }
    return false
}

func (s OperationState) Terminal() bool {
    return s == OpSucceeded || s == OpFailed || s == OpCancelled
}

type Operation struct {
    OperationID  string
    ResourceType string
    ResourceID   string
    SandboxID    string
    SnapshotID   string
    Type         string
    State        OperationState
    StartedAt    time.Time
    FinishedAt   *time.Time
    ErrorText    string
    Metadata     map[string]any
}

type OperationFilter struct {
    ResourceType *string
    ResourceID   *string
    SandboxID    *string
    State        *OperationState
    Limit        int
}
```

```go
// internal/domain/image.go
package domain

import "time"

type ImageState string

const (
    ImagePending ImageState = "pending"
    ImageReady   ImageState = "ready"
    ImageFailed  ImageState = "failed"
    ImageDeleted ImageState = "deleted"
)

func (s ImageState) Valid() bool {
    switch s {
    case ImagePending, ImageReady, ImageFailed, ImageDeleted:
        return true
    }
    return false
}

type SourceType string

const (
    SourceImported         SourceType = "imported"
    SourceSnapshotPromoted SourceType = "snapshot_promoted"
)

type BaseImage struct {
    ImageID          string
    ProjectScope     string
    Name             string
    Version          string
    SourceType       SourceType
    SourceSnapshotID string
    Backend          string
    BackendRef       string
    Architecture     string
    State            ImageState
    CreatedAt        time.Time
    Metadata         map[string]any
}

type ImageFilter struct {
    Name         *string
    Architecture *string
    State        *ImageState
}
```

```go
// internal/domain/port.go
package domain

import "time"

type PortBinding struct {
    SandboxID     string
    TargetPort    int
    PublishedPort int
    HostAddress   string
    CreatedAt     time.Time
}
```

```go
// internal/domain/event.go
package domain

import "time"

type EventType string

const (
    EventSandboxStateChanged   EventType = "sandbox_state_changed"
    EventSnapshotStateChanged  EventType = "snapshot_state_changed"
    EventImageStateChanged     EventType = "image_state_changed"
    EventSessionStateChanged   EventType = "session_state_changed"
    EventOperationStateChanged EventType = "operation_state_changed"
    EventExecOutput            EventType = "exec_output"
    EventExecCompleted         EventType = "exec_completed"
)

type Event struct {
    Type      EventType
    Timestamp time.Time
    Data      map[string]any
}

type EventFilter struct {
    ProjectID *string
    SandboxID *string
    Types     []EventType
}
```

- [ ] **Step 6: Write Operation state test**

```go
// internal/domain/operation_test.go
package domain_test

import (
    "testing"
    "github.com/navaris/navaris/internal/domain"
)

func TestOperationStateTerminal(t *testing.T) {
    terminal := []domain.OperationState{domain.OpSucceeded, domain.OpFailed, domain.OpCancelled}
    for _, s := range terminal {
        if !s.Terminal() {
            t.Errorf("expected %s to be terminal", s)
        }
    }
    nonTerminal := []domain.OperationState{domain.OpPending, domain.OpRunning}
    for _, s := range nonTerminal {
        if s.Terminal() {
            t.Errorf("expected %s to be non-terminal", s)
        }
    }
}
```

- [ ] **Step 7: Run all domain tests**

```bash
go test ./internal/domain/ -v
```
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/domain/
git commit -m "feat: add all domain types with state machines"
```

---

### Task 6: Domain interfaces — Store

**Files:**
- Create: `internal/domain/store.go`

- [ ] **Step 1: Write all store interfaces**

```go
// internal/domain/store.go
package domain

import (
    "context"
    "time"
)

type ProjectStore interface {
    Create(ctx context.Context, p *Project) error
    Get(ctx context.Context, id string) (*Project, error)
    GetByName(ctx context.Context, name string) (*Project, error)
    List(ctx context.Context) ([]*Project, error)
    Update(ctx context.Context, p *Project) error
    Delete(ctx context.Context, id string) error
}

type SandboxStore interface {
    Create(ctx context.Context, s *Sandbox) error
    Get(ctx context.Context, id string) (*Sandbox, error)
    List(ctx context.Context, f SandboxFilter) ([]*Sandbox, error)
    Update(ctx context.Context, s *Sandbox) error
    Delete(ctx context.Context, id string) error
    ListExpired(ctx context.Context, now time.Time) ([]*Sandbox, error)
}

type SnapshotStore interface {
    Create(ctx context.Context, s *Snapshot) error
    Get(ctx context.Context, id string) (*Snapshot, error)
    ListBySandbox(ctx context.Context, sandboxID string) ([]*Snapshot, error)
    Update(ctx context.Context, s *Snapshot) error
    Delete(ctx context.Context, id string) error
    ListOrphaned(ctx context.Context) ([]*Snapshot, error)
}

type SessionStore interface {
    Create(ctx context.Context, s *Session) error
    Get(ctx context.Context, id string) (*Session, error)
    ListBySandbox(ctx context.Context, sandboxID string) ([]*Session, error)
    Update(ctx context.Context, s *Session) error
    Delete(ctx context.Context, id string) error
}

type ImageStore interface {
    Create(ctx context.Context, i *BaseImage) error
    Get(ctx context.Context, id string) (*BaseImage, error)
    List(ctx context.Context, f ImageFilter) ([]*BaseImage, error)
    Update(ctx context.Context, i *BaseImage) error
    Delete(ctx context.Context, id string) error
}

type OperationStore interface {
    Create(ctx context.Context, o *Operation) error
    Get(ctx context.Context, id string) (*Operation, error)
    List(ctx context.Context, f OperationFilter) ([]*Operation, error)
    Update(ctx context.Context, o *Operation) error
    ListStale(ctx context.Context, olderThan time.Time) ([]*Operation, error)
    ListByState(ctx context.Context, state OperationState) ([]*Operation, error)
}

type PortBindingStore interface {
    Create(ctx context.Context, pb *PortBinding) error
    ListBySandbox(ctx context.Context, sandboxID string) ([]*PortBinding, error)
    Delete(ctx context.Context, sandboxID string, targetPort int) error
    GetByPublishedPort(ctx context.Context, publishedPort int) (*PortBinding, error)
    NextAvailablePort(ctx context.Context, rangeStart, rangeEnd int) (int, error)
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/domain/store.go
git commit -m "feat: add store interfaces to domain"
```

---

### Task 7: Domain interfaces — Provider and EventBus

**Files:**
- Create: `internal/domain/provider.go`
- Create: `internal/domain/eventbus.go`

- [ ] **Step 1: Write Provider interface with handle types**

```go
// internal/domain/provider.go
package domain

import (
    "context"
    "io"
)

type BackendRef struct {
    Backend string
    Ref     string
}

type CreateSandboxRequest struct {
    Name          string
    ImageRef      string
    CPULimit      *int
    MemoryLimitMB *int
    NetworkMode   NetworkMode
    Metadata      map[string]any
}

type ExecRequest struct {
    Command []string
    Env     map[string]string
    WorkDir string
}

type SessionRequest struct {
    Shell string
}

type PublishPortOptions struct{}

type PublishImageRequest struct {
    Name    string
    Version string
}

type PublishedEndpoint struct {
    HostAddress   string
    PublishedPort int
}

type ExecHandle struct {
    Stdout io.ReadCloser
    Stderr io.ReadCloser
    Wait   func() (exitCode int, err error)
    Cancel func() error
}

type DetachedExecHandle struct {
    Stdin  io.WriteCloser
    Stdout io.ReadCloser
    Resize func(w, h int) error
    Close  func() error
}

type SessionHandle struct {
    Conn   io.ReadWriteCloser
    Resize func(w, h int) error
    Close  func() error
}

type ImageInfo struct {
    Architecture string
    Size         int64
}

type ProviderHealth struct {
    Backend   string
    Healthy   bool
    LatencyMS int64
    Error     string
}

type Provider interface {
    CreateSandbox(ctx context.Context, req CreateSandboxRequest) (BackendRef, error)
    StartSandbox(ctx context.Context, ref BackendRef) error
    StopSandbox(ctx context.Context, ref BackendRef, force bool) error
    DestroySandbox(ctx context.Context, ref BackendRef) error
    GetSandboxState(ctx context.Context, ref BackendRef) (SandboxState, error)

    Exec(ctx context.Context, ref BackendRef, req ExecRequest) (ExecHandle, error)
    ExecDetached(ctx context.Context, ref BackendRef, req ExecRequest) (DetachedExecHandle, error)
    AttachSession(ctx context.Context, ref BackendRef, req SessionRequest) (SessionHandle, error)

    CreateSnapshot(ctx context.Context, ref BackendRef, label string, mode ConsistencyMode) (BackendRef, error)
    RestoreSnapshot(ctx context.Context, sandboxRef BackendRef, snapshotRef BackendRef) error
    DeleteSnapshot(ctx context.Context, snapshotRef BackendRef) error

    CreateSandboxFromSnapshot(ctx context.Context, snapshotRef BackendRef, req CreateSandboxRequest) (BackendRef, error)
    PublishSnapshotAsImage(ctx context.Context, snapshotRef BackendRef, req PublishImageRequest) (BackendRef, error)
    DeleteImage(ctx context.Context, imageRef BackendRef) error
    GetImageInfo(ctx context.Context, imageRef BackendRef) (ImageInfo, error)

    PublishPort(ctx context.Context, ref BackendRef, targetPort int, opts PublishPortOptions) (PublishedEndpoint, error)
    UnpublishPort(ctx context.Context, ref BackendRef, publishedPort int) error

    Health(ctx context.Context) ProviderHealth
}
```

- [ ] **Step 2: Write EventBus interface**

```go
// internal/domain/eventbus.go
package domain

import "context"

type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(ctx context.Context, filter EventFilter) (<-chan Event, func(), error)
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/domain/provider.go internal/domain/eventbus.go
git commit -m "feat: add Provider and EventBus interfaces to domain"
```

---

## Phase 2: SQLite Store

### Task 8: SQLite setup, migration runner, and Store aggregate interface

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/sqlite/sqlite.go`
- Create: `internal/store/sqlite/migrations/001_initial.sql`
- Create: `internal/store/sqlite/sqlite_test.go`

- [ ] **Step 1: Write Store aggregate interface**

```go
// internal/store/store.go
package store

import "github.com/navaris/navaris/internal/domain"

type Store interface {
    ProjectStore() domain.ProjectStore
    SandboxStore() domain.SandboxStore
    SnapshotStore() domain.SnapshotStore
    SessionStore() domain.SessionStore
    ImageStore() domain.ImageStore
    OperationStore() domain.OperationStore
    PortBindingStore() domain.PortBindingStore
    Close() error
}
```

- [ ] **Step 2: Write test for DB open and migration**

```go
// internal/store/sqlite/sqlite_test.go
package sqlite_test

import (
    "testing"

    "github.com/navaris/navaris/internal/store/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
    t.Helper()
    s, err := sqlite.Open(":memory:")
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { s.Close() })
    return s
}

func TestOpen(t *testing.T) {
    s := newTestStore(t)
    // Verify tables exist by querying sqlite_master
    rows, err := s.DB().QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
    if err != nil {
        t.Fatal(err)
    }
    defer rows.Close()
    var tables []string
    for rows.Next() {
        var name string
        rows.Scan(&name)
        tables = append(tables, name)
    }
    expected := []string{"base_images", "operations", "port_bindings", "projects", "sandboxes", "schema_migrations", "sessions", "snapshots"}
    if len(tables) != len(expected) {
        t.Fatalf("expected %d tables, got %d: %v", len(expected), len(tables), tables)
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/store/sqlite/ -run TestOpen -v
```

- [ ] **Step 4: Create migration SQL**

```sql
-- internal/store/sqlite/migrations/001_initial.sql
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

- [ ] **Step 5: Implement sqlite.go with Open, Close, migration runner**

```go
// internal/store/sqlite/sqlite.go
package sqlite

import (
    "database/sql"
    "embed"
    "fmt"
    "sort"
    "strings"

    _ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
    db *sql.DB
}

func Open(dsn string) (*Store, error) {
    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, fmt.Errorf("open sqlite: %w", err)
    }

    // Enable WAL, foreign keys, busy timeout
    for _, pragma := range []string{
        "PRAGMA journal_mode=WAL",
        "PRAGMA foreign_keys=ON",
        "PRAGMA busy_timeout=5000",
    } {
        if _, err := db.Exec(pragma); err != nil {
            db.Close()
            return nil, fmt.Errorf("exec %s: %w", pragma, err)
        }
    }

    s := &Store{db: db}
    if err := s.migrate(); err != nil {
        db.Close()
        return nil, fmt.Errorf("migrate: %w", err)
    }
    return s, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
    _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
        version TEXT PRIMARY KEY,
        applied_at TEXT NOT NULL DEFAULT (datetime('now'))
    )`)
    if err != nil {
        return err
    }

    entries, err := migrations.ReadDir("migrations")
    if err != nil {
        return err
    }
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].Name() < entries[j].Name()
    })

    for _, entry := range entries {
        if !strings.HasSuffix(entry.Name(), ".sql") {
            continue
        }
        var count int
        s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", entry.Name()).Scan(&count)
        if count > 0 {
            continue
        }
        content, err := migrations.ReadFile("migrations/" + entry.Name())
        if err != nil {
            return err
        }
        tx, err := s.db.Begin()
        if err != nil {
            return err
        }
        if _, err := tx.Exec(string(content)); err != nil {
            tx.Rollback()
            return fmt.Errorf("migration %s: %w", entry.Name(), err)
        }
        if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", entry.Name()); err != nil {
            tx.Rollback()
            return err
        }
        if err := tx.Commit(); err != nil {
            return err
        }
    }
    return nil
}
```

- [ ] **Step 6: Run test to verify it passes**

```bash
go test ./internal/store/sqlite/ -run TestOpen -v
```

- [ ] **Step 7: Add Store aggregate accessors to SQLite Store**

The `Store` struct needs to implement the `store.Store` interface with typed accessors. Add stub accessor methods that return sub-store types (implemented in subsequent tasks):

```go
// Add to sqlite.go
var _ store.Store = (*Store)(nil) // compile-time check

func (s *Store) ProjectStore() domain.ProjectStore   { return &projectStore{db: s.db} }
func (s *Store) SandboxStore() domain.SandboxStore   { return &sandboxStore{db: s.db} }
func (s *Store) SnapshotStore() domain.SnapshotStore { return &snapshotStore{db: s.db} }
func (s *Store) SessionStore() domain.SessionStore   { return &sessionStore{db: s.db} }
func (s *Store) ImageStore() domain.ImageStore       { return &imageStore{db: s.db} }
func (s *Store) OperationStore() domain.OperationStore { return &operationStore{db: s.db} }
func (s *Store) PortBindingStore() domain.PortBindingStore { return &portBindingStore{db: s.db} }
```

Create stub types for each sub-store (empty structs with `db *sql.DB` field). They won't compile until their methods are implemented in subsequent tasks, so initially use `//go:build ignore` or implement the methods as panics — then replace with real implementations in Tasks 9-15.

Pragmatic alternative: skip the compile-time check until all sub-stores are implemented (Task 15), and have earlier tests construct sub-stores directly.

- [ ] **Step 8: Commit**

```bash
git add internal/store/ internal/store/sqlite/
git commit -m "feat: add SQLite store with migration runner, schema, and Store interface"
```

---

### Task 9: ProjectStore implementation

**Files:**
- Create: `internal/store/sqlite/project.go`
- Create: `internal/store/sqlite/project_test.go`

- [ ] **Step 1: Write tests for Project CRUD**

```go
// internal/store/sqlite/project_test.go
package sqlite_test

import (
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/navaris/navaris/internal/domain"
)

func TestProjectCreate(t *testing.T) {
    s := newTestStore(t)
    p := &domain.Project{
        ProjectID: uuid.NewString(),
        Name:      "test-project",
        CreatedAt: time.Now().UTC(),
        UpdatedAt: time.Now().UTC(),
    }
    if err := s.ProjectStore().Create(t.Context(), p); err != nil {
        t.Fatal(err)
    }
    got, err := s.ProjectStore().Get(t.Context(), p.ProjectID)
    if err != nil {
        t.Fatal(err)
    }
    if got.Name != p.Name {
        t.Errorf("got name %q, want %q", got.Name, p.Name)
    }
}

func TestProjectGetByName(t *testing.T) {
    s := newTestStore(t)
    p := &domain.Project{
        ProjectID: uuid.NewString(),
        Name:      "by-name",
        CreatedAt: time.Now().UTC(),
        UpdatedAt: time.Now().UTC(),
    }
    s.ProjectStore().Create(t.Context(), p)
    got, err := s.ProjectStore().GetByName(t.Context(), "by-name")
    if err != nil {
        t.Fatal(err)
    }
    if got.ProjectID != p.ProjectID {
        t.Error("wrong project returned")
    }
}

func TestProjectNotFound(t *testing.T) {
    s := newTestStore(t)
    _, err := s.ProjectStore().Get(t.Context(), "nonexistent")
    if err == nil {
        t.Fatal("expected error")
    }
}

func TestProjectList(t *testing.T) {
    s := newTestStore(t)
    for i := 0; i < 3; i++ {
        s.ProjectStore().Create(t.Context(), &domain.Project{
            ProjectID: uuid.NewString(),
            Name:      "proj-" + uuid.NewString()[:8],
            CreatedAt: time.Now().UTC(),
            UpdatedAt: time.Now().UTC(),
        })
    }
    list, err := s.ProjectStore().List(t.Context())
    if err != nil {
        t.Fatal(err)
    }
    if len(list) != 3 {
        t.Errorf("got %d projects, want 3", len(list))
    }
}

func TestProjectDuplicateName(t *testing.T) {
    s := newTestStore(t)
    p1 := &domain.Project{ProjectID: uuid.NewString(), Name: "dup", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
    p2 := &domain.Project{ProjectID: uuid.NewString(), Name: "dup", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
    s.ProjectStore().Create(t.Context(), p1)
    err := s.ProjectStore().Create(t.Context(), p2)
    if err == nil {
        t.Fatal("expected conflict error on duplicate name")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/sqlite/ -run TestProject -v
```

- [ ] **Step 3: Implement ProjectStore**

Implement `internal/store/sqlite/project.go` with standard CRUD using `database/sql`. `Store` exposes `ProjectStore()` returning a `projectStore` that holds the `*sql.DB`. Map domain errors: SQL no-rows → `domain.ErrNotFound`, UNIQUE constraint → `domain.ErrConflict`. Serialize `Metadata` as JSON TEXT. Timestamps as RFC3339.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/sqlite/ -run TestProject -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/project.go internal/store/sqlite/project_test.go
git commit -m "feat: add ProjectStore SQLite implementation"
```

---

### Task 10: SandboxStore implementation

**Files:**
- Create: `internal/store/sqlite/sandbox.go`
- Create: `internal/store/sqlite/sandbox_test.go`

- [ ] **Step 1: Write tests**

Key test cases (all with full test code):

```go
func TestSandboxCreateAndGet(t *testing.T) {
    s := newTestStore(t)
    proj := createTestProject(t, s) // helper that creates a project
    now := time.Now().UTC()
    cpu := 2
    mem := 1024
    sbx := &domain.Sandbox{
        SandboxID:     uuid.NewString(),
        ProjectID:     proj.ProjectID,
        Name:          "test-sbx",
        State:         domain.SandboxPending,
        Backend:       "incus",
        NetworkMode:   domain.NetworkIsolated,
        CPULimit:      &cpu,
        MemoryLimitMB: &mem,
        CreatedAt:     now,
        UpdatedAt:     now,
    }
    if err := s.SandboxStore().Create(t.Context(), sbx); err != nil {
        t.Fatal(err)
    }
    got, err := s.SandboxStore().Get(t.Context(), sbx.SandboxID)
    if err != nil {
        t.Fatal(err)
    }
    if got.Name != "test-sbx" { t.Error("wrong name") }
    if *got.CPULimit != 2 { t.Error("wrong cpu") }
    if *got.MemoryLimitMB != 1024 { t.Error("wrong memory") }
}

func TestSandboxListByProject(t *testing.T) {
    s := newTestStore(t)
    p1 := createTestProject(t, s)
    p2 := createTestProject(t, s)
    createTestSandbox(t, s, p1.ProjectID, "s1")
    createTestSandbox(t, s, p1.ProjectID, "s2")
    createTestSandbox(t, s, p2.ProjectID, "s3")
    list, _ := s.SandboxStore().List(t.Context(), domain.SandboxFilter{ProjectID: &p1.ProjectID})
    if len(list) != 2 {
        t.Errorf("expected 2 sandboxes for project 1, got %d", len(list))
    }
}

func TestSandboxListExpired(t *testing.T) {
    s := newTestStore(t)
    proj := createTestProject(t, s)
    past := time.Now().UTC().Add(-time.Hour)
    future := time.Now().UTC().Add(time.Hour)
    sbx1 := createTestSandboxWithExpiry(t, s, proj.ProjectID, "expired", &past)
    createTestSandboxWithExpiry(t, s, proj.ProjectID, "notyet", &future)
    expired, _ := s.SandboxStore().ListExpired(t.Context(), time.Now().UTC())
    if len(expired) != 1 || expired[0].SandboxID != sbx1.SandboxID {
        t.Errorf("expected 1 expired sandbox, got %d", len(expired))
    }
}
```

Also test: Update state, Delete, unique constraint on (project_id, name), Get nonexistent returns ErrNotFound.

- [ ] **Step 2: Run to verify fail**
- [ ] **Step 3: Implement SandboxStore**

SQL pattern for nullable fields:
```go
// For nullable time fields, use sql.NullTime
// For nullable int fields, use sql.NullInt64
// In Create/Update: convert *int → sql.NullInt64{Int64: int64(*v), Valid: v != nil}
// In scan: convert sql.NullInt64 → *int: if valid, ptr to int; else nil
```

- [ ] **Step 4: Run to verify pass**
- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/sandbox.go internal/store/sqlite/sandbox_test.go
git commit -m "feat: add SandboxStore SQLite implementation"
```

---

### Task 11: SnapshotStore implementation

**Files:**
- Create: `internal/store/sqlite/snapshot.go`
- Create: `internal/store/sqlite/snapshot_test.go`

Test cases: Create, Get, ListBySandbox, Update, Delete, ListOrphaned, ErrNotFound.

`ListOrphaned` SQL query (non-trivial — requires JOIN):

```sql
SELECT s.* FROM snapshots s
JOIN sandboxes sb ON s.sandbox_id = sb.sandbox_id
WHERE sb.state = 'destroyed'
AND s.snapshot_id NOT IN (
    SELECT source_snapshot_id FROM base_images WHERE source_snapshot_id IS NOT NULL
)
AND s.state != 'deleted'
```

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add SnapshotStore SQLite implementation"
```

---

### Task 12: SessionStore implementation

**Files:**
- Create: `internal/store/sqlite/session.go`
- Create: `internal/store/sqlite/session_test.go`

Test cases: Create, Get, ListBySandbox, Update (state changes, last_attached_at), Delete, ErrNotFound.

- [ ] **Step 1: Write tests**
- [ ] **Step 2: Run to verify fail**
- [ ] **Step 3: Implement SessionStore**
- [ ] **Step 4: Run to verify pass**
- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/session.go internal/store/sqlite/session_test.go
git commit -m "feat: add SessionStore SQLite implementation"
```

---

### Task 13: ImageStore implementation

**Files:**
- Create: `internal/store/sqlite/image.go`
- Create: `internal/store/sqlite/image_test.go`

Test cases: Create, Get, List with filters (name, architecture, state), Update, Delete, unique constraint on (name, version), ErrNotFound.

- [ ] **Step 1: Write tests**
- [ ] **Step 2: Run to verify fail**
- [ ] **Step 3: Implement ImageStore**
- [ ] **Step 4: Run to verify pass**
- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite/image.go internal/store/sqlite/image_test.go
git commit -m "feat: add ImageStore SQLite implementation"
```

---

### Task 14: OperationStore implementation

**Files:**
- Create: `internal/store/sqlite/operation.go`
- Create: `internal/store/sqlite/operation_test.go`

Test cases: Create, Get, List with filters, Update, ListStale (operations in terminal state older than threshold), ListByState (used by dispatcher startup recovery).

```go
func TestOperationListStale(t *testing.T) {
    s := newTestStore(t)
    old := time.Now().UTC().Add(-48 * time.Hour)
    recent := time.Now().UTC().Add(-1 * time.Hour)
    createTestOp(t, s, domain.OpSucceeded, old)    // stale
    createTestOp(t, s, domain.OpFailed, old)        // stale
    createTestOp(t, s, domain.OpRunning, old)       // not terminal, not stale
    createTestOp(t, s, domain.OpSucceeded, recent)  // terminal but recent
    stale, _ := s.OperationStore().ListStale(t.Context(), time.Now().UTC().Add(-24*time.Hour))
    if len(stale) != 2 {
        t.Errorf("expected 2 stale ops, got %d", len(stale))
    }
}
```

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add OperationStore SQLite implementation"
```

---

### Task 15: PortBindingStore implementation

**Files:**
- Create: `internal/store/sqlite/port.go`
- Create: `internal/store/sqlite/port_test.go`

Test cases: Create, ListBySandbox, Delete, GetByPublishedPort, NextAvailablePort.

```go
func TestPortNextAvailable(t *testing.T) {
    s := newTestStore(t)
    proj := createTestProject(t, s)
    sbx := createTestSandbox(t, s, proj.ProjectID, "s1")

    // First allocation returns range start
    port1, _ := s.PortBindingStore().NextAvailablePort(t.Context(), 40000, 40010)
    if port1 != 40000 { t.Errorf("expected 40000, got %d", port1) }

    s.PortBindingStore().Create(t.Context(), &domain.PortBinding{
        SandboxID: sbx.SandboxID, TargetPort: 80, PublishedPort: port1, HostAddress: "0.0.0.0",
        CreatedAt: time.Now().UTC(),
    })

    // Second allocation returns next
    port2, _ := s.PortBindingStore().NextAvailablePort(t.Context(), 40000, 40010)
    if port2 != 40001 { t.Errorf("expected 40001, got %d", port2) }
}
```

`NextAvailablePort` SQL: find the lowest integer in [rangeStart, rangeEnd] not in the published_port column.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add PortBindingStore SQLite implementation"
```

---

### Task 16: Compile-time Store interface check

**Files:**
- Modify: `internal/store/sqlite/sqlite.go`

- [ ] **Step 1: Add compile-time check and verify all sub-stores compile**

Now that all sub-stores are implemented, add `var _ store.Store = (*Store)(nil)` and ensure all accessor methods return the real sub-store types.

- [ ] **Step 2: Run all store tests**

```bash
go test ./internal/store/... -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/store/
git commit -m "feat: add Store aggregate interface with compile-time check"
```

---

## Phase 3: EventBus and Worker Dispatcher

### Task 17: In-memory EventBus

**Files:**
- Create: `internal/eventbus/memory.go`
- Create: `internal/eventbus/memory_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/eventbus/memory_test.go
package eventbus_test

import (
    "testing"
    "time"

    "github.com/navaris/navaris/internal/domain"
    "github.com/navaris/navaris/internal/eventbus"
)

func TestPublishSubscribe(t *testing.T) {
    bus := eventbus.New(64) // buffer size
    ch, cancel, err := bus.Subscribe(t.Context(), domain.EventFilter{})
    if err != nil {
        t.Fatal(err)
    }
    defer cancel()

    evt := domain.Event{
        Type:      domain.EventSandboxStateChanged,
        Timestamp: time.Now().UTC(),
        Data:      map[string]any{"sandbox_id": "s1"},
    }
    bus.Publish(t.Context(), evt)

    select {
    case got := <-ch:
        if got.Type != domain.EventSandboxStateChanged {
            t.Errorf("got type %s, want %s", got.Type, domain.EventSandboxStateChanged)
        }
    case <-time.After(time.Second):
        t.Fatal("timeout waiting for event")
    }
}

func TestSubscribeWithFilter(t *testing.T) {
    bus := eventbus.New(64)
    sid := "sandbox-123"
    ch, cancel, err := bus.Subscribe(t.Context(), domain.EventFilter{SandboxID: &sid})
    if err != nil {
        t.Fatal(err)
    }
    defer cancel()

    // Event for different sandbox — should not be received
    bus.Publish(t.Context(), domain.Event{
        Type: domain.EventSandboxStateChanged,
        Data: map[string]any{"sandbox_id": "other"},
    })
    // Event for matching sandbox — should be received
    bus.Publish(t.Context(), domain.Event{
        Type: domain.EventSandboxStateChanged,
        Data: map[string]any{"sandbox_id": sid},
    })

    select {
    case got := <-ch:
        if got.Data["sandbox_id"] != sid {
            t.Errorf("got wrong sandbox_id: %v", got.Data["sandbox_id"])
        }
    case <-time.After(time.Second):
        t.Fatal("timeout")
    }
}

func TestCancelUnsubscribes(t *testing.T) {
    bus := eventbus.New(64)
    ch, cancel, _ := bus.Subscribe(t.Context(), domain.EventFilter{})
    cancel()

    bus.Publish(t.Context(), domain.Event{Type: domain.EventSandboxStateChanged})
    // Channel should be closed
    select {
    case _, ok := <-ch:
        if ok {
            t.Error("expected channel to be closed after cancel")
        }
    case <-time.After(100 * time.Millisecond):
        // Also acceptable — no event delivered
    }
}
```

- [ ] **Step 2: Run to verify fail**
- [ ] **Step 3: Implement in-memory EventBus**

Fan-out pub/sub. Each subscriber gets a buffered channel. Publish iterates subscribers, sends non-blocking (drop if full). Filter matching in Publish. Cancel removes subscriber and closes channel.

- [ ] **Step 4: Run to verify pass**
- [ ] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat: add in-memory EventBus implementation"
```

---

### Task 18: Worker Dispatcher

**Files:**
- Create: `internal/worker/dispatcher.go`
- Create: `internal/worker/dispatcher_test.go`

- [ ] **Step 1: Write tests**

```go
// internal/worker/dispatcher_test.go
package worker_test

import (
    "context"
    "fmt"
    "sync/atomic"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/navaris/navaris/internal/domain"
    "github.com/navaris/navaris/internal/eventbus"
    "github.com/navaris/navaris/internal/worker"
)

// mockOpStore is a minimal in-memory OperationStore for testing
type mockOpStore struct {
    ops map[string]*domain.Operation
}
// ... implement Get, Update for the mock

func TestDispatcherRunsHandler(t *testing.T) {
    store := &mockOpStore{ops: make(map[string]*domain.Operation)}
    bus := eventbus.New(64)
    d := worker.NewDispatcher(store, bus, 4) // concurrency=4

    var called atomic.Bool
    d.Register("test_op", func(ctx context.Context, op *domain.Operation) error {
        called.Store(true)
        return nil
    })
    d.Start()
    defer d.Stop()

    op := &domain.Operation{
        OperationID: uuid.NewString(),
        Type:        "test_op",
        State:       domain.OpPending,
        StartedAt:   time.Now().UTC(),
    }
    store.ops[op.OperationID] = op
    d.Enqueue(op)

    // Wait for handler to run
    time.Sleep(100 * time.Millisecond)
    if !called.Load() {
        t.Error("handler was not called")
    }
    if store.ops[op.OperationID].State != domain.OpSucceeded {
        t.Errorf("expected state Succeeded, got %s", store.ops[op.OperationID].State)
    }
}

func TestDispatcherHandlerError(t *testing.T) {
    store := &mockOpStore{ops: make(map[string]*domain.Operation)}
    bus := eventbus.New(64)
    d := worker.NewDispatcher(store, bus, 4)

    d.Register("fail_op", func(ctx context.Context, op *domain.Operation) error {
        return fmt.Errorf("something broke")
    })
    d.Start()
    defer d.Stop()

    op := &domain.Operation{
        OperationID: uuid.NewString(),
        Type:        "fail_op",
        State:       domain.OpPending,
        StartedAt:   time.Now().UTC(),
    }
    store.ops[op.OperationID] = op
    d.Enqueue(op)

    time.Sleep(100 * time.Millisecond)
    if store.ops[op.OperationID].State != domain.OpFailed {
        t.Errorf("expected state Failed, got %s", store.ops[op.OperationID].State)
    }
    if store.ops[op.OperationID].ErrorText == "" {
        t.Error("expected error_text to be set")
    }
}

func TestDispatcherConcurrencyLimit(t *testing.T) {
    ctx := t.Context()
    bus := eventbus.New()
    d := worker.NewDispatcher(store, bus, 2) // limit = 2

    var running atomic.Int32
    var maxSeen atomic.Int32
    gate := make(chan struct{})

    d.Register("slow", func(ctx context.Context, op domain.Operation) error {
        cur := running.Add(1)
        for {
            old := maxSeen.Load()
            if cur <= old || maxSeen.CompareAndSwap(old, cur) {
                break
            }
        }
        <-gate
        running.Add(-1)
        return nil
    })

    // Enqueue 4 ops but only 2 should run concurrently
    for i := 0; i < 4; i++ {
        op := domain.Operation{OperationID: fmt.Sprintf("op-%d", i), Type: "slow", State: domain.OperationPending}
        store.CreateOperation(ctx, op)
        d.Enqueue(ctx, op)
    }

    // Let all finish
    time.Sleep(50 * time.Millisecond) // let goroutines start
    close(gate)
    d.WaitIdle()

    if maxSeen.Load() > 2 {
        t.Fatalf("expected max 2 concurrent, saw %d", maxSeen.Load())
    }
}
```

- [ ] **Step 2: Run to verify fail**
- [ ] **Step 3: Implement Dispatcher**

```go
// internal/worker/dispatcher.go
package worker

// Dispatcher: channel-based queue, semaphore for concurrency,
// handler map, Start/Stop/Enqueue.
// On enqueue: goroutine reads from channel, acquires semaphore,
// sets op Running, calls handler, sets op Succeeded/Failed,
// publishes event.
```

Key implementation details:
- `handlers map[string]OperationHandler`
- `queue chan *domain.Operation`
- `sem chan struct{}` (buffered to max concurrency)
- `Start()` launches goroutine that reads from queue
- `Stop()` closes queue channel, waits for in-flight handlers (with 30s timeout)
- `Enqueue()` sends to queue (non-blocking with buffer)
- `Cancel(opID)` stores a cancel func per operation, calls it
- `WaitIdle()` blocks until all in-flight handlers finish (used by tests)

- [ ] **Step 4: Run to verify pass**
- [ ] **Step 5: Commit**

```bash
git add internal/worker/
git commit -m "feat: add operation Dispatcher with concurrency control"
```

---

### Task 19: GC Sweeper

**Files:**
- Create: `internal/worker/gc.go`
- Create: `internal/worker/gc_test.go`

- [ ] **Step 1: Write tests**

Test with real SQLite store (in-memory) and mock provider (for destroy calls):
- Insert expired sandbox, run sweep, verify it's marked destroyed
- Insert orphaned snapshot (parent sandbox destroyed), run sweep, verify deleted
- Insert stale terminal operation, run sweep, verify deleted

- [ ] **Step 2-5: TDD cycle + commit**

```bash
git commit -m "feat: add GC sweeper"
```

---

## Phase 4: Mock Provider

### Task 20: Mock Provider for service/API testing

**Files:**
- Create: `internal/provider/mock.go`

- [ ] **Step 1: Implement mock provider**

A configurable mock that implements `domain.Provider`. Each method has a corresponding function field that tests can set. Default behavior: return success with generated `BackendRef`. This is used by service and API tests so they don't need a real Incus installation.

```go
// internal/provider/mock.go
package provider

import (
    "context"
    "github.com/google/uuid"
    "github.com/navaris/navaris/internal/domain"
)

type MockProvider struct {
    CreateSandboxFn           func(ctx context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error)
    StartSandboxFn            func(ctx context.Context, ref domain.BackendRef) error
    // ... one func field per Provider method
}

func NewMock() *MockProvider {
    return &MockProvider{
        CreateSandboxFn: func(_ context.Context, _ domain.CreateSandboxRequest) (domain.BackendRef, error) {
            return domain.BackendRef{Backend: "mock", Ref: "mock-" + uuid.NewString()[:8]}, nil
        },
        // ... defaults for all methods
    }
}

// Compile-time check
var _ domain.Provider = (*MockProvider)(nil)

func (m *MockProvider) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
    return m.CreateSandboxFn(ctx, req)
}
// ... delegate all methods to function fields
```

- [ ] **Step 2: Commit**

```bash
git add internal/provider/mock.go
git commit -m "feat: add mock Provider for testing"
```

---

## Phase 5: Service Layer

### Task 21: ProjectService

**Files:**
- Create: `internal/service/project.go`
- Create: `internal/service/project_test.go`

- [ ] **Step 1: Write tests**

Test with real SQLite store (in-memory). ProjectService is simple — it wraps the store with validation.

```go
func TestProjectServiceCreate(t *testing.T) {
    svc := newProjectService(t)  // helper that opens in-memory DB, creates service
    p, err := svc.Create(t.Context(), "my-project", nil)
    if err != nil { t.Fatal(err) }
    if p.Name != "my-project" { t.Errorf("wrong name") }
    if p.ProjectID == "" { t.Error("expected ID to be set") }
}

func TestProjectServiceDuplicateName(t *testing.T) {
    svc := newProjectService(t)
    svc.Create(t.Context(), "dup", nil)
    _, err := svc.Create(t.Context(), "dup", nil)
    if !errors.Is(err, domain.ErrConflict) {
        t.Errorf("expected ErrConflict, got %v", err)
    }
}
```

- [ ] **Steps 2-5: TDD cycle + commit**

```bash
git commit -m "feat: add ProjectService"
```

---

### Task 22: SandboxService

**Files:**
- Create: `internal/service/sandbox.go`
- Create: `internal/service/sandbox_test.go`

The core service. Tests use real SQLite + mock provider + real eventbus + real dispatcher.

```go
// internal/service/sandbox.go
type SandboxService struct {
    sandboxes domain.SandboxStore
    ops       domain.OperationStore
    ports     domain.PortBindingStore
    provider  domain.Provider
    events    domain.EventBus
    workers   *worker.Dispatcher
}

func (s *SandboxService) Create(ctx context.Context, projectID, name, imageID string, opts CreateSandboxOpts) (*domain.Operation, error) {
    // 1. Validate project exists (call project store or accept projectID as trusted)
    // 2. Build Sandbox record: ID=uuid, State=Pending, Backend="incus"
    // 3. Store sandbox
    // 4. Build Operation record: Type="create_sandbox", State=Pending, ResourceType="sandbox", ResourceID=sandbox.ID
    // 5. Store operation
    // 6. Enqueue to dispatcher
    // 7. Return operation
}

func (s *SandboxService) Destroy(ctx context.Context, id string) (*domain.Operation, error) {
    // 1. Get sandbox, verify not already destroyed
    // 2. Cancel all pending/running operations for this sandbox
    // 3. Create destroy operation
    // 4. Enqueue — handler destroys sessions, port bindings, then calls provider.DestroySandbox
    // 5. Return operation
}
```

Key test cases:
- **Create:** returns Operation in Pending state, dispatcher eventually moves sandbox to Running
- **Start:** sandbox must be Stopped, returns Operation
- **Stop:** sandbox must be Running, returns Operation
- **Destroy:** cancels in-flight operations first, then destroys
- **Create from snapshot:** sets parent_snapshot_id on new sandbox
- **State validation:** cannot start a Pending sandbox (must wait for create to finish)

```go
func TestSandboxServiceCreate(t *testing.T) {
    env := newServiceEnv(t) // helper: in-memory store, mock provider, eventbus, dispatcher
    op, err := env.sandbox.Create(t.Context(), env.projectID, "my-sandbox", "image-1", CreateSandboxOpts{})
    if err != nil { t.Fatal(err) }
    if op.State != domain.OpPending { t.Error("expected pending") }

    // Wait for dispatcher to process
    env.dispatcher.WaitIdle()

    sbx, _ := env.sandbox.Get(t.Context(), op.ResourceID)
    if sbx.State != domain.SandboxRunning { t.Errorf("expected running, got %s", sbx.State) }
}
```

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add SandboxService with create, start, stop, destroy"
```

---

### Task 23: SnapshotService

**Files:**
- Create: `internal/service/snapshot.go`
- Create: `internal/service/snapshot_test.go`

Key test cases:
- Create snapshot (default: sandbox must be stopped)
- Create snapshot with live mode (sandbox may be running)
- Restore snapshot
- Delete snapshot
- Cannot snapshot a destroyed sandbox

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add SnapshotService"
```

---

### Task 24: ImageService

**Files:**
- Create: `internal/service/image.go`
- Create: `internal/service/image_test.go`

Key test cases:
- Promote snapshot to base image
- Register external image
- List and get images
- Delete image
- Cannot promote a failed snapshot

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add ImageService"
```

---

### Task 25: SessionService

**Files:**
- Create: `internal/service/session.go`
- Create: `internal/service/session_test.go`

Key test cases:
- Create session on running sandbox → Session returned (synchronous, no Operation)
- Create session on stopped sandbox → ErrInvalidState
- Auto-detect backing: mock provider's Exec for `which tmux` returns success → tmux; returns error → direct
- Destroy session
- List sessions by sandbox

Note: Session attach, scrollback, and input are handled at the API layer (WebSocket), not in the service. The service manages session records and delegates shell startup to the provider.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add SessionService"
```

---

### Task 26: OperationService

**Files:**
- Create: `internal/service/operation.go`
- Create: `internal/service/operation_test.go`

Simple service: Get, List, Cancel. Cancel calls dispatcher.Cancel(opID).

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add OperationService"
```

---

## Phase 6: API Layer

### Task 27: API server skeleton, middleware, response helpers

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/middleware.go`
- Create: `internal/api/response.go`

- [ ] **Step 1: Write test for auth middleware**

```go
func TestAuthMiddlewareRejectsNoToken(t *testing.T) {
    handler := middleware.Auth("secret-token")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(200)
    }))
    req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != 401 { t.Errorf("expected 401, got %d", rec.Code) }
}

func TestAuthMiddlewareAcceptsValidToken(t *testing.T) {
    handler := middleware.Auth("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(200)
    }))
    req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
    req.Header.Set("Authorization", "Bearer secret")
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != 200 { t.Errorf("expected 200, got %d", rec.Code) }
}

func TestAuthMiddlewareAcceptsQueryToken(t *testing.T) {
    // For WebSocket: ?token=secret
    handler := middleware.Auth("secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(200)
    }))
    req := httptest.NewRequest("GET", "/v1/events?token=secret", nil)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != 200 { t.Errorf("expected 200, got %d", rec.Code) }
}
```

- [ ] **Step 2: Implement middleware**

Auth middleware: check `Authorization: Bearer <token>` header, fall back to `?token=` query param.
Request ID middleware: generate UUID, set on context and `X-Request-ID` header.
Logging middleware: log request method, path, status, duration via slog.

- [ ] **Step 3: Implement response helpers**

```go
// internal/api/response.go
func respondData(w http.ResponseWriter, status int, data any) { ... }
func respondList(w http.ResponseWriter, status int, data any) { ... } // includes "pagination": null
func respondOperation(w http.ResponseWriter, op *domain.Operation) { ... }
func respondError(w http.ResponseWriter, err error) { ... }
```

`respondList` wraps list responses with `{"data": [...], "pagination": null}` (pagination reserved for v2).

`respondError` maps domain errors to HTTP status + error code JSON.

- [ ] **Step 4: Implement Server with routing**

```go
// internal/api/server.go
type Server struct { ... }

func NewServer(cfg ServerConfig) *Server { ... }
func (s *Server) Handler() http.Handler { ... }

// Handler() returns an http.Handler with all routes registered.
// Uses Go 1.22 enhanced ServeMux: mux.HandleFunc("POST /v1/sandboxes", s.createSandbox)
```

- [ ] **Step 5: Run tests, commit**

```bash
git add internal/api/
git commit -m "feat: add API server skeleton with auth middleware and response helpers"
```

---

### Task 28: Project API handlers

**Files:**
- Create: `internal/api/project.go`
- Create: `internal/api/project_test.go`

- [ ] **Step 1: Write handler tests**

Use `httptest` with real services backed by in-memory SQLite. Test: create project, list projects, get project, update project, delete project, error responses (not found, conflict).

- [ ] **Steps 2-5: TDD cycle + commit**

```bash
git commit -m "feat: add Project API handlers"
```

---

### Task 29: Sandbox API handlers

**Files:**
- Create: `internal/api/sandbox.go`
- Create: `internal/api/sandbox_test.go`

Test: create sandbox returns operation JSON, get sandbox, list with project_id filter (400 without it), start/stop/destroy return operations.

Must implement all three creation routes:
- `POST /v1/sandboxes` — CreateSandbox (generic, takes image_id or snapshot_id in body)
- `POST /v1/sandboxes/from-snapshot` — CreateSandboxFromSnapshot (convenience, snapshot_id in body)
- `POST /v1/sandboxes/from-image` — CreateSandboxFromImage (convenience, calls CreateSandbox with image_id)

Also implement the non-persistent attach WebSocket:
- `GET /v1/sandboxes/:id/attach` — direct PTY attach, no session object. WebSocket upgrade, bridges to provider.AttachSession.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add Sandbox API handlers"
```

---

### Task 30: Snapshot and Image API handlers

**Files:**
- Create: `internal/api/snapshot.go`, `snapshot_test.go`
- Create: `internal/api/image.go`, `image_test.go`

Snapshot handlers: CreateSnapshot, ListSnapshots, GetSnapshot, RestoreSnapshot (`POST /v1/sandboxes/:id/snapshots/:sid/restore`), DeleteSnapshot.

Image handlers: CreateBaseImageFromSnapshot (`POST /v1/images`), RegisterBaseImage (`POST /v1/images/register`), ListBaseImages, GetBaseImage, DeleteBaseImage.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add Snapshot and Image API handlers"
```

---

### Task 31: Session API handlers (including WebSocket attach)

**Files:**
- Create: `internal/api/session.go`
- Create: `internal/api/session_test.go`

Session handlers: CreateSession (synchronous), ListSessions, GetSession, DestroySession, GetScrollback, SendInput.

WebSocket handler for `GET /v1/sessions/:id/attach`:
- Upgrade to WebSocket using `nhooyr.io/websocket`
- Get session from service, verify active
- For direct-backed: replay scrollback ring buffer, then bridge bidirectional PTY stream
- For tmux-backed: call provider.AttachSession with tmux attach command, bridge WebSocket
- Binary frames for PTY data, text frames for control messages (resize)
- Update session last_attached_at

Test with `nhooyr.io/websocket` client.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add Session API handlers with WebSocket attach"
```

---

### Task 32: Operation, Port, and Health API handlers

**Files:**
- Create: `internal/api/operation.go`, `operation_test.go`
- Create: `internal/api/port.go`, `port_test.go`
- Create: `internal/api/health.go`

Operation handlers: GetOperation, ListOperations, CancelOperation.

Port handlers: PublishPort, UnpublishPort, ListPortBindings.

Health handler: calls provider.Health(), returns structured JSON with provider status, store status, worker queue depth.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add Operation, Port, and Health API handlers"
```

---

### Task 33: Exec handler and operation streaming WebSocket

**Files:**
- Create: `internal/api/exec.go`
- Create: `internal/api/exec_test.go`

The exec handler:
1. POST `/v1/sandboxes/:id/exec` → creates Exec operation, returns Operation JSON
2. GET `/v1/operations/:id/stream` (WebSocket) → streams exec output events

Test: create exec operation, connect to stream WebSocket, verify stdout/stderr/exit events arrive.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add Exec handler with operation streaming WebSocket"
```

---

### Task 34: Event stream WebSocket

**Files:**
- Create: `internal/api/events.go`
- Create: `internal/api/events_test.go`

WebSocket handler for `/v1/events`. Subscribes to EventBus with filter from query params. Forwards events as JSON messages.

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add event stream WebSocket handler"
```

---

## Phase 7: Daemon Wiring

### Task 35: navarisd main.go

**Files:**
- Create: `cmd/navarisd/main.go`
- Create: `cmd/navarisd/main_test.go`

- [ ] **Step 1: Implement daemon entry point**

```go
// cmd/navarisd/main.go
package main

// Reads config (flags: --listen, --db-path, --log-level, --auth-token, --incus-socket)
// Opens SQLite store
// Creates EventBus
// Creates Dispatcher, registers all operation handlers
// Creates mock provider (Incus provider added in Phase 9)
// Creates all services
// Creates API server
// Starts dispatcher
// Starts GC sweeper
// Starts HTTP server
// Handles SIGTERM/SIGINT for graceful shutdown
```

- [ ] **Step 2: Write smoke test**

```go
// cmd/navarisd/main_test.go
func TestDaemonStartsAndServesHealth(t *testing.T) {
    // Start server on random port with in-memory SQLite and mock provider
    // Hit GET /v1/health
    // Verify 200 response with "healthy" status
    // Shut down cleanly
}
```

- [ ] **Step 3: Build and verify it compiles**

```bash
go build -o bin/navarisd ./cmd/navarisd/
```

- [ ] **Step 4: Commit**

```bash
git add cmd/navarisd/
git commit -m "feat: add navarisd daemon with DI wiring"
```

---

## Phase 8: Go SDK

### Task 36: SDK client and types

**Files:**
- Create: `pkg/client/client.go`
- Create: `pkg/client/types.go`

- [ ] **Step 1: Implement client with functional options**

```go
// pkg/client/client.go
package client

import (
    "net/http"
    "net/url"
)

type Client struct {
    baseURL    *url.URL
    token      string
    httpClient *http.Client
}

type Option func(*Client)

func WithURL(u string) Option       { ... }
func WithToken(t string) Option     { ... }
func WithHTTPClient(c *http.Client) Option { ... }

func NewClient(opts ...Option) (*Client, error) {
    // Apply options, fall back to env vars (NAVARIS_API_URL, NAVARIS_TOKEN)
}

// Internal helpers:
func (c *Client) get(ctx, path string, result any) error { ... }
func (c *Client) post(ctx, path string, body, result any) error { ... }
func (c *Client) put(ctx context.Context, path string, body, result any) error { ... }
func (c *Client) delete(ctx context.Context, path string, result any) error { ... }
```

- [ ] **Step 2: Define public types** in `types.go`

Mirror domain types but standalone — no import from internal. Each type has JSON tags matching the API response format.

- [ ] **Step 3: Commit**

```bash
git add pkg/client/client.go pkg/client/types.go
git commit -m "feat: add SDK client foundation and types"
```

---

### Task 37: SDK CRUD resource methods

**Files:**
- Create: `pkg/client/project.go`
- Create: `pkg/client/sandbox.go`
- Create: `pkg/client/snapshot.go`
- Create: `pkg/client/image.go`
- Create: `pkg/client/port.go`

Each file implements REST calls using the internal `get`/`post`/`delete` helpers from Task 36. Mutating sandbox/snapshot/image calls return `*Operation`. Read calls return the typed resource.

- [ ] **Step 1: Implement each resource file**
- [ ] **Step 2: Commit**

```bash
git add pkg/client/
git commit -m "feat: add SDK CRUD resource methods"
```

---

### Task 38: SDK operation waiting and streaming

**Files:**
- Create: `pkg/client/operation.go`

Key implementations:

```go
func (c *Client) WaitForOperation(ctx context.Context, opID string, opts WaitOptions) (*Operation, error) {
    deadline := time.Now().Add(opts.Timeout)
    interval := 500 * time.Millisecond
    for {
        op, err := c.GetOperation(ctx, opID)
        if err != nil {
            return nil, err
        }
        if op.State.Terminal() {
            return op, nil
        }
        if time.Now().After(deadline) {
            return nil, fmt.Errorf("timeout waiting for operation %s", opID)
        }
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(interval):
        }
        // Exponential backoff up to 5s
        if interval < 5*time.Second {
            interval = interval * 3 / 2
        }
    }
}

func (c *Client) StreamOperation(ctx context.Context, opID string, fn func(OperationEvent) error) error {
    // 1. Dial WebSocket at /v1/operations/{opID}/stream?token=...
    // 2. Read JSON messages in a loop
    // 3. Decode each as OperationEvent, call fn
    // 4. Return when connection closes or fn returns error
}
```

Also implement `CreateSandboxAndWait`, `StartSandboxAndWait`, etc. as thin wrappers:

```go
func (c *Client) CreateSandboxAndWait(ctx context.Context, req CreateSandboxRequest, opts WaitOptions) (*Sandbox, error) {
    op, err := c.CreateSandbox(ctx, req)
    if err != nil {
        return nil, err
    }
    result, err := c.WaitForOperation(ctx, op.OperationID, opts)
    if err != nil {
        return nil, err
    }
    if result.State == "failed" {
        return nil, fmt.Errorf("operation failed: %s", result.ErrorText)
    }
    return c.GetSandbox(ctx, result.ResourceID)
}
```

- [ ] **Step 1: Implement operation methods**
- [ ] **Step 2: Commit**

```bash
git commit -m "feat: add SDK operation waiting and streaming"
```

---

### Task 39: SDK session and exec methods

**Files:**
- Create: `pkg/client/session.go`
- Create: `pkg/client/exec.go`

Session methods: CreateSession, ListSessions, GetSession, DestroySession, GetScrollback, SendInput.

`AttachToSession` returns a `SessionConn` wrapping a WebSocket:

```go
func (c *Client) AttachToSession(ctx context.Context, sessionID string) (*SessionConn, error) {
    wsURL := c.wsURL("/v1/sessions/" + sessionID + "/attach")
    conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
        HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}},
    })
    if err != nil {
        return nil, err
    }
    return &SessionConn{conn: conn}, nil
}

type SessionConn struct {
    conn *websocket.Conn
}

func (s *SessionConn) Read(p []byte) (int, error) {
    _, msg, err := s.conn.Read(context.Background())
    if err != nil {
        return 0, err
    }
    return copy(p, msg), nil
}

func (s *SessionConn) Write(p []byte) (int, error) {
    err := s.conn.Write(context.Background(), websocket.MessageBinary, p)
    return len(p), err
}

func (s *SessionConn) Resize(width, height int) error {
    msg, _ := json.Marshal(map[string]any{"type": "resize", "width": width, "height": height})
    return s.conn.Write(context.Background(), websocket.MessageText, msg)
}

func (s *SessionConn) Close() error { return s.conn.Close(websocket.StatusNormalClosure, "") }
```

Exec methods: `Exec` (POST, returns Operation), streaming via `StreamOperation`.

- [ ] **Step 1: Implement session and exec methods**
- [ ] **Step 2: Commit**

```bash
git commit -m "feat: add SDK session and exec methods"
```

---

### Task 40: SDK integration test

**Files:**
- Create: `pkg/client/client_test.go`

Start a real `api.Server` (in-memory store, mock provider), test SDK methods end-to-end: create project, create sandbox, wait for it, get it, destroy it.

- [ ] **Step 1: Write integration test**
- [ ] **Step 2: Run and verify**
- [ ] **Step 3: Commit**

```bash
git commit -m "feat: add SDK integration tests"
```

---

## Phase 9: CLI

### Task 41: CLI skeleton and config

**Files:**
- Create: `cmd/navaris/main.go`
- Create: `cmd/navaris/config.go`
- Create: `cmd/navaris/output.go`

- [ ] **Step 1: Implement root command with cobra**

Root command with persistent flags: `--api-url`, `--token`, `--project`, `--output` (json|text).
Config loading: flags > env vars (`NAVARIS_API_URL`, `NAVARIS_TOKEN`, `NAVARIS_PROJECT`) > config file (`~/.config/navaris/config.json`).

- [ ] **Step 2: Implement output mode detection**

`output.go`: if `--output json` or non-TTY → JSON output. If TTY and no `--output` → human-readable table. Helper functions: `printJSON(data any)`, `printTable(headers []string, rows [][]string)`.

- [ ] **Step 3: Implement `--wait` and `--timeout` helpers**

Shared helper used by all mutating commands: if `--wait` set, call `client.WaitForOperation`, then fetch and print the final resource. If `--timeout` set, pass to WaitOptions. Map errors to exit codes (6 for failed, 7 for timeout).

- [ ] **Step 4: Build and verify**

```bash
go build -o bin/navaris ./cmd/navaris/
./bin/navaris --help
```

- [ ] **Step 5: Commit**

```bash
git add cmd/navaris/
git commit -m "feat: add CLI skeleton with config, output modes, and wait helpers"
```

---

### Task 42: CLI project and sandbox subcommands

**Files:**
- Create: `cmd/navaris/project.go`
- Create: `cmd/navaris/sandbox.go`

Project subcommands: create, list, get, update, delete.

Sandbox subcommands: create (with `--image`, `--from-snapshot`, `--name`, `--cpu`, `--memory`, `--port`), list, get, start, stop, destroy, exec, attach, port (publish/unpublish/list).

Exec: propagates child exit code. `-- command args` syntax. `--output json` streams JSON events.
Attach: bridges terminal stdin/stdout to WebSocket. Sets raw terminal mode.

- [ ] **Steps 1-3: Implement + build + commit**

```bash
git commit -m "feat: add CLI project and sandbox subcommands"
```

---

### Task 43: CLI session, snapshot, and image subcommands

**Files:**
- Create: `cmd/navaris/session.go`
- Create: `cmd/navaris/snapshot.go`
- Create: `cmd/navaris/image.go`

Session: create, list, get, attach, scrollback, input, destroy.
Snapshot: create, list, get, restore, delete.
Image: list, get, promote, register, delete.

- [ ] **Steps 1-3: Implement + build + commit**

```bash
git commit -m "feat: add CLI session, snapshot, and image subcommands"
```

---

### Task 44: CLI operation subcommands

**Files:**
- Create: `cmd/navaris/operation.go`

Operation: list, get, cancel, stream.

Stream: connects WebSocket, prints events as JSON lines.

- [ ] **Steps 1-3: Implement + build + commit**

```bash
git commit -m "feat: add CLI operation subcommands"
```

---

## Phase 10: Incus Provider

### Task 45: Incus Provider — setup and sandbox lifecycle

**Files:**
- Create: `internal/provider/incus/incus.go`
- Create: `internal/provider/incus/sandbox.go`

- [ ] **Step 1: Add Incus dependency**

```bash
go get github.com/lxc/incus/v6/client
go get github.com/lxc/incus/v6/shared/api
```

- [ ] **Step 2: Implement IncusProvider constructor and Health**

```go
// internal/provider/incus/incus.go
package incus

import (
    incusclient "github.com/lxc/incus/v6/client"
)

type Config struct {
    Socket       string // default: /var/lib/incus/unix.socket
    PortRangeMin int    // default: 40000
    PortRangeMax int    // default: 49999
}

type IncusProvider struct {
    client incusclient.InstanceServer
    config Config
}

func New(cfg Config) (*IncusProvider, error) {
    client, err := incusclient.ConnectIncusUnix(cfg.Socket, nil)
    if err != nil {
        return nil, err
    }
    return &IncusProvider{client: client, config: cfg}, nil
}
```

- [ ] **Step 3: Implement sandbox lifecycle methods** (sandbox.go)

`CreateSandbox`: calls `CreateInstance` with image source, container name `nvrs-{short-uuid}`, resource limits via `limits.cpu`/`limits.memory` config keys.

`StartSandbox`, `StopSandbox`, `DestroySandbox`: call corresponding Incus state/delete APIs.

`GetSandboxState`: maps Incus instance status to domain `SandboxState`.

- [ ] **Step 4: Commit**

```bash
git add internal/provider/incus/incus.go internal/provider/incus/sandbox.go
git commit -m "feat: add Incus provider setup and sandbox lifecycle"
```

---

### Task 46: Incus Provider — snapshots and images

**Files:**
- Create: `internal/provider/incus/snapshot.go`
- Create: `internal/provider/incus/image.go`

- [ ] **Step 1: Implement snapshot methods** (snapshot.go)

`CreateSnapshot`: `CreateInstanceSnapshot`. Live mode sets `Stateful: true`.
`RestoreSnapshot`, `DeleteSnapshot`: corresponding Incus APIs.
`CreateSandboxFromSnapshot`: `CopyInstance` from snapshot.

- [ ] **Step 2: Implement image methods** (image.go)

`PublishSnapshotAsImage`: Incus `Publish`.
`DeleteImage`, `GetImageInfo`: Incus image APIs.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/incus/snapshot.go internal/provider/incus/image.go
git commit -m "feat: add Incus provider snapshot and image methods"
```

---

### Task 47: Incus Provider — exec, sessions, networking

**Files:**
- Create: `internal/provider/incus/exec.go`
- Create: `internal/provider/incus/network.go`
- Create: `internal/provider/incus/incus_test.go`

- [ ] **Step 1: Implement exec methods** (exec.go)

`Exec`: Incus exec with separated stdout/stderr, returns `ExecHandle`.
`ExecDetached`: Incus exec with PTY, non-blocking, returns `DetachedExecHandle`.
`AttachSession`: Incus exec with PTY allocation, returns `SessionHandle`.

- [ ] **Step 2: Implement port methods** (network.go)

`PublishPort`: adds Incus proxy device. `UnpublishPort`: removes it.

- [ ] **Step 3: Write integration tests** (require live Incus, gated by build tag `//go:build integration`)

```bash
go test -tags integration ./internal/provider/incus/ -v
```

- [ ] **Step 4: Wire Incus provider into navarisd** — replace mock with real Incus when `--incus-socket` flag is set

- [ ] **Step 5: Commit**

```bash
git add internal/provider/incus/ cmd/navarisd/
git commit -m "feat: add Incus provider exec, sessions, and networking"
```

---

## Phase 11: Integration and Polish

### Task 48: End-to-end integration test

**Files:**
- Create: `test/integration/e2e_test.go`

Gated by `//go:build integration`. Requires a running navarisd with Incus.

Test flow:
1. Create project
2. Create sandbox from a base image
3. Wait for sandbox to be running
4. Exec a command, verify stdout
5. Create session, send input, read scrollback
6. Create snapshot (stop sandbox first)
7. Create new sandbox from snapshot
8. Promote snapshot to base image
9. Create sandbox from promoted image
10. Destroy everything

- [ ] **Step 1: Write test**
- [ ] **Step 2: Run against live environment**
- [ ] **Step 3: Commit**

```bash
git commit -m "feat: add end-to-end integration test"
```

---

### Task 49: Reconciliation on startup

**Files:**
- Modify: `cmd/navarisd/main.go`
- Create: `internal/service/reconcile.go`
- Create: `internal/service/reconcile_test.go`

On daemon startup, after opening the store and connecting to the provider:
1. Query sandboxes in Running state, verify against backend
2. Query operations in Running state, re-evaluate
3. For tmux-backed sessions, re-discover via exec `tmux list-sessions`

- [ ] **Steps 1-5: TDD cycle + commit**

```bash
git commit -m "feat: add startup reconciliation"
```

---

### Task 50: Final build verification

- [ ] **Step 1: Build both binaries**

```bash
go build -o bin/navarisd ./cmd/navarisd/
go build -o bin/navaris ./cmd/navaris/
```

- [ ] **Step 2: Run all unit tests**

```bash
go test ./... -v
```

- [ ] **Step 3: Run linter**

```bash
go vet ./...
```

- [ ] **Step 4: Commit any final fixes**

```bash
git commit -m "chore: final build verification and cleanup"
```
