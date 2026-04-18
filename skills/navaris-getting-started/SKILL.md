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

## Workflows

### 1. First-time setup from scratch (daemon + CLI on localhost)

1. Start the daemon in another terminal (see `README.md` for daemon flags):
   ```bash
   ./navarisd --incus-socket /var/lib/incus/unix.socket --auth-token "$(openssl rand -hex 32)"
   ```
   Capture the token that was printed or generated.
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
   PROJECT_ID=$(navaris project create --name playground --quiet)
   export NAVARIS_PROJECT="$PROJECT_ID"
   ```
2. Create a sandbox (Incus by default; image reference with `/` routes to Incus):
   ```bash
   SANDBOX_ID=$(navaris sandbox create --name hello --image alpine/3.21 --quiet)
   ```
3. Wait for it to be running:
   ```bash
   navaris sandbox wait-state "$SANDBOX_ID" --state running --timeout 60s
   ```
4. Exec a command:
   ```bash
   navaris sandbox exec "$SANDBOX_ID" -- echo "hello from the sandbox"
   ```
5. Destroy it:
   ```bash
   navaris sandbox destroy "$SANDBOX_ID" --quiet
   ```

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `401 Unauthorized` | `NAVARIS_TOKEN` missing or wrong | Re-export `NAVARIS_TOKEN` with the value the daemon was started with |
| `connection refused` | Daemon not running on the configured host/port | Start `navarisd` or point `NAVARIS_API_URL` at the right host |
| `dial tcp: no such host` | DNS miss or typo in `NAVARIS_API_URL` | Fix the URL or add to `/etc/hosts` |
| `--project flag or NAVARIS_PROJECT env var is required` | `sandbox create` invoked without a project | Either pass `--project <id>` or `export NAVARIS_PROJECT=<id>` |
| `no images found` | No base images registered | `navaris image list` — if empty, promote an existing snapshot or register one; for Incus, use a slash-style image ref like `alpine/3.21` to pull from the Incus image server |
