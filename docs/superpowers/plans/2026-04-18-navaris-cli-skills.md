# Navaris CLI Skill Pack — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Claude Code plugin inside the navaris repo so users can `/plugin install navaris-cli@erans/navaris` and then ask Claude to drive the `navaris` CLI for them — a router skill plus four task skills covering getting-started, sandboxes, exec/sessions, and snapshots/images/async.

**Architecture:** A `.claude-plugin/` directory holds the plugin manifest (`plugin.json`) and marketplace entry (`marketplace.json`). Five SKILL.md files live under `skills/<name>/SKILL.md`. A bash drift detector walks every SKILL.md for `navaris <group> <subverb>` references and runs `navaris <cmd> --help` against the freshly built CLI; CI gates on it. User-facing docs live in `docs/claude-skills.md`, linked from README.

**Tech Stack:** Markdown (SKILL content), YAML frontmatter, JSON (manifests), Bash (drift detector), GitHub Actions (CI), Go 1.26 (build CLI for drift check).

**Spec:** [docs/superpowers/specs/2026-04-18-navaris-cli-skills-design.md](../specs/2026-04-18-navaris-cli-skills-design.md)

---

## Spec adjustments discovered during exploration

These are deviations from the spec the engineer should be aware of before starting:

1. **`sandbox create` requires a project**. It reads from `--project` flag or `NAVARIS_PROJECT` env var (see `cmd/navaris/config.go:38`). The getting-started skill must include this in its first-sandbox workflow.
2. **`session create` default `--backing` is `direct`, not `tmux`** (see `cmd/navaris/session.go:108`). The `sandbox attach` command defaults to `--backing tmux` when auto-creating a session (see `cmd/navaris/sandbox.go:55`). Workflows must specify `--backing tmux` explicitly when using `session create` for persistence.
3. **Global flags live on `rootCmd`** (see `cmd/navaris/main.go`): `--api-url`, `--token`, `--project`, `--output text|json`, `--quiet`/`-q`. These are persistent; every subcommand accepts them.
4. **`--quiet` on `sandbox list`** does not strip the table — it only affects commands that produce operations or JSON output. For bulk scripting, use `--output json | jq -r '.[].SandboxID'` instead (JSON field names are capitalized Pascal, e.g. `SandboxID`, `SessionID`, `SnapshotID`, `ProjectID`).
5. **`image promote` takes `--snapshot`, `--name`, `--version`** — not a positional snapshot ID. `image register` takes `--name`, `--version`, `--backend`, `--backend-ref` (see `cmd/navaris/image.go:130-141`).
6. **`snapshot create --consistency`** (not `--consistency-mode`) with values `stopped` (default) or `live` (see `cmd/navaris/snapshot.go:121`).

---

## Milestones

- **M1** — Plugin scaffolding (manifest + marketplace entry) — 1 task
- **M2** — Router skill: `using-navaris-cli` — 1 task
- **M3** — Task skill: `navaris-getting-started` — 1 task
- **M4** — Task skill: `navaris-managing-sandboxes` — 1 task
- **M5** — Task skill: `navaris-running-commands` — 1 task
- **M6** — Task skill: `navaris-snapshots-images-async` — 1 task
- **M7** — Drift detector script + tests — 1 task
- **M8** — CI workflow for drift detector — 1 task
- **M9** — User docs + README section — 1 task
- **M10** — End-to-end smoke verification (manual) — 1 task

---

## Conventions

- All SKILL.md files use YAML frontmatter with `name` and `description` only (other fields ignored by Claude Code skill loader).
- The `name` field MUST equal the directory name (e.g. `using-navaris-cli`).
- Plan tasks use strict TDD for the drift detector. Markdown content tasks verify by: (a) frontmatter parses, (b) drift detector passes, (c) diff against spec §3/§4.
- Commit messages follow the convention from recent repo history: `feat(skills):`, `docs:`, `chore:`, `test:`.
- After each milestone, run the drift detector (`./scripts/skill-drift-check.sh`) once it exists; before it exists, verify manually.

---

## M1 — Plugin scaffolding

### Task 1.1: Plugin manifest + marketplace entry

**Files:**
- Create: `.claude-plugin/plugin.json`
- Create: `.claude-plugin/marketplace.json`

- [ ] **Step 1: Create the plugin manifest**

Create `.claude-plugin/plugin.json`:

```json
{
  "name": "navaris-cli",
  "description": "Teach Claude Code how to drive the navaris CLI: create sandboxes, exec commands, attach sessions, manage snapshots and images",
  "version": "0.1.0",
  "author": {
    "name": "Eran Sandler",
    "email": "eran@sandler.co.il"
  },
  "homepage": "https://github.com/erans/navaris",
  "repository": "https://github.com/erans/navaris",
  "license": "Apache-2.0",
  "keywords": [
    "navaris",
    "sandbox",
    "cli",
    "incus",
    "firecracker",
    "claude-code"
  ]
}
```

- [ ] **Step 2: Create the marketplace entry**

Create `.claude-plugin/marketplace.json`:

```json
{
  "name": "navaris",
  "description": "Claude Code plugins for the navaris sandbox control plane",
  "owner": {
    "name": "Eran Sandler",
    "email": "eran@sandler.co.il"
  },
  "plugins": [
    {
      "name": "navaris-cli",
      "description": "Teach Claude Code how to drive the navaris CLI: create sandboxes, exec commands, attach sessions, manage snapshots and images",
      "version": "0.1.0",
      "source": "./",
      "author": {
        "name": "Eran Sandler",
        "email": "eran@sandler.co.il"
      }
    }
  ]
}
```

