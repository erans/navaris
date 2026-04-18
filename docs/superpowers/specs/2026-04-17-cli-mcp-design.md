# CLI improvements & MCP server — design spec

**Date:** 2026-04-17
**Scope:** new `cmd/navaris-mcp` binary, new `internal/mcp` package, embedded MCP endpoint in `navarisd`, targeted improvements to existing `cmd/navaris` CLI
**Related:** existing `cmd/navaris` CLI, `internal/api/server.go` (route registration), `pkg/client` (HTTP client used by both CLI and MCP)

## Problem

Agents (Claude Code, Cursor, Claude Desktop, etc.) need a structured way to manage navaris sandboxes — list them, create them, run commands inside them, snapshot/restore them, and clean them up — without scraping CLI output or hand-rolling REST calls. The existing CLI is sufficient for humans and shell scripts, but agents are best served by an MCP server with typed tool schemas, structured errors, and configurable safety guards.

A small number of agent-friendly gaps in the existing CLI were also surfaced during MCP design and should be closed at the same time.

## Goals

1. **MCP server** that exposes navaris's sandbox/session/snapshot/image surface as MCP tools, available over both stdio (for local agents that spawn a subprocess) and Streamable HTTP (for remote/embedded agents).
2. **On/off configurability** at the navarisd level (`--mcp-enabled`) and a **read-only mode** (`--mcp-read-only`) that hides every mutating tool from the agent.
3. **Single canonical implementation** — both transports share one tool registry that calls navarisd via the existing `pkg/client.Client` HTTP client. No service-layer divergence between MCP and REST.
4. **Pass-through auth** — embedded MCP inherits the caller's bearer token for inner HTTP calls so audit trails are preserved end-to-end.
5. **Targeted CLI fixes** — close the small set of agent/script-friendly gaps in `cmd/navaris` that were surfaced during MCP design (exec stdin/env/timeout, `attach`, `wait-state`, `operation wait`, `--quiet`).

## Non-goals

- MCP `resources` or `prompts` — only `tools` for v1.
- Streaming exec or progress notifications — deferred until a concrete agent or human use case demands it.
- Tmux-driving / session-state tools (`session_run_command`) — agents use `sandbox_exec` and rely on conversational memory; stateful tmux driving is deferred.
- Per-tool allow/deny lists — server on/off plus read-only toggle are sufficient for v1.
- Per-project authorization for MCP — any token that can hit `/v1/*` can use the MCP. Project scoping happens at the API layer, not at MCP.
- New CLI binary — extending the existing `navaris` Cobra CLI in place.
- WebUI changes.

## Architecture

```
                   ┌─────────────────────────────────────┐
                   │  agents (Claude Code, Cursor, etc)  │
                   └──────────┬─────────────┬────────────┘
                              │             │
                       stdio MCP       Streamable HTTP MCP
                              │             │
                   ┌──────────▼─────┐    ┌──▼──────────────────────────┐
                   │ navaris-mcp    │    │  navarisd                   │
                   │  (new binary)  │    │  ┌────────────────────────┐ │
                   │                │    │  │ /v1/mcp endpoint       │◄┘
                   │  uses          │    │  │ (mcp.Server)           │
                   │  pkg/client    │    │  └─────────┬──────────────┘ │
                   └────────┬───────┘    │            │                 │
                            │            │  internal/mcp (shared)       │
                            │            │  ┌─────────▼──────────────┐ │
                            │            │  │ tool registry          │ │
                            │            │  └─────────┬──────────────┘ │
                            │            │            │                 │
                            ▼            │            ▼                 │
                       HTTP /v1/* ──────►│    pkg/client.Client         │
                                         │            │                 │
                                         │            ▼                 │
                                         │  existing /v1/* handlers     │
                                         └─────────────────────────────┘
```

Both transports go through `internal/mcp`, which holds the single registration of tools, their JSON schemas, descriptions, and handlers. Handlers do not call internal services directly — they call back through `pkg/client.Client` over HTTP. This keeps `internal/mcp` decoupled from the storage/provider layers and ensures the MCP can never bypass auth, validation, audit logging, or rate limiting that lives on the REST handlers.

The localhost HTTP roundtrip in embedded mode is acceptable for control-plane operations: sandbox lifecycle calls already wait seconds (Firecracker boot, Incus container creation), so a sub-millisecond loopback is dust.

