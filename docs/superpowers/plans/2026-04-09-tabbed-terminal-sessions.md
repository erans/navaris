# Tabbed Terminal Sessions Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persistent, tmux-backed terminal sessions with a tabbed UI so users can open multiple shells per sandbox and reconnect after disconnect.

**Architecture:** Each terminal session maps to a tmux session inside the container. The server orchestrates tmux lifecycle (lazy install, create, attach, kill) via `Provider.Exec` and `Provider.AttachSession`. The attach WebSocket endpoint gains an optional `?session=<id>` param. The frontend gets a tab bar with one xterm.js instance per session.

**Tech Stack:** Go (backend service/API), SQLite (session store), React + xterm.js (frontend), tmux (in-container session persistence)

**Spec:** `docs/superpowers/specs/2026-04-09-tabbed-terminal-sessions-design.md`

---

### Task 1: Add `Command` field to `SessionRequest`

The `SessionRequest` struct currently only has `Shell string`. We need `Command []string` so providers can receive a full command like `["tmux", "attach", "-t", "<id>"]` instead of a single shell path.

**Files:**
- Modify: `internal/domain/provider.go:29-31`

- [ ] **Step 1: Add the `Command` field**

In `internal/domain/provider.go`, change the `SessionRequest` struct from:

```go
type SessionRequest struct {
	Shell string
}
```

to:

```go
type SessionRequest struct {
	Shell   string
	Command []string // Full command with args; takes precedence over Shell if non-empty.
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /home/eran/work/navaris && go build ./...`
Expected: PASS (the new field is additive; existing code that sets only `Shell` still compiles)

- [ ] **Step 3: Commit**

```bash
git add internal/domain/provider.go
git commit -m "feat(domain): add Command field to SessionRequest"
```

---

### Task 2: Update Incus provider to use `Command` field

The Incus `AttachSession` at `internal/provider/incus/exec.go:147-209` currently uses `Command: []string{shell}` (line 160). It needs to check `req.Command` first.

**Files:**
- Modify: `internal/provider/incus/exec.go:147-165`

- [ ] **Step 1: Update `AttachSession` to prefer `Command`**

In `internal/provider/incus/exec.go`, find the `AttachSession` method. After the shell detection block (lines 152-154), replace the `execReq` construction (lines 159-165) with:

```go
	cmd := req.Command
	if len(cmd) == 0 {
		cmd = []string{shell}
	}

	execReq := incusapi.InstanceExecPost{
		Command:     cmd,
		WaitForWS:   true,
		Interactive: true,
		Width:       80,
		Height:      24,
	}
```

The shell detection logic (`detectShell`) still runs when `req.Shell` is empty, but `req.Command` takes final precedence.

- [ ] **Step 2: Verify compilation**

Run: `cd /home/eran/work/navaris && go build -tags incus ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/provider/incus/exec.go
git commit -m "feat(incus): support Command field in AttachSession"
```

---

### Task 3: Update Firecracker provider to use `Command` field

The Firecracker `AttachSession` at `internal/provider/firecracker/exec.go:202-232` passes `req.Shell` to the vsock agent via `SessionPayload{Shell: req.Shell}` (line 211). It needs to support `Command` too.

**Files:**
- Modify: `internal/provider/firecracker/exec.go:202-215`

- [ ] **Step 1: Check the `SessionPayload` struct**

Read `internal/provider/firecracker/vsock/protocol.go` (or wherever `SessionPayload` is defined) to see if it already has a `Command` field. If not, add one:

```go
type SessionPayload struct {
	Shell   string   `json:"shell,omitempty"`
	Command []string `json:"command,omitempty"`
}
```

- [ ] **Step 2: Update `AttachSession` to pass `Command`**

In `internal/provider/firecracker/exec.go`, update the `AttachSession` method to set the `Command` field:

```go
	payload := fcvsock.SessionPayload{
		Shell:   req.Shell,
		Command: req.Command,
	}
	session, err := client.Session(payload)
```

- [ ] **Step 3: Update the vsock agent to use `Command`**

In `cmd/navaris-agent/agent/session.go`, the `HandleSession` function uses `allocPTY(shell)` which calls `exec.Command(shell)` — a single string. When `Command` is set (e.g., `["tmux", "attach", "-t", "<id>"]`), the agent must use `exec.CommandContext(ctx, cmd[0], cmd[1:]...)` instead. 

Modify `HandleSession` to check `payload.Command` first:

