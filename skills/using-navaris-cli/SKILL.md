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