### Module boundaries

- `internal/mcp` depends only on `pkg/client` and the MCP SDK. Independently testable with a fake client. Cannot drift from the HTTP API surface because it goes through it.
- `cmd/navaris-mcp` depends only on `internal/mcp` and `pkg/client`. Tiny entry point.
- `cmd/navarisd` gains a small block (~20 lines) to mount the embedded server when `--mcp-enabled`.
- `pkg/client` may need additive changes (Stdin/Env on ExecRequest, attach client) to support new CLI features. No breaking changes to the public client API.

## Components

### `internal/mcp/server.go`

Constructs an `*mcp.Server` configured with the navaris server identity (`"navaris"`, version), then calls `register.Register(server, opts)`. Returns the server. Used by both the stdio binary and the embedded HTTP mount.

### `internal/mcp/register.go`

```go
type Options struct {
    Client    *client.Client  // injected; talks to navarisd
    ReadOnly  bool            // when true, mutating tools are not registered
    MaxTimeout time.Duration  // upper bound on per-tool timeout_seconds (default 600s)
}

func Register(s *mcp.Server, opts Options) {
    registerProjectTools(s, opts)
    registerSandboxReadTools(s, opts)
    registerSessionReadTools(s, opts)
    registerSnapshotReadTools(s, opts)
    registerImageTools(s, opts)
    registerOperationReadTools(s, opts)

    if opts.ReadOnly {
        return
    }
    registerSandboxMutatingTools(s, opts)
    registerSessionMutatingTools(s, opts)
    registerSnapshotMutatingTools(s, opts)
    registerOperationMutatingTools(s, opts)
}
```

Read-only enforcement is structural: in read-only mode, mutating tools are simply never added to the server, so they don't appear in `tools/list` and calling them returns `method not found`. No per-call gate to forget.

### `internal/mcp/tools_*.go`

One file per resource. Each file defines the tool schemas and handlers for that resource. Pattern:

```go
type sandboxExecInput struct {
    SandboxID      string            `json:"sandbox_id"`
    Command        []string          `json:"command"`
    TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
    Stdin          string            `json:"stdin,omitempty"`
    Env            map[string]string `json:"env,omitempty"`
}

type sandboxExecOutput struct {
    Stdout   string `json:"stdout"`
    Stderr   string `json:"stderr"`
    ExitCode int    `json:"exit_code"`
}

func registerSandboxExec(s *mcp.Server, opts Options) {
    s.AddTool(mcp.Tool{
        Name:        "sandbox_exec",
        Description: descriptions["sandbox_exec"],
        InputSchema: schemaFor[sandboxExecInput](),
    }, handleSandboxExec(opts))
}
```

Tool descriptions live in `descriptions.md` and are loaded at init. This keeps the descriptions reviewable as prose without being inlined throughout the Go code.

### `internal/mcp/waiter.go`

Helper used by every mutating tool that defaults to `wait=true`. Signature:

```go
func waitForOperationAndFetch[T any](
    ctx context.Context, c *client.Client, op *client.Operation,
    timeout time.Duration, fetch func() (*T, error),
) (any, error) {
    if op.State == client.OpSucceeded { return fetch() }
    final, err := c.WaitForOperation(ctx, op.OperationID, &client.WaitOptions{Timeout: timeout})
    if err != nil {
        // timeout — return progress info, not error
        return map[string]any{
            "operation_id": op.OperationID,
            "status": "running",
            "note": "still in progress, poll operation_get",
        }, nil
    }
    if final.State == client.OpFailed {
        return nil, fmt.Errorf("operation failed: %s", final.ErrorText)
    }
    if final.State == client.OpCancelled {
        return nil, fmt.Errorf("operation cancelled")
    }
    return fetch()
}
```

When the per-tool timeout elapses but the operation is still running on the server, we return a non-error progress object so the agent can decide whether to wait more (re-call with longer timeout) or move on (poll later via `operation_get`). The underlying server-side operation keeps running — the timeout is purely client-side.

### `internal/mcp/auth.go`

For the embedded HTTP transport only. Extracts the bearer token from the inbound request's `Authorization` header (cookie auth is rejected on this endpoint — see "Embedded mount" below), then constructs a request-scoped `*client.Client` configured with that token pointed at `http://localhost:<navarisd-port>`. This per-request client is passed into the tool handlers via context so each tool's inner HTTP calls inherit the caller's identity.