```go
	var cmd *exec.Cmd
	if len(payload.Command) > 0 {
		cmd = exec.CommandContext(ctx, payload.Command[0], payload.Command[1:]...)
	} else {
		shell := payload.Shell
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.CommandContext(ctx, shell)
	}
```

Then pass `cmd` to the PTY allocation, replacing the existing `allocPTY(shell)` call. If `allocPTY` only accepts a string, either refactor it to accept `*exec.Cmd` or inline the PTY setup. The key change: the `exec.Cmd` must be built from the `Command` slice, not a single shell string.

- [ ] **Step 4: Verify compilation**

Run: `cd /home/eran/work/navaris && go build -tags firecracker ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/
git commit -m "feat(firecracker): support Command field in AttachSession"
```

---

### Task 4: Add `Detach` and `ExitAllForSandbox` methods to SessionService

The service currently has Create, Get, ListBySandbox, and Destroy. We need:
- `Detach(ctx, id)` — transitions session to `detached`, sets `LastAttachedAt`
- `ExitAllForSandbox(ctx, sandboxID)` — bulk-transitions all active/detached sessions to `exited`

Both must validate state transitions via the domain's `CanTransitionTo`.

**Files:**
- Modify: `internal/service/session.go:105-123`
- Test: `internal/service/session_test.go`

- [ ] **Step 1: Write failing test for `Detach`**

In `internal/service/session_test.go`, add. **Note:** The existing tests create `sessSvc` inline via `service.NewSessionService(...)` — follow this pattern, do NOT reference `env.session` (no such field exists on `serviceEnv`):

```go
func TestSessionServiceDetach(t *testing.T) {
	env := newServiceEnv(t)
	sessSvc := service.NewSessionService(
		env.store.SessionStore(), env.store.SandboxStore(), env.mock, env.events,
	)

	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "detach-test", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	sess, err := sessSvc.Create(t.Context(), createOp.ResourceID, "", "")
	require.NoError(t, err)
	require.Equal(t, domain.SessionActive, sess.State)

	err = sessSvc.Detach(t.Context(), sess.SessionID)
	require.NoError(t, err)

	got, err := sessSvc.Get(t.Context(), sess.SessionID)
	require.NoError(t, err)
	assert.Equal(t, domain.SessionDetached, got.State)
	assert.NotNil(t, got.LastAttachedAt)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/eran/work/navaris && go test ./internal/service/ -run TestSessionServiceDetach -v`
Expected: FAIL — `env.session.Detach` does not exist

- [ ] **Step 3: Implement `Detach`**

In `internal/service/session.go`, add after the `Destroy` method:

```go
func (s *SessionService) Detach(ctx context.Context, id string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.DetachSession")
	defer span.End()

	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if !sess.State.CanTransitionTo(domain.SessionDetached) {
		return fmt.Errorf("cannot detach session in state %s: %w", sess.State, domain.ErrInvalidState)
	}
	now := time.Now().UTC()
	sess.State = domain.SessionDetached
	sess.UpdatedAt = now
	sess.LastAttachedAt = &now
	if err := s.sessions.Update(ctx, sess); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/eran/work/navaris && go test ./internal/service/ -run TestSessionServiceDetach -v`
Expected: PASS

- [ ] **Step 5: Write failing test for `ExitAllForSandbox`**

Follow the same inline `sessSvc` pattern:

```go
func TestSessionServiceExitAllForSandbox(t *testing.T) {
	env := newServiceEnv(t)
	sessSvc := service.NewSessionService(
		env.store.SessionStore(), env.store.SandboxStore(), env.mock, env.events,
	)

	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "exitall-test", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	s1, err := sessSvc.Create(t.Context(), createOp.ResourceID, "", "")
	require.NoError(t, err)
	s2, err := sessSvc.Create(t.Context(), createOp.ResourceID, "", "")
	require.NoError(t, err)

	err = sessSvc.ExitAllForSandbox(t.Context(), createOp.ResourceID)
	require.NoError(t, err)

	got1, _ := sessSvc.Get(t.Context(), s1.SessionID)
	got2, _ := sessSvc.Get(t.Context(), s2.SessionID)
	assert.Equal(t, domain.SessionExited, got1.State)
	assert.Equal(t, domain.SessionExited, got2.State)
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd /home/eran/work/navaris && go test ./internal/service/ -run TestSessionServiceExitAllForSandbox -v`
Expected: FAIL

- [ ] **Step 7: Implement `ExitAllForSandbox`**

