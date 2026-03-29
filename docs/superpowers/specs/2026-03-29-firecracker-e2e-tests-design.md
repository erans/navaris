# Firecracker E2E Integration Tests

## Goal

Run the existing backend-agnostic integration test suite against a real Firecracker backend inside Docker, mirroring the Incus integration test pattern.

## Architecture

Single-container approach: navarisd runs alongside Firecracker/jailer in one privileged Docker container with `/dev/kvm` access. No separate daemon container needed — Firecracker doesn't have a persistent daemon; navarisd launches VMs directly.

```
docker-compose.integration-firecracker.yml
├── navarisd          — navarisd + Firecracker binaries + kernel + rootfs
├── navarisd-dev      — extends navarisd with exposed port (dev profile)
└── test-runner       — existing Dockerfile.test, reused as-is
```

## Dockerfile.navarisd-firecracker

Multi-stage build:

### Stage 1: Build (Go)

- Base: `golang:1.26-bookworm`
- Compile `navarisd` with `-tags firecracker`
- Compile `navaris-agent` (static binary, `CGO_ENABLED=0`)

### Stage 2: Rootfs

- Base: `alpine:3.21`
- Copy the navaris-agent binary to `/usr/local/bin/navaris-agent`
- Add an init script that starts the agent on boot
- Use `mke2fs -d` to create a pre-populated ext4 image from the Alpine filesystem — no loop mount or build-time privileges required

The rootfs stage produces the file `/opt/firecracker/images/alpine/3.21.ext4` (matching the `ImageRef` format `alpine/3.21`).

### Stage 3: Runtime

- Base: `debian:bookworm-slim`
- Install: `wget` (health checks), `iproute2` (tap devices), `iptables` (masquerade), `e2fsprogs` (for rootfs copy operations), `procps` (process management)
- Download pre-built Firecracker-compatible vmlinux kernel from Firecracker GitHub releases
- Download Firecracker and jailer binaries from Firecracker GitHub releases
- Copy navarisd binary from build stage
- Copy rootfs image from rootfs stage
- Copy entrypoint script

### Kernel & Binary Versions

Pin to a specific Firecracker release (e.g. `v1.12.0`). Download at Docker build time from `https://github.com/firecracker-microvm/firecracker/releases/`.

The kernel is the minimal `vmlinux.bin` published alongside the release.

## Entrypoint Script (scripts/firecracker-entrypoint.sh)

```bash
#!/bin/bash
set -eu

# Enable IP forwarding for VM networking.
sysctl -w net.ipv4.ip_forward=1

# Create chroot base for jailer.
mkdir -p /srv/firecracker

# Exec navarisd with all arguments.
exec navarisd "$@"
```

## Docker Compose (docker-compose.integration-firecracker.yml)

```yaml
services:
  navarisd:
    build:
      context: .
      dockerfile: Dockerfile.navarisd-firecracker
    privileged: true
    devices:
      - /dev/kvm:/dev/kvm
    command:
      - --listen=:8080
      - --db-path=/tmp/navaris.db
      - --firecracker-bin=/usr/local/bin/firecracker
      - --jailer-bin=/usr/local/bin/jailer
      - --kernel-path=/opt/firecracker/vmlinux
      - --image-dir=/opt/firecracker/images
      - --chroot-base=/srv/firecracker
      - --auth-token=test-token
      - --log-level=debug
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "--header",
             "Authorization: Bearer test-token",
             "http://localhost:8080/v1/health"]
      interval: 2s
      timeout: 5s
      retries: 15
      start_period: 5s

  navarisd-dev:
    extends:
      service: navarisd
    ports:
      - "${NAVARIS_HOST_PORT:-8080}:8080"
    profiles:
      - dev

  test-runner:
    build:
      context: .
      dockerfile: Dockerfile.test
    environment:
      NAVARIS_API_URL: http://navarisd:8080
      NAVARIS_TOKEN: test-token
      NAVARIS_BASE_IMAGE: alpine/3.21
      NAVARIS_CLI: /usr/local/bin/navaris
      NAVARIS_SKIP_SNAPSHOTS: "1"
    depends_on:
      navarisd:
        condition: service_healthy
    profiles:
      - test
```

## Test Scope

### Reused as-is

The existing `test/integration/` tests run unchanged. They are backend-agnostic — they use the Navaris SDK against the API.

### Snapshot tests: skipped

Snapshot/restore is a Phase 2 stub in the Firecracker provider. Tests that use snapshots are skipped via `NAVARIS_SKIP_SNAPSHOTS=1` environment variable. Each test that calls `CreateSnapshot` or `CreateSandboxFromSnapshot` checks this env var at the top:

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Skip("snapshots not supported by this backend")
}
```

This requires a small addition to `test/integration/snapshot_test.go` and the snapshot portion of `e2e_test.go`.

## Makefile Targets

```makefile
FC_COMPOSE_FILE := docker-compose.integration-firecracker.yml

integration-test-firecracker:
    docker compose -f $(FC_COMPOSE_FILE) --profile test up \
        --build --abort-on-container-exit --exit-code-from test-runner; \
    rc=$$?; \
    docker compose -f $(FC_COMPOSE_FILE) --profile test down -v; \
    exit $$rc

integration-env-firecracker:
    NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COMPOSE_FILE) --profile dev up -d --build navarisd-dev

integration-env-firecracker-down:
    NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COMPOSE_FILE) --profile dev down -v
```

## CI Workflow (.github/workflows/integration-firecracker.yml)

```yaml
name: Integration Tests (Firecracker)
on:
  push:
    branches: [main]
  pull_request:

jobs:
  integration:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v4
      - name: Enable KVM
        run: sudo chmod 666 /dev/kvm
      - name: Run integration tests
        run: make integration-test-firecracker
```

## Image Ref Convention

The rootfs image is stored at `/opt/firecracker/images/alpine/3.21.ext4`. The `--image-dir` flag points to `/opt/firecracker/images`. When a sandbox is created with `ImageID: "alpine/3.21"`, the provider resolves it to `/opt/firecracker/images/alpine/3.21.ext4` via `filepath.Join(imageDir, imageRef+".ext4")`.

This means the rootfs stage must create the directory structure `alpine/` under the images dir and name the file `3.21.ext4`.

## Files to Create/Modify

| File | Action |
|------|--------|
| `Dockerfile.navarisd-firecracker` | Create |
| `scripts/firecracker-entrypoint.sh` | Create |
| `docker-compose.integration-firecracker.yml` | Create |
| `Makefile` | Add 3 targets |
| `.github/workflows/integration-firecracker.yml` | Create |
| `test/integration/e2e_test.go` | Add skip check for snapshot section |
| `test/integration/snapshot_test.go` | Add skip check at top |
