# Navaris CLI Skill Pack — Design Spec

**Date:** 2026-04-18
**Status:** Draft — pending user review

## 1. Goal & scope

Ship a Claude Code plugin, bundled inside the navaris repo, that teaches Claude how to drive the `navaris` CLI on a user's behalf. After a one-time install, when a user asks Claude to do something with sandboxes ("spin up an alpine container and run my test suite", "snapshot this sandbox before I upgrade it", "attach me to session X"), Claude knows the exact commands, flags, and ordering.

The pack is a router skill plus four task skills. It coexists with the existing MCP server: the router explicitly points autonomous-agent use cases at MCP, and keeps human-in-the-loop CLI use cases on the skill pack.

### Non-goals (v1)

- A Go SDK (`pkg/client`) skill.
- A web-UI skill.
- A daemon-operator skill (kernel builds, jailer setup, KVM configuration, OTLP wiring).
- Auto-generation of skill content from `navaris --help` output.
- Bundling the CLI binary itself inside the plugin.
- Per-skill changelog, telemetry, or usage analytics.
- A separate skill for `navaris-agent` (the Firecracker in-guest agent).

## 2. Plugin layout in the repo

```
.claude-plugin/
  plugin.json                                # name, version, description, skills list
  marketplace.json                           # marketplace entry enabling /plugin marketplace add erans/navaris
skills/
  using-navaris-cli/
    SKILL.md                                 # router (~150 lines): env check → route → MCP positioning
  navaris-getting-started/
    SKILL.md                                 # install, env vars, health check, first sandbox
  navaris-managing-sandboxes/
    SKILL.md                                 # projects, sandbox lifecycle, ports, backend selection
  navaris-running-commands/
    SKILL.md                                 # exec flags, sessions, attach, exec-vs-session
  navaris-snapshots-images-async/
    SKILL.md                                 # snapshots, images, operations, wait-state
docs/
  claude-skills.md                           # user-facing: what's in the pack, install, uninstall, MCP-vs-skill, versioning
README.md                                    # add a "Using Claude Code with navaris" section linking into docs/claude-skills.md
```

Five SKILL.md files total. Plugin manifest and marketplace entry live in `.claude-plugin/`. One new user-facing doc (`docs/claude-skills.md`). Existing `README.md` gets a short pointer section.

## 3. Router skill — `using-navaris-cli`

### Frontmatter

```yaml
---
name: using-navaris-cli
description: Use when the user asks Claude to do anything with navaris sandboxes via the CLI — create/start/stop sandboxes, exec commands, snapshot, attach, manage projects. Establishes environment, picks the right task skill, and points autonomous-agent use cases at the MCP server.
---
```

### Body, in fixed order

1. **One-line "what is navaris"** — sandbox control plane, two backends (Incus / Firecracker), unified CLI.

2. **MCP fork.** If the user is driving navaris from an autonomous agent loop with no human in the chair, prefer the MCP server (see `docs/mcp.md`). This pack is for human-in-the-loop CLI work in Claude Code.

3. **Environment check — always run before any CLI command.**
   - `NAVARIS_API_URL` set? If missing, ask the user or default to `http://localhost:8080`.
   - `NAVARIS_TOKEN` set? Required when the daemon was started with `--auth-token`.
   - One-shot health check: `navaris project list --quiet` (success = CLI installed, daemon reachable, auth accepted). On failure, classify: `command not found` → CLI not installed → route to `navaris-getting-started`; `401` → bad/missing token → route to `navaris-getting-started`; `connection refused` / `dial tcp` → daemon down → surface to the user before continuing.

4. **Routing table — user intent → task skill.**

   | Intent | Skill |
   |---|---|
   | Install CLI, set up env, "where do I start" | `navaris-getting-started` |
   | Create/list/start/stop/destroy a sandbox; manage projects/ports; pick a backend | `navaris-managing-sandboxes` |
   | Run a command in a sandbox; attach a terminal; manage sessions | `navaris-running-commands` |
   | Snapshot, restore, promote to image, wait on async ops | `navaris-snapshots-images-async` |