- [ ] **Step 3: Verify both files are valid JSON**

Run:
```bash
jq . .claude-plugin/plugin.json >/dev/null && jq . .claude-plugin/marketplace.json >/dev/null && echo OK
```
Expected: `OK`

- [ ] **Step 4: Commit**

```bash
git add .claude-plugin/plugin.json .claude-plugin/marketplace.json
git commit -m "feat(skills): add plugin manifest and marketplace entry for navaris-cli"
```

---

## M2 — Router skill: `using-navaris-cli`

### Task 2.1: Router SKILL.md

**Files:**
- Create: `skills/using-navaris-cli/SKILL.md`

- [ ] **Step 1: Write the router skill**

Create `skills/using-navaris-cli/SKILL.md`:

````markdown
---
name: using-navaris-cli
description: Use when the user asks Claude to do anything with navaris sandboxes via the CLI — create/start/stop sandboxes, exec commands, snapshot, attach, manage projects. Establishes environment, picks the right task skill, and points autonomous-agent use cases at the MCP server.
---

# Using Navaris CLI

Navaris is a sandbox control plane with two backends (Incus containers, Firecracker microVMs) and one CLI (`navaris`). This skill pack teaches Claude Code how to drive that CLI on a user's behalf.

**Announce at start:** "I'm using the using-navaris-cli skill to drive navaris."

## MCP fork

If the user is driving navaris from an autonomous agent loop with no human in the chair, **prefer the MCP server** instead. See `docs/mcp.md` in the navaris repo. This pack is for human-in-the-loop CLI work in Claude Code — it goes through Bash, one command at a time, with user approval on destructive steps.

## Environment check (always run before any CLI command)

1. **`NAVARIS_API_URL`** — required. If unset, ask the user. Common default: `http://localhost:8080`.
2. **`NAVARIS_TOKEN`** — required when the daemon was started with `--auth-token`.
3. **`NAVARIS_PROJECT`** — optional, but highly recommended to set once so sandbox commands don't need `--project` on every call.
4. **One-shot health check:** `navaris project list --quiet --output json >/dev/null`. Classify failure:
   - `command not found` → CLI not installed → route to `navaris-getting-started`.
   - HTTP 401 → token missing or wrong → route to `navaris-getting-started`.
   - `connection refused` / `dial tcp` → daemon down → surface to user before proceeding.

## Routing table

Match the user's intent to one of the task skills and invoke it via the Skill tool:

| User intent | Skill |
|---|---|
| Install CLI, set up env, "where do I start" | `navaris-getting-started` |
| Create/list/start/stop/destroy a sandbox; manage projects/ports; pick a backend | `navaris-managing-sandboxes` |
| Run a command in a sandbox; attach a terminal; manage sessions | `navaris-running-commands` |
| Snapshot, restore, promote to image, wait on async operations | `navaris-snapshots-images-async` |

If the user's question spans two skills, start with the one that matches the verb they used (e.g. "snapshot the sandbox and then destroy it" → `navaris-snapshots-images-async` first).

## Quiet mode for scripting

Pass `--quiet` (`-q`) on operation commands so they print only the resulting ID — useful for `xargs`, shell variables, and scripted pipelines. For list commands, use `--output json | jq -r '.[].SandboxID'` (or the corresponding Pascal-case ID field: `SessionID`, `SnapshotID`, `ProjectID`, `ImageID`) since `--quiet` does not strip the table on lists.

## Red flags

- **Never** pass `--auth-token` directly to `navarisd` in a shared shell history. Use an env file or a secret manager.
- **Never** run `sandbox destroy` without confirming the sandbox ID with the user first.
- **Never** assume the daemon is up — always run the health check above.
- **Never** edit daemon config (KVM, jailer, OTLP flags) from this skill pack — that's an operator concern, out of scope here.
- **Never** fabricate command flags. Run `navaris <cmd> --help` if unsure.

## When to escalate

If the user asks for something not covered by the four task skills (e.g. web UI config, daemon operator tasks, SDK usage) say so and point them at `README.md` and `docs/mcp.md`.
````

- [ ] **Step 2: Verify frontmatter parses**

Run:
```bash
awk '/^---$/{f++;next} f==1{print}' skills/using-navaris-cli/SKILL.md | head -5
```
Expected: exactly 2 lines of frontmatter (`name:` and `description:`), no third section before the first content line.

- [ ] **Step 3: Commit**

```bash
git add skills/using-navaris-cli/SKILL.md
git commit -m "feat(skills): add using-navaris-cli router skill"
```

---

## M3 — Task skill: `navaris-getting-started`

### Task 3.1: Getting-started SKILL.md

**Files:**
- Create: `skills/navaris-getting-started/SKILL.md`

- [ ] **Step 1: Write the skill**

Create `skills/navaris-getting-started/SKILL.md`:

````markdown
---
name: navaris-getting-started
description: Use when a user is setting up the navaris CLI for the first time, connecting to an existing navarisd, or hitting "how do I start" questions. Covers install, env vars, health check, first project, first sandbox, and common startup errors (401, connection refused, DNS).
---