```go
func (s *SessionService) ExitAllForSandbox(ctx context.Context, sandboxID string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ExitAllForSandbox")
	defer span.End()
	span.SetAttributes(attribute.String("sandbox.id", sandboxID))

	sessions, err := s.sessions.ListBySandbox(ctx, sandboxID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	now := time.Now().UTC()
	for _, sess := range sessions {
		if !sess.State.CanTransitionTo(domain.SessionExited) {
			continue
		}
		sess.State = domain.SessionExited
		sess.UpdatedAt = now
		if err := s.sessions.Update(ctx, sess); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}
	return nil
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd /home/eran/work/navaris && go test ./internal/service/ -run TestSessionServiceExitAllForSandbox -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/service/session.go internal/service/session_test.go
git commit -m "feat(service): add Detach and ExitAllForSandbox to SessionService"
```

---

### Task 5: Add tmux install and orchestration to SessionService

Wire tmux lifecycle into Create and Destroy. Create must: check/install tmux, then run `tmux new-session`. Destroy must: run `tmux kill-session` before marking destroyed.

This requires the `SessionService` to hold a reference to `Provider` (it already does — field `provider` at line 18 of `session.go`) and to the `SandboxStore` (field `sandboxes` at line 17).

**Files:**
- Modify: `internal/service/session.go`
- Test: `internal/service/session_test.go`

- [ ] **Step 1: Add tmux-installed cache field**

In `internal/service/session.go`, add a field to the `SessionService` struct:

```go
type SessionService struct {
	sessions  domain.SessionStore
	sandboxes domain.SandboxStore
	provider  domain.Provider
	events    domain.EventBus
	tmuxReady sync.Map // sandboxID → bool; caches whether tmux is installed
}
```

Add `"sync"` to imports.

- [ ] **Step 2: Add `ensureTmux` helper**

Add a private method that checks whether tmux is available and installs it if not. This uses `Provider.Exec` to run commands inside the container:

```go
func (s *SessionService) ensureTmux(ctx context.Context, sandboxID string, ref domain.BackendRef) error {
	if _, ok := s.tmuxReady.Load(sandboxID); ok {
		return nil
	}

	// Check if tmux is already installed.
	if s.execCheck(ctx, ref, []string{"command", "-v", "tmux"}) {
		s.tmuxReady.Store(sandboxID, true)
		return nil
	}

	// Detect package manager and install.
	var installCmd []string
	switch {
	case s.execCheck(ctx, ref, []string{"command", "-v", "apk"}):
		installCmd = []string{"apk", "add", "--no-cache", "tmux"}
	case s.execCheck(ctx, ref, []string{"command", "-v", "apt-get"}):
		installCmd = []string{"sh", "-c", "apt-get update && apt-get install -y --no-install-recommends tmux"}
	default:
		return fmt.Errorf("tmux not available and no supported package manager found")
	}

	if err := s.execRun(ctx, ref, installCmd); err != nil {
		return fmt.Errorf("install tmux: %w", err)
	}
	s.tmuxReady.Store(sandboxID, true)
	return nil
}

// execCheck runs a command and returns true if exit code is 0.
func (s *SessionService) execCheck(ctx context.Context, ref domain.BackendRef, cmd []string) bool {
	handle, err := s.provider.Exec(ctx, ref, domain.ExecRequest{Command: cmd})
	if err != nil {
		return false
	}
	defer handle.Stdout.Close()
	defer handle.Stderr.Close()
	code, err := handle.Wait()
	return err == nil && code == 0
}

// execRun runs a command and returns an error if it fails.
func (s *SessionService) execRun(ctx context.Context, ref domain.BackendRef, cmd []string) error {
	handle, err := s.provider.Exec(ctx, ref, domain.ExecRequest{Command: cmd})
	if err != nil {
		return err
	}
	defer handle.Stdout.Close()
	defer handle.Stderr.Close()
	code, err := handle.Wait()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("command %v exited with code %d", cmd, code)
	}
	return nil
}
```

- [ ] **Step 3: Update `Create` to orchestrate tmux**

In the existing `Create` method, after the DB record is persisted (after the `s.sessions.Create` call at line 70), add tmux orchestration. The method needs the sandbox's `BackendRef`, so look it up:

```go
	// --- after s.sessions.Create(ctx, sess) succeeds ---

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}

	if err := s.ensureTmux(ctx, sbx.SandboxID, ref); err != nil {
		// Clean up the DB record on failure.
		_ = s.sessions.Delete(ctx, sess.SessionID)
		return nil, fmt.Errorf("ensure tmux: %w", err)
	}

	tmuxCmd := []string{"tmux", "new-session", "-d", "-s", sess.SessionID, shell}
	if err := s.execRun(ctx, ref, tmuxCmd); err != nil {
		_ = s.sessions.Delete(ctx, sess.SessionID)
		return nil, fmt.Errorf("tmux new-session: %w", err)
	}

	return sess, nil
```