5. **Quiet-mode reminder** — single line: when scripting or piping, pass `--quiet` so commands print only the resulting ID.

6. **Red flags.** Short list of things never to do:
   - Don't pass `--auth-token` directly in a shared shell history.
   - Don't run `sandbox destroy` without confirming the ID with the user.
   - Don't assume the daemon is up — always run the health check first.
   - Don't edit daemon config (KVM / Firecracker flags) — that's an operator skill, not part of this pack.

Target length: 120–180 lines. Always loaded as a small router; deep content lives in the task skills.

## 4. Task skills

Each task skill follows the same internal shape:

1. **Frontmatter** (`name`, `description` — specific enough that Claude Code's skill matcher lands on the right one).
2. **Reference.** Tight table of commands and key flags for this slice of the CLI. Pulled from `cmd/navaris/*.go`.
3. **Workflows.** 2–4 opinionated flows as numbered steps with exact commands. Each workflow names the problem it solves in one line, then walks through the commands.
4. **Common errors.** 3–5 symptoms → causes → fixes, so Claude can recover without escalating every failure.

Each task skill is self-contained — a user who installs only one still gets full value for that slice.

### 4.1 `navaris-getting-started`

**Reference:**
- Install: `go build -o navaris ./cmd/navaris` (source) or fetch a release binary.
- Env vars: `NAVARIS_API_URL` (required), `NAVARIS_TOKEN` (required if daemon uses `--auth-token`).
- Health check: `navaris project list --quiet`.

**Workflows:**
1. First-time setup from scratch (daemon + CLI on localhost).
2. Connect to an existing remote navarisd (env vars + sanity check).
3. Create project → first sandbox → exec `echo hello` → destroy sandbox.

**Common errors:**
- `401 Unauthorized` → `NAVARIS_TOKEN` missing or wrong.
- `connection refused` → daemon not running on the configured host.
- `dial tcp: no such host` → DNS / `NAVARIS_API_URL` typo.
- `no projects found` → first call after fresh install; create a project before a sandbox.

### 4.2 `navaris-managing-sandboxes`

**Reference:**
- `project create/list/get/update/delete`.
- `sandbox create/list/get/start/stop/destroy`.
- `port create/list/delete`.
- Backend auto-detection rule: image reference with a `/` (e.g. `alpine/3.21`) → Incus; flat (e.g. `alpine-3.21`) → Firecracker. Override with `--backend`.
- `--quiet` on create/destroy for scripting.

**Workflows:**
1. Ephemeral dev sandbox on Incus (fast, throwaway).
2. Isolated untrusted workload on Firecracker (hardware isolation).
3. Publish a sandbox port to the host (`port create`) and verify it's reachable.
4. Bulk-destroy a set of sandboxes via `sandbox list --output json | jq -r '.[].id' | xargs -n1 navaris sandbox destroy --quiet`.

**Common errors:**
- Image not found → `image list` to find the right reference.
- KVM missing → daemon started without Firecracker; pick an Incus image or enable Firecracker.
- Backend not enabled → check `navarisd` flags.
- `destroy` blocked because sandbox is running → `sandbox stop` first, or pass `--force` if supported.

### 4.3 `navaris-running-commands`

**Reference:**
- `sandbox exec <id> -- <cmd>` with `--env KEY=VAL` (repeatable), `--workdir /path`, `--timeout 30s`. Note: stdin is not forwarded in v1 — out of scope for this pack until the provider support lands.
- `session create/list/get/destroy` (direct or tmux-backed).
- `sandbox attach <id>` (WebSocket terminal to the session).
- `sandbox wait-state <id> --state running --timeout 30s`.

**Workflows:**
1. One-shot exec with env and workdir (`sandbox exec <id> --env K=V --workdir /srv -- ./run.sh`).
2. Persistent tmux-backed session → attach interactively → detach cleanly.
3. Wait for `running` before exec (avoids "sandbox not ready" races).
4. Decision tree: stateless one-shot → `exec`; stateful / multi-command / interactive → `session` + `attach`.

**Common errors:**
- Session not found → it was destroyed or sandbox was restarted.
- Attach WebSocket drop → daemon restart or network blip; reconnect with `attach` again.
- Exec timeout → process may still be running server-side; don't retry blindly.
- `sandbox not running` → either not started yet or stopped; use `wait-state` or `start`.

### 4.4 `navaris-snapshots-images-async`

**Reference:**
- `snapshot create <sandbox-id> --label X [--consistency stopped|live]` (default `stopped`), `snapshot list/get/restore/delete`.
- `image promote --snapshot <snapshot-id> --name <name> --version <ver>`, `image register --name <name> --version <ver> --backend <type> --backend-ref <ref>`, `image list/get/delete`.
- `operation list/get/cancel/wait <operation-id> [--timeout 5m]`.
- `sandbox wait-state <id> --state running`.

**Workflows:**
1. Snapshot → upgrade → restore on failure (classic safety net).
2. Promote a working snapshot to a reusable base image.
3. Register an externally built rootfs (Firecracker) as a navaris image.
4. Launch a long operation with `--wait=false`, capture the operation ID, poll with `operation wait`, cancel on timeout.

**Common errors:**
- Live snapshot not supported on this backend → fall back to `stopped`.
- Image delete blocked → listed sandboxes still derive from it; clean them up first.
- Operation stuck in `pending` → daemon backlog or dependency waiting; inspect with `operation get`.

## 5. Install UX, maintenance, versioning

### Install

Users run, in Claude Code:

```
/plugin marketplace add erans/navaris
/plugin install navaris-cli@navaris
```

The `.claude-plugin/marketplace.json` in the repo root registers the marketplace; `.claude-plugin/plugin.json` declares the plugin and lists the 5 skills. Claude Code auto-loads the router whenever any of the 5 skills matches a user's request.

`docs/claude-skills.md` documents: what the pack does, the two install commands, how to uninstall (`/plugin uninstall`), one-paragraph "MCP vs skill pack" comparison, and the versioning policy. `README.md` grows a short section pointing to `docs/claude-skills.md`.

### Versioning

`plugin.json` carries `version`. Start at `0.1.0`. Bump:

- **Patch** — typo / phrasing fix.
- **Minor** — new workflow, new error entry, new flag covered.
- **Major** — rename or remove a skill, change the router contract, break the navaris CLI version expected by the pack.

Skill versions move with navaris CLI release tags: if a CLI release adds a flag, the same release ships the SKILL.md update.

### Maintenance

Hand-written, not auto-generated. When `cmd/navaris/**` changes a flag or command, the PR that changes it updates the affected SKILL.md — same pattern the existing README already follows.

**Drift detector (cheap):** a CI check that parses every SKILL.md for `navaris <cmd>` invocations and runs `navaris <cmd> --help` against the freshly built CLI; any non-zero exit fails the build. Catches renamed/removed subcommands before they ship.

## 6. Architecture summary

Thin router + fat task skills. Router handles global concerns (environment check, MCP positioning, routing). Each task skill is self-contained and covers one slice of the CLI surface with reference + workflows + errors. Claude Code's skill matcher loads the router first on any navaris-related intent; the router then invokes the specific task skill based on the user's goal.

## 7. Success criteria

The pack is successful when:

1. A new user can install it with two `/plugin` commands and immediately ask Claude "help me spin up a dev sandbox" without needing to read the navaris README first.
2. Claude recovers cleanly from the common errors enumerated in each skill — doesn't loop on `401`, doesn't destroy the wrong sandbox, doesn't hang waiting on an untracked async operation.
3. The pack stays accurate: the drift detector blocks CI when a SKILL.md references a command that no longer exists.
4. Users running autonomous agent flows (not human-in-the-loop Claude Code) are routed to MCP by the router rather than reimplementing MCP behavior in Bash.