# Navaris CLI — Getting Started

Gets a user from zero to "I just exec'd something in a sandbox" with minimum friction.

## Reference

### Install

From source (requires Go 1.26+):
```bash
go build -o navaris ./cmd/navaris
sudo mv navaris /usr/local/bin/       # optional; keep local if preferred
```

Or download a release binary from the navaris releases page and put it on `$PATH`.

### Required env vars

| Env var | Required | Purpose |
|---|---|---|
| `NAVARIS_API_URL` | yes | URL of navarisd, e.g. `http://localhost:8080` |
| `NAVARIS_TOKEN` | yes if daemon uses `--auth-token` | Bearer token for auth |
| `NAVARIS_PROJECT` | recommended | Default project ID so `--project` isn't needed every call |

All three are also available as CLI flags: `--api-url`, `--token`, `--project`.

### Health check

```bash
navaris project list --quiet --output json
```

Exit 0 = CLI installed, daemon reachable, auth accepted. Non-zero = see errors below.

`sandbox create` is asynchronous — it returns immediately with an operation; pass `--wait` to block until provisioning completes and receive the sandbox row instead.

## Workflows

### 1. First-time setup from scratch (daemon + CLI on localhost)

1. Start `navarisd` (see `README.md` for daemon flags and the all-in-one Docker option) and capture the auth token it was started with.
2. Export the env vars in the CLI terminal:
   ```bash
   export NAVARIS_API_URL=http://localhost:8080
   export NAVARIS_TOKEN=<token-from-step-1>
   ```
3. Run the health check:
   ```bash
   navaris project list --quiet --output json
   ```
   Expect `[]` on a fresh daemon.

### 2. Connect to an existing remote navarisd

1. Ask the operator for the base URL and token (or pull them from your secret manager).
2. Export env vars:
   ```bash
   export NAVARIS_API_URL=https://navaris.example.com
   export NAVARIS_TOKEN=<token>
   ```
3. Run the health check.

### 3. First project → first sandbox → exec → destroy

1. Create a project and capture its ID:
   ```bash
   PROJECT_ID=$(navaris project create --name playground --output json | jq -r '.ProjectID')
   export NAVARIS_PROJECT="$PROJECT_ID"
   ```
2. Create a sandbox (Incus by default; image reference with `/` routes to Incus).
   `--wait` blocks until the create operation finishes so the JSON we capture is
   the sandbox row, not the still-pending operation:
   ```bash
   SANDBOX_ID=$(navaris sandbox create --name hello --image alpine/3.21 --wait --output json | jq -r '.SandboxID')
   ```
3. Belt-and-suspenders: wait until the sandbox is actually running:
   ```bash
   navaris sandbox wait-state "$SANDBOX_ID" --state running --timeout 60s
   ```
4. Exec a command:
   ```bash
   navaris sandbox exec "$SANDBOX_ID" -- echo "hello from the sandbox"
   ```
5. Destroy it:
   ```bash
   navaris sandbox destroy "$SANDBOX_ID" --wait
   ```

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `api error 401` (or `HTTP 401`) | `NAVARIS_TOKEN` missing or wrong | Re-export `NAVARIS_TOKEN` with the value the daemon was started with |
| `connection refused` | Daemon not running on the configured host/port | Start `navarisd` or point `NAVARIS_API_URL` at the right host |
| `dial tcp: no such host` | DNS miss or typo in `NAVARIS_API_URL` | Fix the URL |
| `--project flag or NAVARIS_PROJECT env var is required` | `sandbox create` invoked without a project | Either pass `--project <id>` or `export NAVARIS_PROJECT=<id>` |
````

- [ ] **Step 2: Verify frontmatter parses**

Run:
```bash
awk '/^---$/{f++;next} f==1{print}' skills/navaris-getting-started/SKILL.md | head -5
```
Expected: `name: navaris-getting-started` on one line, `description:` on the next.

- [ ] **Step 3: Commit**

```bash
git add skills/navaris-getting-started/SKILL.md
git commit -m "feat(skills): add navaris-getting-started task skill"
```

---

## M4 — Task skill: `navaris-managing-sandboxes`

### Task 4.1: Managing-sandboxes SKILL.md

**Files:**
- Create: `skills/navaris-managing-sandboxes/SKILL.md`

- [ ] **Step 1: Write the skill**

Create `skills/navaris-managing-sandboxes/SKILL.md`:

````markdown
---
name: navaris-managing-sandboxes
description: Use when the user wants to create, list, start, stop, or destroy navaris sandboxes, manage projects, publish ports, or decide between the Incus and Firecracker backends. Covers the full sandbox lifecycle plus project/port CRUD.
---

# Navaris CLI — Managing Sandboxes & Projects

Covers projects, sandbox lifecycle, port forwarding, and backend selection.

## Reference

### Projects

| Command | Purpose |
|---|---|
| `navaris project create --name <n>` | Create a project |
| `navaris project list` | List projects |
| `navaris project get <project-id>` | Get one |
| `navaris project update <project-id> --name <n>` | Rename |
| `navaris project delete <project-id>` | Delete |

### Sandboxes