For the stdio binary, the client is constructed once at startup from `NAVARIS_TOKEN` and shared across all requests (a stdio MCP server is single-tenant by definition).

### `cmd/navaris-mcp/main.go`

```go
func main() {
    apiURL := requireEnv("NAVARIS_API_URL")
    token := os.Getenv("NAVARIS_TOKEN")
    readOnly := envBool("NAVARIS_MCP_READ_ONLY")
    timeout := envDuration("NAVARIS_MCP_TIMEOUT", 600*time.Second)

    c, err := client.New(apiURL, client.WithToken(token))
    if err != nil { fatal(err) }

    s := mcp.NewServer("navaris", version)
    mcppkg.Register(s, mcppkg.Options{Client: c, ReadOnly: readOnly, MaxTimeout: timeout})
    if err := s.ServeStdio(context.Background()); err != nil { fatal(err) }
}
```

Tiny. All logic lives in `internal/mcp`.

### Embedded mount in `cmd/navarisd/main.go`

```go
if cfg.MCPEnabled {
    handler := internalmcp.NewHTTPHandler(internalmcp.Options{
        // Client is per-request, derived in auth.go from inbound token
        ReadOnly: cfg.MCPReadOnly,
        MaxTimeout: cfg.MCPMaxTimeout,
        LocalAPIURL: fmt.Sprintf("http://localhost%s", cfg.Listen),
    })
    mux.Handle(cfg.MCPPath, requireAuth(handler))
}
```

Sits behind the same auth middleware as `/v1/*` for token validation. Unlike other `/v1/*` routes, **the MCP endpoint requires bearer-token auth and does not accept session cookies** — agents are programmatic, not browser-based, and rejecting cookie auth keeps the loopback identity story simple (the inbound bearer token is the same value passed to the inner `pkg/client.Client`).

## Tool catalog

| Tool | Read-only | Inputs | Returns |
|---|:-:|---|---|
| `project_list` | ✓ | — | `[{project_id, name, ...}]` |
| `project_get` | ✓ | `project_id` | `{project_id, name, ...}` |
| `sandbox_list` | ✓ | `project_id`, `state?` | `[{sandbox_id, name, state, image_id, created_at, ...}]` |
| `sandbox_get` | ✓ | `sandbox_id` | `{sandbox_id, name, state, backend, ...}` |
| `sandbox_create` | | `project_id`, `image_id`, `name?`, `cpu?`, `memory_mb?`, `wait?`=true, `timeout_seconds?`=300 | sandbox object (or `{operation_id}` if `wait=false`) |
| `sandbox_start` | | `sandbox_id`, `wait?`=true, `timeout_seconds?`=120 | sandbox object |
| `sandbox_stop` | | `sandbox_id`, `force?`, `wait?`=true, `timeout_seconds?`=60 | sandbox object |
| `sandbox_destroy` | | `sandbox_id`, `wait?`=true, `timeout_seconds?`=60 | `{ok: true}` |
| `sandbox_exec` | | `sandbox_id`, `command`, `timeout_seconds?`=120, `stdin?`, `env?` | `{stdout, stderr, exit_code}` |
| `session_list` | ✓ | `sandbox_id` | `[{session_id, state, backing, ...}]` |
| `session_get` | ✓ | `session_id` | `{session_id, ...}` |
| `session_create` | | `sandbox_id`, `shell?`, `backing?` (`direct` or `tmux`) | `{session_id, ...}` |
| `session_destroy` | | `session_id` | `{ok: true}` |
| `snapshot_list` | ✓ | `sandbox_id?`, `project_id?` | `[{snapshot_id, ...}]` |
| `snapshot_get` | ✓ | `snapshot_id` | `{snapshot_id, ...}` |
| `snapshot_create` | | `sandbox_id`, `name?`, `wait?`=true, `timeout_seconds?`=120 | snapshot object |
| `snapshot_restore` | | `snapshot_id`, `wait?`=true, `timeout_seconds?`=300 | sandbox object |
| `snapshot_delete` | | `snapshot_id`, `wait?`=true, `timeout_seconds?`=60 | `{ok: true}` |
| `image_list` | ✓ | `project_id` | `[{image_id, name, version, state, ...}]` |
| `image_get` | ✓ | `image_id` | `{image_id, ...}` |
| `operation_get` | ✓ | `operation_id` | `{operation_id, type, state, error_text?, ...}` |
| `operation_cancel` | | `operation_id` | `{ok: true}` |