Also update the backing default from `SessionBackingDirect` to `SessionBackingTmux`:

```go
	if backing == "" || backing == domain.SessionBackingAuto {
		backing = domain.SessionBackingTmux
	}
```

- [ ] **Step 4: Replace existing `Destroy` with tmux-aware version**

**Important:** This completely replaces the existing `Destroy` method body in `internal/service/session.go:105-123`. The existing version lacks `CanTransitionTo` validation and has no tmux cleanup. Replace it entirely with:

```go
func (s *SessionService) Destroy(ctx context.Context, id string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.DestroySession")
	defer span.End()

	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	if !sess.State.CanTransitionTo(domain.SessionDestroyed) {
		return fmt.Errorf("cannot destroy session in state %s: %w", sess.State, domain.ErrInvalidState)
	}

	// Kill tmux session if the sandbox is still accessible.
	if sbx, err := s.sandboxes.Get(ctx, sess.SandboxID); err == nil && sbx.State == domain.SandboxRunning {
		ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
		_ = s.execRun(ctx, ref, []string{"tmux", "kill-session", "-t", sess.SessionID})
	}

	sess.State = domain.SessionDestroyed
	sess.UpdatedAt = time.Now().UTC()
	if err := s.sessions.Update(ctx, sess); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}
```

- [ ] **Step 5: Add `ClearTmuxCache` method**

```go
func (s *SessionService) ClearTmuxCache(sandboxID string) {
	s.tmuxReady.Delete(sandboxID)
}
```

This will be called from the sandbox stop handler (Task 6).

- [ ] **Step 6: Verify compilation**

Run: `cd /home/eran/work/navaris && go build ./...`
Expected: PASS

- [ ] **Step 7: Update existing tests**

The existing `TestSessionServiceCreate` test uses the mock provider which doesn't implement `Exec`. Add an `ExecFn` to the mock provider (if not present) that returns a successful no-op handle for tmux commands. Update the test env to wire this up.

Check the mock at `internal/provider/mock.go` — if `ExecFn` doesn't exist, add it:

```go
ExecFn func(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error)
```

With a default implementation that returns a handle with a successful wait (exit code 0):

```go
ExecFn: func(_ context.Context, _ domain.BackendRef, _ domain.ExecRequest) (domain.ExecHandle, error) {
	return domain.ExecHandle{
		Stdout: io.NopCloser(strings.NewReader("")),
		Stderr: io.NopCloser(strings.NewReader("")),
		Wait:   func() (int, error) { return 0, nil },
		Cancel: func() error { return nil },
	}, nil
},
```

Also add the `Exec` method to MockProvider:

```go
func (m *MockProvider) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error) {
	return m.ExecFn(ctx, ref, req)
}
```

- [ ] **Step 8: Run all service tests**

Run: `cd /home/eran/work/navaris && go test ./internal/service/ -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/service/session.go internal/service/session_test.go internal/provider/mock.go
git commit -m "feat(service): add tmux install and orchestration to SessionService"
```

---

### Task 6: Wire `ExitAllForSandbox` into sandbox stop

When a sandbox is stopped, all tmux processes inside it die. We must mark all active/detached sessions as `exited` and clear the tmux cache.

**Files:**
- Modify: `internal/service/sandbox.go` (the `handleStop` method)

- [ ] **Step 1: Find the stop handler**

In `internal/service/sandbox.go`, locate the `handleStop` method. It handles the async stop operation after `StopSandbox` is called. Find where it sets the sandbox state to `stopped` — that's where we add the session cleanup.

- [ ] **Step 2: Add session cleanup after stop**

The `SandboxService` needs access to the `SessionService`. Check if it already has it. If not, add a field:

```go
type SandboxService struct {
	// ... existing fields ...
	sessions *SessionService
}
```

And update `NewSandboxService` to accept and store it.

After the sandbox state is set to `stopped` in `handleStop`, add:

```go
	if s.sessions != nil {
		if err := s.sessions.ExitAllForSandbox(ctx, sbx.SandboxID); err != nil {
			s.log.Error("failed to exit sessions on sandbox stop", "error", err, "sandbox_id", sbx.SandboxID)
		}
		s.sessions.ClearTmuxCache(sbx.SandboxID)
	}
```

Do the same in `handleDestroy` if it doesn't already clean up sessions.

- [ ] **Step 3: Update constructor wiring**

