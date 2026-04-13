# Tabbed Terminal Sessions Design

## Goal

Add persistent, tmux-backed terminal sessions to the Navaris UI. Users see a tab bar above the terminal, can open multiple sessions per sandbox (up to 5 in the UI; unlimited via API), and reconnect to existing sessions after disconnecting.

## Architecture

Sessions are backed by tmux processes inside the container. The server orchestrates tmux lifecycle (install, create, attach, kill) via the existing `Provider.Exec` and `Provider.AttachSession` methods. The frontend manages a tab bar and one xterm.js instance per session.

The existing session DB model, service, and REST endpoints are already in place and require only minor wiring changes.

## Existing infrastructure

The backend already has:

- **Domain model** (`internal/domain/session.go`): `Session` struct with `SessionID`, `SandboxID`, `Backing` (direct/tmux/auto), `Shell`, `State` (active/detached/exited/destroyed), `CreatedAt`, `UpdatedAt`, `LastAttachedAt`, `IdleTimeout`, `Metadata`. State machine with valid transitions defined.
- **Store interface** (`internal/domain/store.go`): `SessionStore` with Create, Get, ListBySandbox, Update, Delete.
- **Service** (`internal/service/session.go`): `SessionService` with Create (validates sandbox is running), Get, ListBySandbox, Destroy. Create defaults to `direct` backing and `/bin/bash` shell.
- **REST API** (`internal/api/session.go`): Four endpoints already registered in `server.go`:
  - `POST /v1/sandboxes/{id}/sessions` — create session
  - `GET /v1/sandboxes/{id}/sessions` — list sessions for a sandbox
  - `GET /v1/sessions/{id}` — get session by ID
  - `DELETE /v1/sessions/{id}` — destroy session
- **Attach handler** (`internal/api/attach.go`): Stateless WebSocket bridge. Calls `Provider.AttachSession` with a bare shell. Does not reference the session service.
- **Provider interface** (`internal/domain/provider.go`): `AttachSession(ctx, ref, SessionRequest) (SessionHandle, error)` where `SessionRequest` has a `Shell` field.

The frontend has:

- **Terminal page** (`web/src/routes/Terminal.tsx`): Single xterm.js instance, connects to `/v1/sandboxes/:id/attach` with no session tracking.
- **No sandbox session API client** — only auth session calls exist in `web/src/api/session.ts`.

## Section 1: Session lifecycle (backend)

### SessionRequest changes

The current `SessionRequest` struct has a single `Shell` field (string). The Incus provider uses it as `Command: []string{shell}`, which means a multi-word string like `tmux attach -t <id>` would fail (it would try to exec a single binary with that name).

Add a `Command []string` field to `SessionRequest`:

```go
type SessionRequest struct {
    Shell   string   // bare shell path (existing behavior)
    Command []string // full command with args (takes precedence over Shell if set)
}
```

The Incus `AttachSession` implementation checks `Command` first; if set, uses it directly as the `Command` field in `InstanceExecPost`. If empty, falls back to `[]string{shell}` (current behavior). Same change for Firecracker.

### Session creation (`POST /v1/sandboxes/:id/sessions`)

1. `SessionService.Create` creates the DB record with `Backing: tmux`, `State: active`.
2. Service ensures tmux is installed in the container (see Section 2).
3. Service runs `Provider.Exec` with command `["tmux", "new-session", "-d", "-s", "<session-id>", "<shell>"]` to start a detached tmux session inside the container. Must call `handle.Wait()` to confirm exit code 0, then close `handle.Stdout` and `handle.Stderr`.
4. If tmux new-session fails (non-zero exit code), the DB record is cleaned up and an error is returned.

### Attaching (`GET /v1/sandboxes/:id/attach?session=<session-id>`)

1. Attach handler reads the `session` query parameter.
2. Looks up the session record via `SessionService.Get` (the handler already has access to `s.cfg.Sessions`).
3. Validates the session belongs to the sandbox in the URL path.
4. Validates session state is `active` or `detached`.
5. Calls `Provider.AttachSession` with `SessionRequest{Command: []string{"tmux", "attach", "-t", "<session-id>"}}`.
6. On WebSocket close, calls `SessionService.Detach(id)` (new method — see below) to set state to `detached` and `LastAttachedAt`.

### Concurrent attaches

Two browser tabs can attach to the same tmux session simultaneously. tmux handles this natively — both see the same output. On disconnect, both close handlers call `Detach`, which is idempotent (sets state to `detached` regardless of current state, as long as the transition is valid).

### SessionService new methods

Add `Detach(ctx, id)` — validates `CanTransitionTo(SessionDetached)`, sets state and `LastAttachedAt`. All state transitions in the service (Create, Destroy, Detach) must use the domain's `CanTransitionTo` validation.

### Session destroy (`DELETE /v1/sessions/:id`)

1. Looks up the session and the associated sandbox to get the `BackendRef`.
2. Runs `Provider.Exec` with `["tmux", "kill-session", "-t", "<session-id>"]`. Calls `handle.Wait()` and closes streams.
3. Validates `CanTransitionTo(SessionDestroyed)`, updates DB record state to `destroyed`.

### Sandbox stop/destroy cleanup

When a sandbox is stopped, all tmux processes inside it die. The sandbox stop handler (`internal/service/sandbox.go`) must mark all `active`/`detached` sessions for that sandbox as `exited`. This prevents stale session records that claim to be alive when the tmux processes no longer exist. Add a `SessionService.ExitAllForSandbox(ctx, sandboxID)` method that bulk-transitions sessions.

