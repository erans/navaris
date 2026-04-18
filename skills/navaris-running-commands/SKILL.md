---
name: navaris-running-commands
description: Use when the user wants to run commands inside a navaris sandbox — one-shot exec, persistent interactive sessions, or attaching a terminal. Covers env/workdir/timeout on exec, session lifecycle, attach over WebSocket, and picking between exec and session.
---

# Navaris CLI — Running Commands

Covers `sandbox exec`, sessions, and `sandbox attach`.

## Reference

### One-shot exec

```
navaris sandbox exec <sandbox-id> [flags] -- <command...>
```

| Flag | Purpose |
|---|---|
| `--env KEY=VAL` (repeatable) | Set environment variables |
| `--workdir <path>` | Working directory inside the sandbox |
| `--timeout <duration>` | Bound the request (e.g. `30s`, `5m`); 0 = no timeout |

Stdin is **not** forwarded in v1 — if you need to pipe input, write to a file inside the sandbox first (via exec), then run the consumer. Use `sandbox wait-state` before exec if you just started the sandbox.

### Sessions

Sessions are persistent shell sessions backed by either `direct` (raw pty) or `tmux`. Use tmux when you want reconnectability.

| Command | Flags |
|---|---|
| `navaris session create --sandbox <id>` | `--shell bash` (default), `--backing direct` (default) or `tmux` |
| `navaris session list --sandbox <id>` | — |
| `navaris session get <session-id>` | — |
| `navaris session destroy <session-id>` | — |

### Attach

```
navaris sandbox attach <sandbox-id> [flags]
```

| Flag | Purpose |
|---|---|
| `--session <id>` | Existing session to attach to (auto-creates one when omitted) |
| `--shell bash` | Shell for auto-created session |
| `--backing tmux` | Backing for auto-created session (`direct` or `tmux`; default `tmux` here) |

Exit with the shell's normal exit mechanism (e.g. `exit` or Ctrl-D). For tmux-backed sessions, detach without killing the session with the configured tmux detach key.

### Wait for state

```
navaris sandbox wait-state <sandbox-id> --state running [--timeout 60s] [--interval 500ms]
```

## Workflows

### 1. One-shot exec with env and workdir

```bash
navaris sandbox wait-state "$SANDBOX_ID" --state running --timeout 30s
navaris sandbox exec "$SANDBOX_ID" \
  --env FOO=bar --env BAZ=qux \
  --workdir /srv \
  --timeout 2m \
  -- ./run.sh
```

### 2. Persistent tmux-backed session, attach, detach cleanly

```bash
SESSION_ID=$(navaris session create --sandbox "$SANDBOX_ID" --backing tmux --output json | jq -r '.SessionID')
navaris sandbox attach "$SANDBOX_ID" --session "$SESSION_ID"
# work in the shell; detach via tmux prefix + d to keep the session alive
# reattach later with the same command
```

If you want a throwaway attached shell, skip `session create` and just run `navaris sandbox attach "$SANDBOX_ID"` — it auto-creates a tmux-backed session.

### 3. Wait for running, then exec

```bash
SANDBOX_ID=$(navaris sandbox create --name build-$$ --image alpine/3.21 --wait --output json | jq -r '.SandboxID')
navaris sandbox wait-state "$SANDBOX_ID" --state running --timeout 60s
navaris sandbox exec "$SANDBOX_ID" -- apk add --no-cache curl
```

Skipping `wait-state` after `create` can produce errors because create returns as soon as the operation is queued (unless `--wait` was passed).

### 4. Exec vs session — pick the right tool

| You need… | Use |
|---|---|
| Stateless, short command (seconds) | `sandbox exec` |
| Long-running process you want to monitor | `sandbox exec --timeout <long-duration>` |
| Multiple related commands with shared env/cwd | `session create --backing tmux` + `sandbox attach` |
| Interactive shell | `sandbox attach` (auto-creates a tmux-backed session) |
| Need to reconnect after a client drop | `session create --backing tmux` (not `direct`) |

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `api error 404: session: not found` | Session was destroyed, or the sandbox was restarted and `direct`-backed sessions were dropped | Recreate with `session create`; use `--backing tmux` to survive client disconnects |
| Attach WebSocket drops mid-session | Daemon restart or network blip | Re-run `navaris sandbox attach`; tmux-backed sessions preserve state |
| `context deadline exceeded` on exec | `--timeout` elapsed; the process may still be running server-side | Check with `navaris sandbox exec <id> -- ps aux` or wait and retry; do not assume the command was cancelled |
| `api error 422: sandbox must be running to create session` | `session create` called before the sandbox reached running state | Insert `navaris sandbox wait-state <id> --state running` before `session create` |
| `dial attach: unexpected HTTP status code 401` | `NAVARIS_TOKEN` missing or wrong for the attach WebSocket handshake | Re-check `NAVARIS_TOKEN`; run `navaris project list` to verify auth works |