Find where `NewSandboxService` is called (likely in `cmd/navarisd/main.go` or a setup function) and pass the `SessionService` instance.

- [ ] **Step 4: Verify compilation**

Run: `cd /home/eran/work/navaris && go build ./...`
Expected: PASS

- [ ] **Step 5: Run sandbox service tests**

Run: `cd /home/eran/work/navaris && go test ./internal/service/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/service/sandbox.go internal/service/session.go cmd/navarisd/main.go
git commit -m "feat(service): exit all sessions on sandbox stop"
```

---

### Task 7: Wire session param into attach handler

The attach handler needs to read `?session=<id>` from the WebSocket URL and use it to attach to a specific tmux session. On disconnect, it calls `SessionService.Detach`.

**Files:**
- Modify: `internal/api/attach.go:19-37, 45-67`
- Test: `internal/api/attach_test.go`

- [ ] **Step 1: Write failing test for session-aware attach**

In `internal/api/attach_test.go`, add a test that creates a session and attaches with `?session=<id>`:

```go
func TestAttachWithSessionParam(t *testing.T) {
	env := newTestEnv(t)
	sbx := env.createRunningSandbox(t)

	// Create a session via the API.
	body := `{"backing":"tmux","shell":"/bin/bash"}`
	resp := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sbx.SandboxID+"/sessions", body)
	require.Equal(t, http.StatusCreated, resp.Code)

	var sess struct{ SessionID string }
	json.Unmarshal(resp.Body.Bytes(), &sess)

	// Set up mock to capture the Command field.
	var gotCommand []string
	env.mock.AttachSessionFn = func(_ context.Context, _ domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
		gotCommand = req.Command
		r, w := io.Pipe()
		return domain.SessionHandle{
			Conn:   &pipeConn{Reader: r, Writer: w, closeFn: func() error { r.Close(); w.Close(); return nil }},
			Resize: func(int, int) error { return nil },
			Close:  func() error { r.Close(); w.Close(); return nil },
		}, nil
	}

	// Connect with ?session= param.
	wsURL := "/v1/sandboxes/" + sbx.SandboxID + "/attach?session=" + sess.SessionID
	// ... WebSocket dial and verify gotCommand contains ["tmux", "attach", "-t", sess.SessionID]
	assert.Equal(t, []string{"tmux", "attach", "-t", sess.SessionID}, gotCommand)
}
```