**Naming:** `<resource>_<verb>`. Server identifies as `navaris`, so MCP clients display these as `navaris/sandbox_list` etc. No image upload, port management, or events subscribe in v1.

## Configuration

### Embedded MCP (navarisd flags)

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--mcp-enabled` | `NAVARIS_MCP_ENABLED` | `false` | Mount the `/v1/mcp` endpoint |
| `--mcp-read-only` | `NAVARIS_MCP_READ_ONLY` | `false` | Hide all mutating tools |
| `--mcp-path` | `NAVARIS_MCP_PATH` | `/v1/mcp` | Endpoint path |
| `--mcp-max-timeout` | `NAVARIS_MCP_MAX_TIMEOUT` | `600s` | Cap on per-tool `timeout_seconds` |

### Stdio MCP (env only — MCP clients pass env vars in their config)

| Env | Required | Default | Purpose |
|---|:-:|---|---|
| `NAVARIS_API_URL` | ✓ | — | URL of navarisd |
| `NAVARIS_TOKEN` | conditional | — | Required when navarisd has auth enabled |
| `NAVARIS_MCP_READ_ONLY` | | `false` | Hide all mutating tools |
| `NAVARIS_MCP_MAX_TIMEOUT` | | `600s` | Cap on per-tool `timeout_seconds` |
| `NAVARIS_MCP_LOG_FORMAT` | | `text` | `json` for structured logs to stderr |

Example Claude Code config:

```json
{
  "mcpServers": {
    "navaris": {
      "command": "navaris-mcp",
      "env": {
        "NAVARIS_API_URL": "https://navaris.example.com",
        "NAVARIS_TOKEN": "..."
      }
    }
  }
}
```

## CLI improvements

Targeted additions to `cmd/navaris/`. No restructuring.

| Change | File | Detail |
|---|---|---|
| `sandbox exec` flags | `cmd/navaris/sandbox.go` | Add `--timeout <duration>`, `--stdin -` (pipe stdin from CLI stdin), `--env KEY=VAL` (repeatable) |
| `sandbox attach` (new) | `cmd/navaris/sandbox.go` | `navaris sandbox attach <sandbox-id> [--session <session-id>] [--shell bash] [--backing tmux\|direct]`. Wraps the existing `/v1/sandboxes/{id}/attach` WebSocket for terminal use from a TTY. Auto-creates a session if `--session` is not provided. SIGWINCH handling for resize. |
| `sandbox wait-state` (new) | `cmd/navaris/sandbox.go` | `navaris sandbox wait-state <sandbox-id> --state running [--timeout 60s]`. Polls `GetSandbox` until state matches or timeout. Exit 0 on hit, non-zero on timeout. |
| `operation wait` (new) | `cmd/navaris/operation.go` | `navaris operation wait <operation-id> [--timeout 5m]`. Promotes the existing `c.WaitForOperation` helper into a first-class subcommand. |
| `--quiet` global flag | `cmd/navaris/main.go`, `output.go` | With `--output json`: suppress non-data output to stdout (errors still go to stderr). With text mode for create/destroy/start/stop: print only the affected ID(s) one per line, friendly for `xargs`. |

### Server-side prerequisites

- `sandbox exec` stdin/env: verify (and add if missing) `Stdin string` and `Env map[string]string` on `client.ExecRequest` and the `POST /v1/sandboxes/{id}/exec` request struct in `internal/api/exec.go`.
- `sandbox attach` for the CLI: a new WebSocket attach client in `pkg/client` wired to `os.Stdin`/`os.Stdout`. The server endpoint already exists.
- `sandbox wait-state` and `operation wait`: pure client-side polling, no server changes.

## Errors and timeouts

| HTTP status | MCP tool result |
|---|---|
| 200/201/202 | Success |
| 400 | Tool error: `"invalid request: <body.error_message>"` |
| 401/403 | Tool error: `"authentication failed"` / `"forbidden"` |
| 404 | Tool error: `"<resource> not found: <id>"` |
| 409 | Tool error: `"conflict: <body.error_message>"` |
| 5xx | Tool error: `"server error: <body.error_message>"` |
| Operation `failed` | Tool error: `"operation failed: <op.error_text>"`, includes `operation_id` |
| Operation `cancelled` | Tool error: `"operation cancelled"`, includes `operation_id` |
| Per-tool timeout exceeded while op still running | **Not an error** — returns `{operation_id, status: "running", note: "still in progress, poll operation_get"}` so the agent can choose to wait more or move on |

Three timeout layers:
1. **Per-tool `timeout_seconds`** — agent-controlled; per-tool defaults shown in the catalog above.
2. **Server-side cap (`--mcp-max-timeout` / `NAVARIS_MCP_MAX_TIMEOUT`)** — upper bound on what the agent can request. Default 600s.
3. **HTTP client default timeout** — already exists in `pkg/client`; left as-is.

## Observability

- **Embedded MCP**: each tool call emits an OpenTelemetry span `mcp.tool.call` with attributes `tool.name`, `tool.read_only`, `caller.token_subject` (when extractable). Inherits the daemon's existing OTLP exporter — no new config.
- **Stdio MCP**: structured logs to stderr via `slog`; JSON handler when `NAVARIS_MCP_LOG_FORMAT=json`.
- **Audit**: every mutating tool call hits navarisd over HTTP, so the existing request-log middleware records caller, route, status, and any audit trail the API already produces. No separate MCP audit path.

## Testing

- `internal/mcp/*_test.go` — unit tests against a fake `pkg/client` interface. Cover input parsing, schema validation, error mapping, wait-then-fetch, and read-only filtering (assert exact tool list per mode).
- `internal/api/server_test.go` — integration: start navarisd with `--mcp-enabled`, hit `/v1/mcp` over HTTP with a real MCP client, exercise `tools/list` and at least one read tool plus one write tool end-to-end.
- `cmd/navaris-mcp/main_test.go` — smoke test: spawn the binary as a subprocess with stdio, send `initialize` and `tools/list`, assert tool count differs in read-only vs full mode.
- CLI changes get unit tests in `cmd/navaris/sandbox_test.go` and `cmd/navaris/operation_test.go` (matching the existing per-resource layout).
- Manual end-to-end: `docs/mcp.md` includes a step-by-step recipe to connect Claude Code to a local navarisd, verify tool discovery, and run a smoke flow (create → exec → destroy).

## File layout

**New:**

```
cmd/navaris-mcp/
  main.go
  main_test.go

internal/mcp/
  server.go
  register.go
  schemas.go
  descriptions.md
  auth.go
  waiter.go
  tools_project.go
  tools_sandbox.go
  tools_session.go
  tools_snapshot.go
  tools_image.go
  tools_operation.go
  *_test.go

docs/mcp.md
```

**Edited:**

```
cmd/navarisd/main.go              # add --mcp-* flags, mount embedded server
internal/api/server.go            # mount internal/mcp HTTP handler at --mcp-path
cmd/navaris/sandbox.go            # add exec --timeout/--stdin/--env, attach, wait-state
cmd/navaris/operation.go          # add wait subcommand
cmd/navaris/main.go               # add --quiet global flag
cmd/navaris/output.go             # honor --quiet in printResult
pkg/client/exec.go                # add Stdin, Env to ExecRequest if missing
pkg/client/attach.go              # NEW: WebSocket attach client (used by CLI)
internal/api/exec.go              # accept Stdin, Env if missing on server side
go.mod                            # add MCP SDK dependency
README.md                         # short pointer to docs/mcp.md
```

## Decisions deferred to plan stage

- **MCP SDK choice**: `github.com/modelcontextprotocol/go-sdk` (official) is the default. Fallback is `github.com/mark3labs/mcp-go`. Decision pending evaluation of stdio + Streamable HTTP support in the chosen version.
- **Streamable HTTP vs SSE transport**: target current MCP spec recommendation (Streamable HTTP). Confirm in the plan based on SDK support at implementation time.

## Out of scope (explicitly)

- MCP `resources` and `prompts`
- Streaming exec (`sandbox_exec_stream`) and progress notifications
- Tmux session-driving tool (`session_run_command`)
- Per-tool allow/deny configuration
- Per-project authorization at the MCP layer
- Image upload, port management, events subscribe tools
- New CLI binary (`navaris-agent-cli`) — extending the existing `navaris` Cobra CLI in place
- WebUI changes
- Rate limiting MCP-specifically
