# Navaris — Claude Code Skill Pack

Navaris ships a Claude Code plugin — `navaris-cli` — that teaches Claude how to drive the `navaris` CLI on a user's behalf. After installing the plugin, you can ask Claude things like "create an alpine sandbox and run my test suite in it" and it knows which commands to run, in what order, with what flags.

## Install

> Requires a Claude Code build with the `/plugin` command. Run `claude --version` if unsure; update if `/plugin marketplace add` is unrecognised.

In Claude Code:

```
/plugin marketplace add erans/navaris
/plugin install navaris-cli@navaris
```

The first command registers the marketplace entry; the second installs the plugin. Skills load automatically on navaris-related prompts.

## Uninstall

```
/plugin uninstall navaris-cli@navaris
```

Remove the marketplace entry with `/plugin marketplace remove navaris` if you're done with it entirely.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Unknown command: /plugin` | Claude Code is too old | Update with `claude update` (or your installer's equivalent); plugin marketplaces require a recent build |
| `marketplace not found` after `/plugin marketplace add erans/navaris` | Repo unreachable, or the path you typed doesn't match | Re-check the spelling; for private repos make sure your `GITHUB_TOKEN` has read access |
| Plugin installed but skills don't fire | Plugin cache stale | Run `/reload-plugins` (or restart Claude Code) |
| Skills still missing after reload | Cache corruption | `rm -rf ~/.claude/plugins/cache` and re-run `/plugin install navaris-cli@navaris` |

## What's in the pack

One router skill plus four task skills:

| Skill | Covers |
|---|---|
| `using-navaris-cli` | Router: environment check, MCP vs skill-pack routing, dispatch to task skills |
| `navaris-getting-started` | Install, env vars, health check, first project/sandbox, common startup errors |
| `navaris-managing-sandboxes` | Projects, sandbox lifecycle, port forwarding, Incus vs Firecracker backend selection |
| `navaris-running-commands` | `sandbox exec` flags, sessions, `sandbox attach`, exec-vs-session decisions |
| `navaris-snapshots-images-async` | Snapshots, images, async operation lifecycle (`operation wait/get/cancel`, `sandbox wait-state`) |

## MCP vs skill pack

The navaris repo also ships an MCP server (see [mcp.md](mcp.md)). Pick one:

- **Skill pack** (this) — goes through Bash, one command at a time, with user approval on destructive steps. Best for human-in-the-loop Claude Code sessions.
- **MCP server** — typed tool surface, no Bash. Best for autonomous agent loops without a human driving.

The router skill tells Claude to route autonomous-agent use cases to MCP. If you're using Claude Code interactively, stay on the skill pack.

## Versioning

The plugin's version lives in `.claude-plugin/plugin.json`. It starts at `0.1.0` and moves with navaris CLI release tags:

- **Patch** (`0.1.0` → `0.1.1`) — typo or phrasing fix.
- **Minor** (`0.1.0` → `0.2.0`) — new workflow, new error entry, new CLI flag covered.
- **Major** (`0.1.0` → `1.0.0`) — rename or remove a skill, change the router contract, break compat with a navaris CLI version.

## Drift detection

CI runs a drift check (`.github/workflows/skill-drift.yml`) on every push that parses each `SKILL.md` for `navaris <group> <subverb>` references and verifies them against the freshly built CLI. If a navaris release renames or removes a subcommand, the build fails — so the skills shouldn't ship referencing removed commands.

## Reporting issues

File bugs and feature requests at https://github.com/erans/navaris/issues. PRs that add new task skills or fix existing ones are welcome — see `docs/superpowers/specs/` for the design spec and `docs/superpowers/plans/` for the original implementation plan.