Adapt this test to match the exact test patterns in `attach_test.go` (using `newPipeConn`, the WebSocket dial helper, etc.).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/eran/work/navaris && go test ./internal/api/ -run TestAttachWithSessionParam -v`
Expected: FAIL — no session-aware logic in attachSandbox

- [ ] **Step 3: Update `attachSandbox`**

In `internal/api/attach.go`, update `attachSandbox` to read the session param before calling `bridgeAttach`:

```go
func (s *Server) attachSandbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sandboxID := r.PathValue("id")

	sbx, err := s.cfg.Sandboxes.Get(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			respondError(w, domain.ErrNotFound)
			return
		}
		respondError(w, err)
		return
	}
	if sbx.State != domain.SandboxRunning {
		respondError(w, domain.ErrConflict)
		return
	}

	// If a session ID is provided, validate it and build a tmux attach command.
	sessionID := r.URL.Query().Get("session")
	if sessionID != "" {
		sess, err := s.cfg.Sessions.Get(ctx, sessionID)
		if err != nil {
			respondError(w, err)
			return
		}
		if sess.SandboxID != sandboxID {
			respondError(w, domain.ErrNotFound)
			return
		}
		if sess.State != domain.SessionActive && sess.State != domain.SessionDetached {
			respondError(w, domain.ErrConflict)
			return
		}
	}

	s.bridgeAttach(w, r, sbx, sessionID)
}
```

- [ ] **Step 4: Update `bridgeAttach` signature and logic**

Change `bridgeAttach` to accept `sessionID string`:

```go
func (s *Server) bridgeAttach(w http.ResponseWriter, r *http.Request, sbx *domain.Sandbox, sessionID string) {
```

In the `SessionRequest` construction (around line 64-67), use `Command` when a session is specified:

```go
	var sessReq domain.SessionRequest
	if sessionID != "" {
		sessReq = domain.SessionRequest{Command: []string{"tmux", "attach", "-t", sessionID}}
	} else {
		shell := r.URL.Query().Get("shell")
		sessReq = domain.SessionRequest{Shell: shell}
	}

	handle, err := s.cfg.Provider.AttachSession(bridgeCtx, domain.BackendRef{
		Backend: sbx.Backend,
		Ref:     sbx.BackendRef,
	}, sessReq)
```

- [ ] **Step 5: Add detach on WebSocket close**

At the end of `bridgeAttach`, in the `defer` section (near the existing `defer handle.Close()` and `defer conn.Close()`), add session detach:

```go
	if sessionID != "" {
		defer func() {
			_ = s.cfg.Sessions.Detach(context.Background(), sessionID)
		}()
	}
```

Place this before the `defer conn.Close()` so it runs after the bridge loop ends but before the WS closes.

- [ ] **Step 6: Run test to verify it passes**

Run: `cd /home/eran/work/navaris && go test ./internal/api/ -run TestAttachWithSessionParam -v`
Expected: PASS

- [ ] **Step 7: Run all attach tests to verify no regressions**

Run: `cd /home/eran/work/navaris && go test ./internal/api/ -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/api/attach.go internal/api/attach_test.go
git commit -m "feat(api): wire session param into attach handler"
```

---

### Task 8: Add `Session` type to frontend types

**Files:**
- Modify: `web/src/types/navaris.ts`

- [ ] **Step 1: Add Session type**

In `web/src/types/navaris.ts`, add the Session interface. Follow the existing PascalCase convention (mirrors Go struct field names):

```typescript
export type SessionState = "active" | "detached" | "exited" | "destroyed";

export interface Session {
  SessionID: string;
  SandboxID: string;
  Backing: string;
  Shell: string;
  State: SessionState;
  CreatedAt: string;
  UpdatedAt: string;
  LastAttachedAt?: string;
  IdleTimeout?: number;
  Metadata?: Record<string, unknown> | null;
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /home/eran/work/navaris/web && npx tsc --noEmit`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add web/src/types/navaris.ts
git commit -m "feat(web): add Session type"
```

---

### Task 9: Create frontend session API client

**Files:**
- Create: `web/src/api/sandboxSessions.ts`
- Create: `web/src/api/sandboxSessions.test.ts`

- [ ] **Step 1: Write the API client**

Create `web/src/api/sandboxSessions.ts`:

```typescript
import { apiFetch } from "./client";
import type { ListResponse } from "@/types/navaris";
import type { Session } from "@/types/navaris";

export async function listSessions(sandboxId: string): Promise<Session[]> {
  const res = await apiFetch<ListResponse<Session>>(
    `/v1/sandboxes/${encodeURIComponent(sandboxId)}/sessions`,
  );
  return res.data;
}

export async function createSession(
  sandboxId: string,
  shell?: string,
): Promise<Session> {
  return apiFetch<Session>(
    `/v1/sandboxes/${encodeURIComponent(sandboxId)}/sessions`,
    { method: "POST", json: { backing: "tmux", shell: shell || "" } },
  );
}

export async function destroySession(sessionId: string): Promise<void> {
  await apiFetch(`/v1/sessions/${encodeURIComponent(sessionId)}`, {
    method: "DELETE",
  });
}
```

**Important:** The list endpoint wraps responses in a `{ data: [...], pagination: ... }` envelope (via `respondList` in the backend). You must unwrap with `apiFetch<ListResponse<Session>>` and return `res.data`, matching the pattern in `sandboxes.ts`.

- [ ] **Step 2: Write tests**

Create `web/src/api/sandboxSessions.test.ts` following the pattern in existing API test files (e.g., `web/src/api/session.test.ts`). Test that:
- `listSessions` calls `GET /v1/sandboxes/:id/sessions` and returns parsed JSON
- `createSession` calls `POST` with `{backing: "tmux", shell: ""}` body
- `destroySession` calls `DELETE /v1/sessions/:id`

- [ ] **Step 3: Run tests**

Run: `cd /home/eran/work/navaris/web && npx vitest run src/api/sandboxSessions.test.ts`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add web/src/api/sandboxSessions.ts web/src/api/sandboxSessions.test.ts
git commit -m "feat(web): add sandbox session API client"
```

---

### Task 10: Rebuild Terminal.tsx with tab bar

Replace the single-terminal `Terminal.tsx` with a tabbed multi-session terminal. This is the largest frontend task.

**Files:**
- Modify: `web/src/routes/Terminal.tsx`
- Test: `web/src/routes/Terminal.test.tsx` (if exists, update; otherwise create)

- [ ] **Step 1: Plan the component structure**

The new `Terminal.tsx` has three pieces:
1. **Data fetching**: on mount, list sessions, auto-create if empty
2. **Tab bar**: renders tabs for each live session, `+` button, `x` per tab
3. **Terminal panels**: one xterm.js instance per session, show/hide on tab switch

State:
- `sessions: Session[]` — fetched from API, filtered to non-destroyed/exited
- `activeSessionId: string | null` — which tab is selected
- `terminalRefs: Map<string, {term, fit, ws}>` — xterm instances per session

- [ ] **Step 2: Implement data fetching and auto-create**

At the top of the component, fetch sessions and auto-create:

```typescript
const { id } = useParams<{ id: string }>();
const [sessions, setSessions] = useState<Session[]>([]);
const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
const [loading, setLoading] = useState(true);

useEffect(() => {
  if (!id) return;
  (async () => {
    let list = await listSessions(id);
    list = list.filter((s) => s.State !== "destroyed" && s.State !== "exited");
    if (list.length === 0) {
      const created = await createSession(id);
      list = [created];
    }
    setSessions(list);
    setActiveSessionId(list[0]?.SessionID ?? null);
    setLoading(false);
  })();
}, [id]);
```

- [ ] **Step 3: Implement the tab bar**

Render a horizontal strip above the terminal:

```tsx
<div className="flex items-center gap-0 border-b border-[var(--border-subtle)]">
  {sessions.map((s, i) => (
    <button
      key={s.SessionID}
      type="button"
      onClick={() => setActiveSessionId(s.SessionID)}
      className={[
        "flex items-center gap-1.5 px-3 py-1.5 font-mono text-[11px] border-r border-[var(--border-subtle)]",
        s.SessionID === activeSessionId
          ? "text-[var(--fg-primary)] bg-[var(--bg-primary)]"
          : "text-[var(--fg-muted)] hover:text-[var(--fg-secondary)]",
      ].join(" ")}
    >
      <span>Session {i + 1}</span>
      <span
        onClick={(e) => { e.stopPropagation(); openDestroyDialog(s); }}
        className="ml-1 text-[9px] text-[var(--fg-muted)] hover:text-[var(--status-failed)] cursor-pointer"
      >
        x
      </span>
    </button>
  ))}
  {sessions.length < 5 && (
    <button
      type="button"
      onClick={handleNewSession}
      className="px-2 py-1.5 font-mono text-[11px] text-[var(--fg-muted)] hover:text-[var(--fg-primary)]"
    >
      +
    </button>
  )}
</div>
```

- [ ] **Step 4: Implement terminal panel management**

Each session gets its own div. Use a ref map to track xterm instances:

```typescript
const terminalContainers = useRef<Map<string, HTMLDivElement>>(new Map());
const terminalInstances = useRef<Map<string, { term: XTerm; fit: FitAddon; ws: WebSocket }>>(new Map());
```

When a session becomes active and doesn't have a terminal yet, create one:

```typescript
useEffect(() => {
  if (!activeSessionId || !id) return;
  const existing = terminalInstances.current.get(activeSessionId);
  if (existing) {
    existing.fit.fit();
    return;
  }
  const container = terminalContainers.current.get(activeSessionId);
  if (!container) return;

  const term = new XTerm({ /* same theme config as current */ });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(container);
  fit.fit();

  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(
    `${proto}//${window.location.host}/v1/sandboxes/${encodeURIComponent(id)}/attach?session=${encodeURIComponent(activeSessionId)}`,
  );
  ws.binaryType = "arraybuffer";

  ws.onopen = () => ws.send(encodeResizeMessage(term.cols, term.rows));
  ws.onmessage = (msg) => {
    if (msg.data instanceof ArrayBuffer) term.write(new Uint8Array(msg.data));
  };

  term.onData((data) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(encodeInputBytes(data));
  });
  term.onResize(({ cols, rows }) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(encodeResizeMessage(cols, rows));
  });

  terminalInstances.current.set(activeSessionId, { term, fit, ws });
}, [activeSessionId, id, sessions]);
```

Render all panels, hide inactive ones:

```tsx
{sessions.map((s) => (
  <div
    key={s.SessionID}
    ref={(el) => { if (el) terminalContainers.current.set(s.SessionID, el); }}
    className={[
      "flex-1 min-h-[400px] border border-[var(--border-subtle)] bg-black p-2",
      s.SessionID === activeSessionId ? "" : "hidden",
    ].join(" ")}
  />
))}
```

- [ ] **Step 5: Implement new session and destroy handlers**

```typescript
const handleNewSession = async () => {
  if (!id || sessions.length >= 5) return;
  const created = await createSession(id);
  setSessions((prev) => [...prev, created]);
  setActiveSessionId(created.SessionID);
};