| Command | Key flags |
|---|---|
| `navaris sandbox create` | `--name`, `--image` (required), `--cpu`, `--memory`, `--project` (or `NAVARIS_PROJECT`), `--wait`, `--timeout` |
| `navaris sandbox list` | (filters via `--project`) |
| `navaris sandbox get <sandbox-id>` | — |
| `navaris sandbox start <sandbox-id>` | `--wait`, `--timeout` |
| `navaris sandbox stop <sandbox-id>` | `--force`, `--wait`, `--timeout` |
| `navaris sandbox destroy <sandbox-id>` | `--wait`, `--timeout` |

Use `-q` / `--quiet` on create/start/stop/destroy to print only the resulting ID (good for scripting).

`sandbox create` and `sandbox list` both require `--project` or `NAVARIS_PROJECT`.

### Ports

| Command | Purpose |
|---|---|
| `navaris port create --sandbox <id> --port <target-port>` | Publish a sandbox port to the host (published host port is daemon-assigned; see `HOST_ADDRESS`/`PUBLISHED_PORT` in the response) |
| `navaris port list --sandbox <id>` | List published ports |
| `navaris port delete --sandbox <id> <target-port>` | Unpublish |

### Backend selection

Auto-detected from the image reference:

- Slash-style (`alpine/3.21`, `ubuntu/22.04`) → **Incus** container
- Flat-style (`alpine-3.21`, `debian-12`) → **Firecracker** microVM

Override with `--backend incus` or `--backend firecracker` on `sandbox create` if you have a reason to pin.

## Workflows

### 1. Ephemeral dev sandbox on Incus (fast, throwaway)

Best when you want quick iteration and don't need hardware-level isolation.

```bash
SANDBOX_ID=$(navaris sandbox create \
  --name dev-$$ \
  --image alpine/3.21 \
  --cpu 2 --memory 1024 \
  --wait --output json | jq -r '.SandboxID')
navaris sandbox wait-state "$SANDBOX_ID" --state running --timeout 60s
# ... use it ...
navaris sandbox destroy "$SANDBOX_ID" --wait
```

### 2. Isolated untrusted workload on Firecracker (hardware isolation)

Best for running user-submitted code, multi-tenant workloads, or anything security-sensitive.

```bash
SANDBOX_ID=$(navaris sandbox create \
  --name scan-$$ \
  --image alpine-3.21 \
  --cpu 1 --memory 512 \
  --wait --output json | jq -r '.SandboxID')
navaris sandbox wait-state "$SANDBOX_ID" --state running --timeout 120s
navaris sandbox exec "$SANDBOX_ID" -- ./scan.sh
navaris sandbox destroy "$SANDBOX_ID" --wait
```

Firecracker startup is a bit slower than Incus — allow ~2-3s plus image load time.

### 3. Publish a port and verify it's reachable

```bash
navaris sandbox exec "$SANDBOX_ID" -- sh -c 'nc -l -p 8000 &'
navaris port create --sandbox "$SANDBOX_ID" --port 8000 --output json
# response contains HOST_ADDRESS and PUBLISHED_PORT — capture them for curl:
PUBLISHED=$(navaris port list --sandbox "$SANDBOX_ID" --output json | jq -r '.[] | select(.TargetPort==8000) | "\(.HostAddress):\(.PublishedPort)"')
curl -fsS "http://$PUBLISHED/"
```

To tear down: `navaris port delete --sandbox "$SANDBOX_ID" 8000`.

### 4. Bulk-destroy all sandboxes in a project

```bash
navaris sandbox list --project "$NAVARIS_PROJECT" --output json \
  | jq -r '.[].SandboxID' \
  | xargs -n1 -I{} navaris sandbox destroy {} --quiet
```

Each line is the destroy operation ID, not the sandbox ID. Add `--wait` to each invocation if you need the loop to block until every sandbox is actually gone (slower but safer).

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `operation ... failed: firecracker copy rootfs ...: open ...: no such file or directory` (Firecracker) or `operation ... failed: incus create instance: ...` (Incus) | Image ref typo, image not registered for that backend, or image present in a different store | `navaris image list --name <partial>` to find the right ref; for Incus use slash-style (`alpine/3.21`), for Firecracker use flat-style (`alpine-3.21`); register a missing image with `navaris image register` |
| `api error 500: internal server error` after `sandbox create` | Daemon doesn't have the requested backend enabled — common causes: Firecracker requested but `/dev/kvm` unavailable; Incus requested but daemon started without the Incus socket; rootfs/image directory not mounted | Check daemon startup logs (`provider "<name>" not available`); restart `navarisd` with the right backend flags (see `README.md`); pick an image that routes to a supported backend |
| `operation <id> failed: ...` after destroy | Backend rejected the destroy (e.g. provider error, transient I/O) | `navaris operation get <op-id>` to read the wrapped error text; retry or escalate based on the underlying message |
| `--project flag or NAVARIS_PROJECT env var is required` | `sandbox create` or `sandbox list` invoked without a project | Set `NAVARIS_PROJECT` in the environment, or pass `--project <id>` |
````

- [ ] **Step 2: Verify frontmatter parses**

Run:
```bash
awk '/^---$/{f++;next} f==1{print}' skills/navaris-managing-sandboxes/SKILL.md | head -5
```
Expected: frontmatter has `name: navaris-managing-sandboxes` and a `description:` line.

- [ ] **Step 3: Commit**

```bash
git add skills/navaris-managing-sandboxes/SKILL.md
git commit -m "feat(skills): add navaris-managing-sandboxes task skill"
```

