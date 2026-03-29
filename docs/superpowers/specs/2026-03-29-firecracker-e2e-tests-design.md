# Firecracker E2E Integration Tests

## Goal

Run the existing backend-agnostic integration test suite against a real Firecracker backend inside Docker, mirroring the Incus integration test pattern.

## Success Criteria

CI passes `make integration-test-firecracker` in GitHub Actions with all non-stub tests green, and navarisd logs confirm `backend: firecracker`.

## Architecture

Single-container approach: navarisd runs alongside Firecracker/jailer in one privileged Docker container with `/dev/kvm` access. No separate daemon container needed — Firecracker doesn't have a persistent daemon; navarisd launches VMs directly.

```
docker-compose.integration-firecracker.yml
├── navarisd          — navarisd + Firecracker binaries + kernel + rootfs
├── navarisd-dev      — extends navarisd with exposed port (dev profile)
└── test-runner       — existing Dockerfile.test, reused as-is
```

Note: Unlike the Incus setup which has a separate `incus` service, Firecracker uses a single service because there is no external daemon to manage.

## Dockerfile.navarisd-firecracker

Multi-stage build:

### Stage 1: Build (Go)

- Base: `golang:1.26-bookworm`
- Compile `navarisd` with `-tags firecracker`
- Compile `navaris-agent` (static binary, `GOOS=linux CGO_ENABLED=0`)

### Stage 2: Rootfs

- Base: `alpine:3.21`
- Install `openrc` and `e2fsprogs` (`apk add --no-cache openrc e2fsprogs`)
- Copy the navaris-agent binary to `/usr/local/bin/navaris-agent`
- Add an OpenRC service script at `/etc/init.d/navaris-agent`:

```sh
#!/sbin/openrc-run
name="navaris-agent"
description="Navaris guest agent"
command="/usr/local/bin/navaris-agent"
command_background="yes"
pidfile="/run/navaris-agent.pid"
```

- Enable the service: `rc-update add navaris-agent default`
- Use `mke2fs -d` to create a pre-populated ext4 image from the Alpine filesystem — no loop mount or build-time privileges required

The rootfs stage produces the file `/opt/firecracker/images/alpine-3.21.ext4` (matching the flat `ImageRef` format `alpine-3.21`).

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

The kernel is `vmlinux.bin` published alongside the release. Download it to `/opt/firecracker/vmlinux` (no extension in the final path — the compose `--kernel-path` flag uses this exact name).

## Image Ref Convention

The Firecracker provider validates image refs with `regexp.MustCompile("^[a-zA-Z0-9._-]+$")`, which rejects slashes. All image refs use flat names:

- Rootfs stored at: `/opt/firecracker/images/alpine-3.21.ext4`
- `--image-dir` flag: `/opt/firecracker/images`
- Provider resolves `ImageRef: "alpine-3.21"` to `/opt/firecracker/images/alpine-3.21.ext4` via `filepath.Join(imageDir, imageRef+".ext4")`
- `NAVARIS_BASE_IMAGE` env var: `alpine-3.21`

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
      NAVARIS_BASE_IMAGE: alpine-3.21
      NAVARIS_CLI: /usr/local/bin/navaris
      NAVARIS_SKIP_SNAPSHOTS: "1"
      NAVARIS_SKIP_PORTS: "1"
    depends_on:
      navarisd:
        condition: service_healthy
    profiles:
      - test
```

## Test Scope

### Reused as-is

The existing `test/integration/` tests run unchanged. They are backend-agnostic — they use the Navaris SDK against the API.

### Skipped tests

Snapshot/restore and port forwarding are Phase 2 stubs in the Firecracker provider. Tests that use these features are skipped via environment variables:

**`NAVARIS_SKIP_SNAPSHOTS=1`** — skips tests calling `CreateSnapshot`, `CreateSandboxFromSnapshot`, or `PromoteImage`:

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Skip("snapshots not supported by this backend")
}
```

Applied to:
- `test/integration/snapshot_test.go` — skip at top of each test function
- `test/integration/e2e_test.go` — skip the snapshot section (the portion starting with `CreateSnapshot`)
- `test/integration/image_test.go` — skip `TestImagePromoteFromSnapshot` (uses `CreateSnapshot`)

**`NAVARIS_SKIP_PORTS=1`** — skips tests calling `CreatePort`, `ListPorts`, `DeletePort`:

```go
if os.Getenv("NAVARIS_SKIP_PORTS") == "1" {
    t.Skip("port forwarding not supported by this backend")
}
```

Applied to:
- `test/integration/port_test.go` — skip at top of `TestPortPublishListDelete`

Note: `TestImageRegisterListGetDelete` in `image_test.go` is a metadata-only operation (no provider call) and runs as-is. It hardcodes `Backend: "incus"` which is just a stored string — irrelevant to test correctness.

## Makefile Targets

Add to existing Makefile (recipe lines use tabs, not spaces):

```makefile
FC_COMPOSE_FILE := docker-compose.integration-firecracker.yml

.PHONY: integration-test-firecracker integration-env-firecracker integration-env-firecracker-down integration-logs-firecracker

integration-test-firecracker:
	docker compose -f $(FC_COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	docker compose -f $(FC_COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env-firecracker:
	NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COMPOSE_FILE) --profile dev up -d --build navarisd-dev
	@echo ""
	@echo "Navaris API (Firecracker): http://localhost:8080"
	@echo "Token:                     test-token"
	@echo ""
	@echo "Run tests:"
	@echo "  NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token NAVARIS_SKIP_SNAPSHOTS=1 NAVARIS_SKIP_PORTS=1 go test -tags integration ./test/integration/ -v"
	@echo ""
	@echo "Tear down:"
	@echo "  make integration-env-firecracker-down"

integration-env-firecracker-down:
	NAVARIS_HOST_PORT=8080 docker compose -f $(FC_COMPOSE_FILE) --profile dev down -v

integration-logs-firecracker:
	docker compose -f $(FC_COMPOSE_FILE) logs -f
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
    # Pin to ubuntu-24.04 — KVM is available on GitHub-hosted runners
    # but not guaranteed on all ubuntu-latest aliases or self-hosted runners.
    runs-on: ubuntu-24.04
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v4
      - name: Enable KVM
        run: sudo chmod 666 /dev/kvm
      - name: Run integration tests
        run: make integration-test-firecracker
```

## Files to Create/Modify

| File | Action |
|------|--------|
| `Dockerfile.navarisd-firecracker` | Create |
| `scripts/firecracker-entrypoint.sh` | Create |
| `docker-compose.integration-firecracker.yml` | Create |
| `Makefile` | Add 4 targets + `.PHONY` |
| `.github/workflows/integration-firecracker.yml` | Create |
| `test/integration/e2e_test.go` | Add snapshot skip check |
| `test/integration/snapshot_test.go` | Add snapshot skip check at top of each test |
| `test/integration/image_test.go` | Add snapshot skip check for `TestImagePromoteFromSnapshot` |
| `test/integration/port_test.go` | Add port skip check for `TestPortPublishListDelete` |