const handleDestroySession = async (sessionId: string) => {
  await destroySession(sessionId);
  const inst = terminalInstances.current.get(sessionId);
  if (inst) {
    inst.ws.close();
    inst.term.dispose();
    terminalInstances.current.delete(sessionId);
  }
  setSessions((prev) => {
    const next = prev.filter((s) => s.SessionID !== sessionId);
    if (activeSessionId === sessionId && next.length > 0) {
      setActiveSessionId(next[0].SessionID);
    }
    return next;
  });
};
```

- [ ] **Step 6: Add destroy confirmation dialog**

Reuse the same HTML `<dialog>` pattern from the sandboxes table:

```tsx
const [destroyTarget, setDestroyTarget] = useState<Session | null>(null);
const destroyDialogRef = useRef<HTMLDialogElement>(null);

const openDestroyDialog = (s: Session) => {
  setDestroyTarget(s);
  destroyDialogRef.current?.showModal();
};

// Dialog JSX — same pattern as Sandboxes.tsx destroy dialog
```

- [ ] **Step 7: Add loading state**

When `loading` is true, show an indicator:

```tsx
{loading && (
  <div className="flex-1 flex items-center justify-center text-sm text-[var(--fg-muted)]">
    Preparing session...
  </div>
)}
```

- [ ] **Step 8: Cleanup on unmount**

```typescript
useEffect(() => {
  return () => {
    terminalInstances.current.forEach(({ term, ws }) => {
      ws.close();
      term.dispose();
    });
    terminalInstances.current.clear();
  };
}, []);
```

- [ ] **Step 9: Add reconnection indicator**

When attaching to a session whose state was `detached`, flash "Reconnected" in the status bar:

```typescript
const [statusFlash, setStatusFlash] = useState<string | null>(null);