---

## M5 — Task skill: `navaris-running-commands`

### Task 5.1: Running-commands SKILL.md

**Files:**
- Create: `skills/navaris-running-commands/SKILL.md`

- [ ] **Step 1: Write the skill**

Create `skills/navaris-running-commands/SKILL.md`:

````markdown
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
````

- [ ] **Step 2: Verify frontmatter parses**

Run:
```bash
awk '/^---$/{f++;next} f==1{print}' skills/navaris-running-commands/SKILL.md | head -5
```
Expected: frontmatter has `name: navaris-running-commands` and a `description:` line.

- [ ] **Step 3: Commit**

```bash
git add skills/navaris-running-commands/SKILL.md
git commit -m "feat(skills): add navaris-running-commands task skill"
```

---

## M6 — Task skill: `navaris-snapshots-images-async`

### Task 6.1: Snapshots-images-async SKILL.md

**Files:**
- Create: `skills/navaris-snapshots-images-async/SKILL.md`

- [ ] **Step 1: Write the skill**

Create `skills/navaris-snapshots-images-async/SKILL.md`:

````markdown
---
name: navaris-snapshots-images-async
description: Use when the user wants to snapshot a sandbox, restore from a snapshot, promote a snapshot to a reusable base image, register an external image, or manage long-running async operations (wait-state, operation wait/get/cancel). Covers stopped vs live snapshots and the async operation lifecycle.
---

# Navaris CLI — Snapshots, Images & Async Operations

Covers snapshots, images, and the async operation surface that underpins every long-running CLI call.

## Reference

### Snapshots

| Command | Flags |
|---|---|
| `navaris snapshot create --sandbox <sandbox-id> --label <name>` | `--consistency stopped` (default) or `live`; `--wait`, `--timeout` |
| `navaris snapshot list --sandbox <id>` | — |
| `navaris snapshot get <snapshot-id>` | — |
| `navaris snapshot restore <snapshot-id>` | `--wait`, `--timeout` |
| `navaris snapshot delete <snapshot-id>` | `--wait`, `--timeout` |

`stopped` consistency requires the sandbox to be stopped; it's the safest and most universally supported. `live` captures a running sandbox (memory + disk) and only works on backends that support it.

### Images

| Command | Flags |
|---|---|
| `navaris image promote --snapshot <snapshot-id> --name <n> --version <v>` | Promote a snapshot into a reusable image |
| `navaris image register --name <n> --version <v> --backend <type> --backend-ref <ref>` | Register a pre-existing external image (rootfs/kernel) |
| `navaris image list [--name <partial>] [--architecture <arch>]` | List images |
| `navaris image get <image-id>` | — |
| `navaris image delete <image-id>` | — |

### Operations

Every long-running call (create, start, stop, destroy, snapshot, restore) produces an operation. The CLI usually waits for it to finish (controlled by `--wait` / `--timeout` on each command); pass `--wait=false` to return immediately with the operation ID for later polling.

| Command | Flags |
|---|---|
| `navaris operation list` | `--sandbox <id>`, `--state <state>` (filter) |
| `navaris operation get <operation-id>` | — |
| `navaris operation wait <operation-id>` | `--timeout <duration>` |
| `navaris operation cancel <operation-id>` | — |

### Sandbox wait-state

```
navaris sandbox wait-state <sandbox-id> --state running [--timeout 60s] [--interval 500ms]
```

## Workflows

### 1. Safety net: snapshot → upgrade → restore on failure

```bash
SNAP_ID=$(navaris snapshot create --sandbox "$SANDBOX_ID" --label pre-upgrade --wait --output json | jq -r '.SnapshotID')
if ! navaris sandbox exec "$SANDBOX_ID" -- ./upgrade.sh; then
    echo "upgrade failed, restoring..." >&2
    navaris snapshot restore "$SNAP_ID" --wait
fi
```

### 2. Promote a working snapshot to a reusable base image

```bash
# after verifying the sandbox is in a good state
SNAP_ID=$(navaris snapshot create --sandbox "$SANDBOX_ID" --label base-candidate --wait --output json | jq -r '.SnapshotID')
IMAGE_ID=$(navaris image promote \
    --snapshot "$SNAP_ID" \
    --name my-service-base \
    --version 2026.04 \
    --wait --output json | jq -r '.ImageID')
# new sandboxes can now use --image my-service-base
```

### 3. Register an externally built rootfs/kernel as a Firecracker image

```bash
navaris image register \
  --name debian-minimal \
  --version 12.5 \
  --backend firecracker \
  --backend-ref /var/lib/firecracker/images/debian-12.5.rootfs
```

Use this when you build rootfs images yourself (outside navaris) and want them addressable by image reference.

### 4. Fire-and-forget async with polling and timeout