Clear the tmux-installed cache entry for the sandbox ID on stop.

### Backward compatibility

If `?session=` is omitted on the attach endpoint, current behavior is preserved: a bare shell with no persistence.

### API session limit

The API does not enforce a session cap. Any number of sessions can be created via the API. The 5-session limit is enforced only in the UI.

## Section 2: Lazy tmux installation

On the first session creation for a sandbox, before running `tmux new-session`:

1. Run `Provider.Exec` with `["command", "-v", "tmux"]` — check exit code.
2. If tmux is not found, detect the package manager and install:
   - `command -v apk` exists: run `apk add --no-cache tmux`
   - `command -v apt-get` exists: run `apt-get update && apt-get install -y --no-install-recommends tmux`
   - Neither found: fail session creation with error "tmux not available and no supported package manager found"
3. Cache the result in the session service (in-memory map, keyed by sandbox ID) so subsequent session creates for the same sandbox skip the probe. Clear the cache entry when the sandbox is stopped or destroyed.

This runs synchronously during `POST /sessions`. Install typically takes 2-5 seconds. The frontend should show a loading indicator while the first session is being created.

All `Exec` calls must consume the `ExecHandle` properly: call `handle.Wait()` for exit code, then close `handle.Stdout` and `handle.Stderr`.

## Section 3: Attach handler changes

Changes to `internal/api/attach.go`:

1. `attachSandbox` reads optional `session` query parameter from the WebSocket URL.
2. If present:
   - Look up session record via `s.cfg.Sessions.Get`.
   - Validate it belongs to the sandbox in the URL.
   - Validate state is `active` or `detached`.
   - Call `Provider.AttachSession` with `SessionRequest{Command: []string{"tmux", "attach", "-t", "<session-id>"}}`.
   - On WebSocket close, call `s.cfg.Sessions.Detach(id)` to set state to `detached` and `LastAttachedAt`.
3. If absent:
   - Current behavior unchanged: bare shell, no persistence.

The `SessionRequest` struct gains a `Command []string` field (see Section 1). Both Incus and Firecracker providers must be updated to check `Command` first, falling back to `[]string{shell}`.

## Section 4: Frontend tab bar and session management

### Data flow on mount

1. Fetch `GET /v1/sandboxes/:id/sessions` to list existing sessions (filter out `destroyed`/`exited`).
2. If no live sessions exist, auto-create one via `POST /v1/sandboxes/:id/sessions`.
3. Attach to the first (or most recently attached) session via WebSocket with `?session=<id>`.

### Tab bar UI

- Horizontal tab strip above the terminal, matching the existing design system (monospace, small text, subtle borders).
- Each tab shows a stable label: "Session 1", "Session 2", etc. Labels are assigned at creation time based on order and do not change when other sessions are destroyed.
- Active tab has a distinct border/color; inactive tabs are muted.
- `+` button at the end creates a new session. Hidden when 5 live sessions already exist (UI-only cap).
- Each tab has a small `x` to destroy that session, using an HTML `<dialog>` for confirmation (same pattern as the sandbox destroy dialog).
- Clicking a tab disconnects the current WebSocket and opens a new one to the selected session.

### Terminal instance management

- One xterm.js instance per tab, kept alive in memory while the tab exists. Scrollback is preserved when switching tabs.
- When switching tabs: hide the current terminal div, show the target one, call `fit.fit()`.
- When a session is destroyed: dispose its xterm instance and WebSocket, switch to the nearest remaining tab.

### Reconnection indicator

When attaching to a `detached` session, show a brief "Reconnected" flash in the status area (where `ws . open` currently appears). Note: scrollback from previous connections is not restored — only new output from tmux appears. The tmux session preserves running processes and state, but the xterm.js buffer starts fresh on reconnect.

### Loading state

While the first session is being created (which may involve tmux installation taking 2-5 seconds), show a loading indicator in the terminal area.

## Section 5: Frontend API client

New file: `web/src/api/sandboxSessions.ts`

```typescript
listSessions(sandboxId: string)    // GET /v1/sandboxes/:id/sessions
createSession(sandboxId: string, shell?: string)  // POST /v1/sandboxes/:id/sessions
destroySession(sessionId: string)  // DELETE /v1/sessions/:id
```

Uses the existing `apiFetch` wrapper. Types mirror the backend `Session` struct.

## Files affected

### Backend (modify)
- `internal/domain/provider.go` — add `Command []string` to `SessionRequest`
- `internal/service/session.go` — add tmux orchestration to Create/Destroy, add tmux install logic, add `Detach` and `ExitAllForSandbox` methods
- `internal/service/sandbox.go` — call `SessionService.ExitAllForSandbox` on sandbox stop
- `internal/api/attach.go` — read `session` query param, look up session, call `Detach` on close
- `internal/provider/incus/exec.go` — update `AttachSession` to use `Command` field when set
- `internal/provider/firecracker/exec.go` — same `Command` field support

### Frontend (modify)
- `web/src/routes/Terminal.tsx` — tab bar, multi-instance xterm management, session lifecycle

### Frontend (create)
- `web/src/api/sandboxSessions.ts` — session API client

## Testing

### Backend
- `service/session_test.go` — test tmux install detection, session create/destroy with exec calls
- `api/attach_test.go` — test `?session=` param routing, state transitions on close

### Frontend
- `routes/Terminal.test.tsx` — tab rendering, session auto-create, tab switching, destroy dialog
- `api/sandboxSessions.test.ts` — API client tests
