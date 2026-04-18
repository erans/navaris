# Navaris MCP Server

The navaris MCP server exposes sandbox/session/snapshot/image management as Model Context Protocol tools so AI agents (Claude Code, Cursor, Claude Desktop, etc.) can drive sandboxes directly.

Two transports are available:

- **stdio** — `navaris-mcp` binary, spawned by the MCP client as a subprocess
- **Streamable HTTP** — `/v1/mcp` endpoint embedded in navarisd, gated by the `--mcp-enabled` flag

Both transports expose the same tool set and read the same read-only toggle to hide every mutating tool.

## Tool catalog

| Tool | Read-only | Inputs |
|---|:-:|---|
| `project_list` | ✓ | — |
| `project_get` | ✓ | `project_id` |
| `sandbox_list` | ✓ | `project_id`, `state?` |
| `sandbox_get` | ✓ | `sandbox_id` |
| `sandbox_create` | | `project_id`, `image_id`, `name?`, `cpu?`, `memory_mb?`, `wait?`=true, `timeout_seconds?`=300 |
| `sandbox_start` | | `sandbox_id`, `wait?`=true, `timeout_seconds?`=120 |
| `sandbox_stop` | | `sandbox_id`, `force?`, `wait?`=true, `timeout_seconds?`=60 |
| `sandbox_destroy` | | `sandbox_id`, `wait?`=true, `timeout_seconds?`=60 |
| `sandbox_exec` | | `sandbox_id`, `command`, `env?`, `work_dir?`, `timeout_seconds?`=120 |
| `session_list` | ✓ | `sandbox_id` |
| `session_get` | ✓ | `session_id` |
| `session_create` | | `sandbox_id`, `shell?`, `backing?` |
| `session_destroy` | | `session_id` |
| `snapshot_list` | ✓ | `sandbox_id` |
| `snapshot_get` | ✓ | `snapshot_id` |
| `snapshot_create` | | `sandbox_id`, `label?`, `consistency_mode?`, `wait?`=true, `timeout_seconds?`=120 |
| `snapshot_restore` | | `snapshot_id`, `wait?`=true, `timeout_seconds?`=300 |
| `snapshot_delete` | | `snapshot_id`, `wait?`=true, `timeout_seconds?`=60 |
| `image_list` | ✓ | `name?`, `architecture?` |
| `image_get` | ✓ | `image_id` |
| `operation_get` | ✓ | `operation_id` |
| `operation_cancel` | | `operation_id` |

Mutating tools default to `wait=true` so the call returns the final resource. Pass `wait=false` to get an `operation_id` you can poll later via `operation_get`. If `timeout_seconds` elapses while the underlying operation is still running, the tool returns a `{operation_id, status: "running", note: ...}` payload (not an error) so the agent can decide to wait more or move on.

## Stdio (local agents)

The `navaris-mcp` binary reads its config from environment variables. Most MCP clients let you set these in the server config.

| Env | Required | Default | Purpose |
|---|:-:|---|---|
| `NAVARIS_API_URL` | ✓ | — | URL of navarisd (e.g. `http://localhost:8080`) |
| `NAVARIS_TOKEN` | conditional | — | Bearer token; required when navarisd has `--auth-token` set |
| `NAVARIS_MCP_READ_ONLY` | | `false` | `true` to hide every mutating tool |
| `NAVARIS_MCP_MAX_TIMEOUT` | | `600s` | Cap on per-tool `timeout_seconds` |
| `NAVARIS_MCP_LOG_LEVEL` | | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `NAVARIS_MCP_LOG_FORMAT` | | `text` | `json` for structured logs to stderr |

Logs go to stderr. Stdout is reserved for MCP protocol framing — any stray write there breaks the transport.

### Claude Code example

```json
{
  "mcpServers": {
    "navaris": {
      "command": "navaris-mcp",
      "env": {
        "NAVARIS_API_URL": "http://localhost:8080",
        "NAVARIS_TOKEN": "your-bearer-token"
      }
    }
  }
}
```

### Smoke test

1. Start navarisd: `navarisd --listen :8080 --auth-token your-token`
2. Restart Claude Code with the config above.
3. In a Claude Code conversation: "List my navaris projects." — Claude calls `project_list` and returns the result.
4. Try a write op: "Create a sandbox named 'test' in project X using image Y." — Claude calls `sandbox_create` with `wait=true` and returns the running sandbox.

## Embedded HTTP (remote/platform agents)

Enable the embedded MCP endpoint in navarisd:

```bash
navarisd \
  --listen :8080 \
  --auth-token your-token \
  --mcp-enabled \
  --mcp-read-only=false \
  --mcp-max-timeout 10m
```

| Flag | Default | Purpose |
|---|---|---|
| `--mcp-enabled` | `false` | Mount `/v1/mcp` |
| `--mcp-read-only` | `false` | Hide all mutating tools |
| `--mcp-path` | `/v1/mcp` | Endpoint path |
| `--mcp-max-timeout` | `10m` | Cap on per-tool `timeout_seconds` |

These flags have no environment-variable equivalents; they must be passed on the command line.

**Authentication:** bearer token only. The endpoint sits behind navarisd's existing auth middleware AND additionally rejects requests that lack a valid `Authorization: Bearer <token>` header — cookie auth (used by the web UI) is not accepted on `/v1/mcp`. Rejected requests receive a `401` response with a `WWW-Authenticate: Bearer realm="navaris-mcp"` header so clients can detect and handle the auth challenge.

The bearer token presented by the MCP client is passed through to the inner client used by tool handlers, so audit logs always reflect the caller's identity.

**Self-loopback note:** The embedded MCP handler calls navarisd as a client over the same listener; by default `--listen :8080` resolves to `127.0.0.1:8080` for outbound calls, which works in all standard deployments.

**Security note:** Running `--mcp-enabled` without `--auth-token` causes the `/v1/mcp` endpoint to accept unauthenticated requests, including all mutating tools. The daemon logs a warning at startup when this combination is detected. Only use it when the listener is already restricted to localhost in an isolated development environment.

## Read-only mode

When read-only mode is on (`--mcp-read-only` for the daemon, `NAVARIS_MCP_READ_ONLY=1` for the stdio binary):

- Mutating tools (`sandbox_create`, `sandbox_destroy`, `sandbox_exec`, `session_create`, etc.) are NOT registered.
- Agents see only read tools in `tools/list`.
- Calling a missing tool returns `method not found` from the MCP server.

This is enforced at registration time, so there is no per-call gate to forget.

## Limitations (v1)

- No streaming exec, no progress notifications.
- `sandbox_exec` does not accept stdin (deferred — requires changes across all providers).
- No `attach` or session-driving tool — agents use `sandbox_exec` for state-bearing work via tmux sessions they create themselves.
- No image upload, port forwarding, or events subscribe tools.
- The embedded endpoint requires bearer auth (cookie auth, used by the web UI, is rejected).
- `image_list` does not filter by project (the underlying API doesn't either).
- `snapshot_list` is sandbox-scoped (no project-wide listing).