```bash
OP_ID=$(navaris sandbox create \
    --name long-boot \
    --image alpine-3.21 \
    --wait=false --output json | jq -r '.OperationID')
# do other work...
if ! navaris operation wait "$OP_ID" --timeout 5m; then
    echo "operation did not finish in 5m, cancelling" >&2
    navaris operation cancel "$OP_ID"
fi
```

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `live snapshot not supported` | Backend doesn't support live capture (or sandbox is mid-state) | Stop the sandbox and pass `--consistency stopped`, or use a backend that supports live |
| `image delete` fails with "image in use" | Sandboxes still derive from this image | `navaris sandbox list --output json \| jq '.[] \| select(.SourceImageID=="<id>")'` to find users; destroy/migrate them first |
| `operation stuck in pending` | Worker backlog or dependency waiting | `navaris operation get <id>` to inspect; `navaris operation list --state running` to see what's busy |
| `snapshot restore` times out | Restore is longer than `--timeout` | Pass `--wait=false` and poll with `operation wait` at a longer timeout |
| Promoted image has wrong architecture | `image register` / `promote` default architecture didn't match the source | Inspect with `navaris image get <id>` and re-register with the correct `--architecture` if supported on your navaris version |
````

- [ ] **Step 2: Verify frontmatter parses**

Run:
```bash
awk '/^---$/{f++;next} f==1{print}' skills/navaris-snapshots-images-async/SKILL.md | head -5
```
Expected: frontmatter has `name: navaris-snapshots-images-async` and a `description:` line.

- [ ] **Step 3: Commit**

```bash
git add skills/navaris-snapshots-images-async/SKILL.md
git commit -m "feat(skills): add navaris-snapshots-images-async task skill"
```

---

## M7 — Drift detector script + tests

The drift detector scans every SKILL.md file in `skills/` for `navaris <group> <subverb>` command references (where `<group>` is one of the 7 known top-level groups) and runs `navaris <cmd> --help` against the freshly built CLI. It exits non-zero if any referenced command does not accept `--help`, meaning the skill drifted from the real CLI.

### Task 7.1: Drift detector script with TDD

**Files:**
- Create: `scripts/skill-drift-check.sh`
- Create: `scripts/skill-drift-check_test.sh`
- Create: `scripts/testdata/bad-skill/SKILL.md` (golden "bad" fixture used by the test)

- [ ] **Step 1: Write the failing test**

Create `scripts/skill-drift-check_test.sh`:

```bash
#!/usr/bin/env bash
# Tests for skill-drift-check.sh.
#
# Strategy: build the CLI once, then run the detector against:
#   1. The real skills/ directory — expect exit 0.
#   2. A temp directory with the golden "bad" SKILL.md — expect non-zero.
#
# Runs from the repo root.

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

SCRIPT=scripts/skill-drift-check.sh
if [ ! -x "$SCRIPT" ]; then
    echo "FAIL: $SCRIPT does not exist or is not executable"
    exit 1
fi

echo "building navaris CLI for drift test..."
go build -o ./navaris-drift-test ./cmd/navaris
export NAVARIS="$PWD/navaris-drift-test"
trap 'rm -f "$PWD/navaris-drift-test"' EXIT

# 1. Real skills/ should pass.
if ! "$SCRIPT" skills >/dev/null 2>&1; then
    echo "FAIL: drift check reports drift on the real skills/ directory"
    exit 1
fi
echo "OK: real skills/ passes drift check"

# 2. Golden bad fixture should fail.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"; rm -f "$PWD/navaris-drift-test"' EXIT
mkdir -p "$tmp/bad-skill"
cp scripts/testdata/bad-skill/SKILL.md "$tmp/bad-skill/SKILL.md"
if "$SCRIPT" "$tmp" >/dev/null 2>&1; then
    echo "FAIL: drift check passed on the golden bad fixture (expected failure)"
    exit 1
fi
echo "OK: golden bad fixture fails drift check"

echo "PASS: drift detector tests"
```

Create `scripts/testdata/bad-skill/SKILL.md`:

````markdown
---
name: bad-skill
description: intentionally broken fixture; references a command that does not exist
---

This skill references `navaris sandbox frobnicate` which is not a real command.
Running `navaris sandbox frobnicate --help` should fail.
````

Make both executable and verify the test fails because the script doesn't exist yet:

```bash
chmod +x scripts/skill-drift-check_test.sh
./scripts/skill-drift-check_test.sh
```

Expected: `FAIL: scripts/skill-drift-check.sh does not exist or is not executable`.

- [ ] **Step 2: Write the drift detector**

Create `scripts/skill-drift-check.sh`:

```bash
#!/usr/bin/env bash
# Verify every navaris command referenced in a SKILL.md resolves on the
# checked-in CLI. Exit 0 if every reference is live, non-zero otherwise.
#
# Usage: skill-drift-check.sh <skills-dir>
#
# Env:
#   NAVARIS   path to the navaris binary (default: ./navaris, built on the fly)

set -euo pipefail

SKILLS_DIR="${1:-skills}"
if [ ! -d "$SKILLS_DIR" ]; then
    echo "usage: $0 <skills-dir>" >&2
    exit 2
fi

NAVARIS="${NAVARIS:-./navaris}"
if [ ! -x "$NAVARIS" ]; then
    echo "building navaris CLI for drift check..." >&2
    go build -o ./navaris ./cmd/navaris
    NAVARIS="./navaris"
fi

groups=(project sandbox snapshot session image operation port)

fail=0

# 1. Every top-level group must accept --help.
for g in "${groups[@]}"; do
    if ! "$NAVARIS" "$g" --help >/dev/null 2>&1; then
        echo "drift: 'navaris $g --help' failed (top-level group missing)" >&2
        fail=1
    fi
done

# 2. Every `navaris <group> <subverb>` invocation mentioned in any SKILL.md
#    must accept --help on the current CLI.
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

# Regex anchored on the known groups — avoids false positives from prose like
# "navaris uses..." or "navaris supports...".
pat="navaris +(project|sandbox|snapshot|session|image|operation|port) +[a-z][a-z-]*"
grep -rhoE "$pat" "$SKILLS_DIR" | sort -u > "$tmp"

while read -r line; do
    [ -z "$line" ] && continue
    cmd=${line#navaris }
    # Split intentionally on whitespace via unquoted expansion.
    # shellcheck disable=SC2086
    if ! "$NAVARIS" $cmd --help >/dev/null 2>&1; then
        echo "drift: 'navaris $cmd --help' failed" >&2
        fail=1
    fi
done < "$tmp"

if [ "$fail" -eq 1 ]; then
    echo "FAIL: skill command references drift from the checked-in CLI" >&2
    exit 1
fi

echo "OK: all skill command references resolve"
```

