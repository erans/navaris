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