// When ws.onopen fires for a detached session:
const sessionState = sessions.find((s) => s.SessionID === activeSessionId)?.State;
if (sessionState === "detached") {
  setStatusFlash("Reconnected");
  setTimeout(() => setStatusFlash(null), 2000);
}
```

- [ ] **Step 10: Verify compilation**

Run: `cd /home/eran/work/navaris/web && npx tsc --noEmit`
Expected: PASS

- [ ] **Step 11: Manual smoke test**

1. Navigate to a running sandbox terminal
2. Verify first session auto-creates
3. Click `+` to add a second session
4. Switch tabs — both should retain scrollback
5. Close a tab with `x` — confirm dialog appears
6. Refresh page — should reconnect to existing sessions

- [ ] **Step 12: Commit**

```bash
git add web/src/routes/Terminal.tsx
git commit -m "feat(web): tabbed terminal with persistent tmux sessions"
```

---

### Task 11: Stable tab labels

Per the spec, tab labels should be stable — "Session 1" stays "Session 1" even if earlier sessions are destroyed. Assign labels based on the session's creation order from the full history, not just live sessions.

This is handled by using the session's index in the original sorted-by-`CreatedAt` list, not the filtered list. In the tab bar rendering from Task 10, change:

**Files:**
- Modify: `web/src/routes/Terminal.tsx` (the tab bar section from Task 10)

- [ ] **Step 1: Assign stable indices**

When sessions are first loaded, fetch ALL sessions (the `listSessions` endpoint returns all, including destroyed). Sort by `CreatedAt` and assign indices from the full list. Then filter to live sessions for display:

```typescript
const [sessionLabels, setSessionLabels] = useState<Map<string, number>>(new Map());

// In the effect that loads sessions:
const all = await listSessions(id);
// Assign stable labels from full history (including destroyed/exited).
const sorted = [...all].sort((a, b) => a.CreatedAt.localeCompare(b.CreatedAt));
const labels = new Map<string, number>();
sorted.forEach((s, i) => labels.set(s.SessionID, i + 1));
setSessionLabels(labels);

// Filter to live sessions for display.
const live = all.filter((s) => s.State !== "destroyed" && s.State !== "exited");
```

When a new session is created via the `+` button, assign the next label:

```typescript
setSessionLabels((prev) => {
  const next = new Map(prev);
  next.set(created.SessionID, prev.size + 1);
  return next;
});
```

Use `sessionLabels.get(s.SessionID) ?? "?"` in the tab rendering instead of `i + 1`.

- [ ] **Step 2: Verify it works**

Create 3 sessions, destroy session 2, verify labels show "Session 1" and "Session 3" (not "Session 1" and "Session 2").

- [ ] **Step 3: Commit**

```bash
git add web/src/routes/Terminal.tsx
git commit -m "feat(web): stable session tab labels"
```