Make it executable:

```bash
chmod +x scripts/skill-drift-check.sh
```

- [ ] **Step 3: Run the test and verify it passes**

```bash
./scripts/skill-drift-check_test.sh
```

Expected: `PASS: drift detector tests`.

- [ ] **Step 4: Run the script standalone against the real skills**

```bash
./scripts/skill-drift-check.sh skills
```

Expected: `OK: all skill command references resolve`.

If it fails, either a skill has an incorrect command reference (fix the skill) or the detector's regex is too tight (fix the script).

- [ ] **Step 5: Commit**

```bash
git add scripts/skill-drift-check.sh scripts/skill-drift-check_test.sh scripts/testdata/bad-skill/SKILL.md
git commit -m "feat(skills): add drift detector script with tests"
```

---

## M8 — CI workflow for drift detector

### Task 8.1: GitHub Actions workflow

**Files:**
- Create: `.github/workflows/skill-drift.yml`

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/skill-drift.yml`:

```yaml
name: Skill Drift Check

on:
  push:
    branches: [main]
    paths:
      - 'skills/**'
      - 'cmd/navaris/**'
      - 'scripts/skill-drift-check.sh'
      - 'scripts/skill-drift-check_test.sh'
      - '.github/workflows/skill-drift.yml'
  pull_request:
    paths:
      - 'skills/**'
      - 'cmd/navaris/**'
      - 'scripts/skill-drift-check.sh'
      - 'scripts/skill-drift-check_test.sh'
      - '.github/workflows/skill-drift.yml'

jobs:
  drift-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.26'

      - name: Build navaris CLI
        run: go build -o ./navaris ./cmd/navaris

      - name: Run drift detector tests
        env:
          NAVARIS: ${{ github.workspace }}/navaris
        run: ./scripts/skill-drift-check_test.sh

      - name: Run drift detector against skills/
        env:
          NAVARIS: ${{ github.workspace }}/navaris
        run: ./scripts/skill-drift-check.sh skills
```

- [ ] **Step 2: Dry-run the workflow locally (optional but recommended)**

If `act` is installed:
```bash
act -j drift-check
```
If not installed, skip this step — the CI run on push/PR will validate it.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/skill-drift.yml
git commit -m "ci(skills): add drift-detector workflow"
```

---

## M9 — User docs + README section

### Task 9.1: docs/claude-skills.md + README section

**Files:**
- Create: `docs/claude-skills.md`
- Modify: `README.md` (append a "Claude Code integration" section after the existing "MCP server" section around line 236)

- [ ] **Step 1: Write the user-facing doc**

Create `docs/claude-skills.md`:

````markdown
# Navaris — Claude Code Skill Pack

Navaris ships a Claude Code plugin — `navaris-cli` — that teaches Claude how to drive the `navaris` CLI on a user's behalf. After installing the plugin, you can ask Claude things like "create an alpine sandbox and run my test suite in it" and it knows which commands to run, in what order, with what flags.

## What's in the pack

One router skill plus four task skills:

| Skill | Covers |
|---|---|
| `using-navaris-cli` | Router: environment check, MCP vs skill-pack routing, dispatch to task skills |
| `navaris-getting-started` | Install, env vars, health check, first project/sandbox, common startup errors |
| `navaris-managing-sandboxes` | Projects, sandbox lifecycle, port forwarding, Incus vs Firecracker backend selection |
| `navaris-running-commands` | `sandbox exec` flags, sessions, `sandbox attach`, exec-vs-session decisions |
| `navaris-snapshots-images-async` | Snapshots, images, async operation lifecycle (`operation wait/get/cancel`, `sandbox wait-state`) |

## Install

In Claude Code:

```
/plugin marketplace add erans/navaris
/plugin install navaris-cli@erans/navaris
```

The first command registers the marketplace entry; the second installs the plugin. Skills load automatically on navaris-related prompts.

## Uninstall

```
/plugin uninstall navaris-cli
```

Remove the marketplace entry with `/plugin marketplace remove erans/navaris` if you're done with it entirely.

## MCP vs skill pack

The navaris repo also ships an MCP server (see [docs/mcp.md](mcp.md)). Pick one:

- **Skill pack** (this) — goes through Bash, one command at a time, with user approval on destructive steps. Best for human-in-the-loop Claude Code sessions.
- **MCP server** — typed tool surface, no Bash. Best for autonomous agent loops without a human driving.

