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

`--quiet` silences the table on each destroy so only the IDs print. (Note: `sandbox list --quiet` does not compact the table — use `--output json | jq` for IDs.)

## Common errors

| Symptom | Cause | Fix |
|---|---|---|
| `image not found` | Image ref typo or image not registered on this backend | `navaris image list --name <partial>`; for Incus, use the `vendor/version` form (e.g. `alpine/3.21`) |
| `KVM not available` | Firecracker backend is enabled but `/dev/kvm` is missing or non-readable | Either pick an Incus image (slash-style) or enable KVM on the host; verify daemon startup logs |
| `backend not enabled` | Trying to create a Firecracker sandbox on a daemon started with Incus only (or vice versa) | Restart navarisd with the right backend flags (see `README.md`) |
| `destroy` hangs or times out | Sandbox is still running | Stop first: `navaris sandbox stop <id>`, then destroy; add `--force` on stop if needed |
| `--project flag or NAVARIS_PROJECT env var is required` | `sandbox create` invoked without a project | Set `NAVARIS_PROJECT` in the environment, or pass `--project <id>` |