The router skill tells Claude to route autonomous-agent use cases to MCP. If you're using Claude Code interactively, stay on the skill pack.

## Versioning

The plugin's version lives in `.claude-plugin/plugin.json`. It starts at `0.1.0` and moves with navaris CLI release tags:

- **Patch** (`0.1.0` → `0.1.1`) — typo or phrasing fix.
- **Minor** (`0.1.0` → `0.2.0`) — new workflow, new error entry, new CLI flag covered.
- **Major** (`0.1.0` → `1.0.0`) — rename or remove a skill, change the router contract, break compat with a navaris CLI version.

## Drift detection

A CI workflow (`skill-drift.yml`) parses every `skills/**/SKILL.md` for `navaris <group> <subverb>` references and runs `navaris <cmd> --help` against the freshly built CLI. Any mismatch fails the build. This catches removed or renamed subcommands before the skill pack ships stale instructions.

Run it locally:

```bash
./scripts/skill-drift-check.sh skills
```
````

- [ ] **Step 2: Read the existing README to locate the insertion point**

Run:
```bash
grep -n "^## " README.md
```

Find the "MCP server" section (around line 216) and the "CLI usage" section (around line 238). Insert the new section between them.

- [ ] **Step 3: Add a "Claude Code integration" section to README.md**

Between the existing "MCP server" section and the "CLI usage" section, insert:

```markdown
## Claude Code integration

Navaris ships a Claude Code skill pack (`navaris-cli`) so you can ask Claude Code in plain English to drive the CLI for you — "spin up an alpine sandbox and run my tests", "snapshot this sandbox before I upgrade it". Install with:

```
/plugin marketplace add erans/navaris
/plugin install navaris-cli@erans/navaris
```

See [docs/claude-skills.md](docs/claude-skills.md) for the full skill list, install/uninstall, and MCP-vs-skill tradeoffs.

```

Use Edit to insert this after the line `See [docs/mcp.md](docs/mcp.md) for the full tool catalog, env-var reference, and per-tool argument schemas.` and before `## CLI usage`.

- [ ] **Step 4: Verify the README change**

```bash
grep -n "Claude Code integration" README.md
grep -n "docs/claude-skills.md" README.md
```
Expected: both greps return a line number in the 220-240 range.

- [ ] **Step 5: Commit**

```bash
git add docs/claude-skills.md README.md
git commit -m "docs(skills): add user doc for the navaris-cli skill pack"
```

---

## M10 — End-to-end smoke verification

This task is **manual** — there's no fully-automated way to test that Claude Code loads the plugin correctly without running an actual Claude Code session.

### Task 10.1: Local install smoke test

**Files:**
- None (manual verification + documented procedure in commit message).

- [ ] **Step 1: Push the branch to a fork / temporary remote**

If you're working in a worktree off `main`, push the feature branch:

```bash
git push -u origin feature/navaris-cli-skills
```

(For a truly local test that doesn't require a remote, symlink the plugin into `~/.claude/plugins/`: `ln -s $(pwd)/.claude-plugin ~/.claude/plugins/navaris-cli`. Note: plugin directory layouts may vary with Claude Code version; prefer the marketplace install path for the real smoke test.)

- [ ] **Step 2: Install the plugin via marketplace**

In a new Claude Code session, run:

```
/plugin marketplace add <your-user-or-org>/navaris
/plugin install navaris-cli@<your-user-or-org>/navaris
```

If the branch isn't on `main` yet, use the branch reference per Claude Code's install syntax.

- [ ] **Step 3: Verify router skill loads on a navaris-related prompt**

Send: "What do I do to create a navaris sandbox running alpine?"

Expected behavior:
- Claude invokes the `using-navaris-cli` skill first (announces "I'm using the using-navaris-cli skill").
- Router does the env check (asks for `NAVARIS_API_URL` / `NAVARIS_TOKEN` if unset).
- Router invokes `navaris-managing-sandboxes`.
- Response references `navaris sandbox create --image alpine/3.21` (or similar).

- [ ] **Step 4: Verify the MCP fork**

Send: "I want an autonomous agent to manage navaris sandboxes with no human in the loop."

Expected: Claude points the user at `docs/mcp.md` instead of trying to drive the CLI.

- [ ] **Step 5: Run the drift detector locally once more**

```bash
./scripts/skill-drift-check.sh skills
```
Expected: `OK: all skill command references resolve`.

- [ ] **Step 6: Record the smoke-test outcome in a commit message**

If the smoke test surfaced any issues that need skill-content fixes, make those edits and commit them. Otherwise, no commit is needed — this is a verification gate, not a delivery step.

If the smoke test is clean and no fixes are needed, note that in the PR description when opening the pull request. A short bullet like:

> Smoke-tested locally: router loads, routes correctly to `navaris-managing-sandboxes` and `navaris-snapshots-images-async`, MCP fork works.

---

## Post-implementation checklist

Before opening the PR:

- [ ] All 10 tasks complete with commits.
- [ ] `./scripts/skill-drift-check.sh skills` passes locally.
- [ ] `./scripts/skill-drift-check_test.sh` passes locally.
- [ ] `go test ./...` still passes (nothing in Go-land changed, but confirm).
- [ ] `README.md` has the "Claude Code integration" section.
- [ ] Smoke test completed (Task 10).
- [ ] `.github/workflows/skill-drift.yml` runs on the first push to the feature branch.
