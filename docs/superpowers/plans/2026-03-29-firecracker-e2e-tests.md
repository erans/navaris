# Firecracker E2E Integration Tests Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the existing backend-agnostic integration test suite against a real Firecracker backend inside Docker, mirroring the Incus integration test pattern.

**Architecture:** Single privileged Docker container runs navarisd + Firecracker/jailer with `/dev/kvm` passthrough. A multi-stage Dockerfile builds navarisd, the guest agent, and an Alpine ext4 rootfs image. The existing `Dockerfile.test` and test suite are reused as-is, with skip guards added for stub operations.

**Tech Stack:** Go, Docker, Docker Compose, Firecracker, Alpine Linux, OpenRC, mke2fs, GitHub Actions

**Spec:** `docs/superpowers/specs/2026-03-29-firecracker-e2e-tests-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `Dockerfile.navarisd-firecracker` | Create | Multi-stage build: Go binaries → Alpine rootfs → Debian runtime |
| `scripts/firecracker-entrypoint.sh` | Create | Enable IP forwarding, create jailer chroot base, exec navarisd |
| `docker-compose.integration-firecracker.yml` | Create | Service definitions: navarisd, navarisd-dev, test-runner |
| `Makefile` | Modify | Add 4 targets + `.PHONY` for Firecracker integration |
| `.github/workflows/integration-firecracker.yml` | Create | CI workflow with KVM access |
| `test/integration/e2e_test.go` | Modify | Add snapshot skip guard in TestEndToEndLifecycle |
| `test/integration/snapshot_test.go` | Modify | Add snapshot skip guard in TestSnapshotRestoreToSandbox |
| `test/integration/image_test.go` | Modify | Add snapshot skip guard in TestImagePromoteFromSnapshot |
| `test/integration/port_test.go` | Modify | Add port skip guard in TestPortPublishListDelete |

---

### Task 1: Entrypoint Script

**Files:**
- Create: `scripts/firecracker-entrypoint.sh`

- [ ] **Step 1: Create the entrypoint script**

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

- [ ] **Step 2: Make it executable**

Run: `chmod +x scripts/firecracker-entrypoint.sh`

- [ ] **Step 3: Commit**

```bash
git add scripts/firecracker-entrypoint.sh
git commit -m "feat: add Firecracker integration entrypoint script"
```

---

### Task 2: Dockerfile

**Files:**
- Create: `Dockerfile.navarisd-firecracker`

**Reference files:**
- `Dockerfile.navarisd` — existing pattern for Go build + Debian runtime
- `Dockerfile.incus` — existing pattern for integration container
- `cmd/navaris-agent/main.go` — agent binary to compile (listens on vsock port 1024)

- [ ] **Step 1: Create the multi-stage Dockerfile**

The Dockerfile has three stages:

**Stage 1 (build):** Compile `navarisd` with `-tags firecracker` and `navaris-agent` as a static binary.

**Stage 2 (rootfs):** Create an Alpine ext4 filesystem image with the agent binary and an OpenRC service to start it on boot. Use `mke2fs -d` to pack the filesystem into an ext4 image without needing loop mounts or privileges. The rootfs is stored at `/opt/firecracker/images/alpine-3.21.ext4`.

**Stage 3 (runtime):** Debian slim with Firecracker/jailer binaries and kernel downloaded from GitHub releases, plus the built navarisd and rootfs image.

```dockerfile
# ---- Stage 1: Build Go binaries ----
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags firecracker -o /navarisd ./cmd/navarisd
RUN GOOS=linux CGO_ENABLED=0 go build -o /navaris-agent ./cmd/navaris-agent

# ---- Stage 2: Build Alpine rootfs ext4 image ----
FROM alpine:3.21 AS rootfs

# Install OpenRC (init system) and e2fsprogs (mke2fs).
RUN apk add --no-cache openrc e2fsprogs

# Copy the agent binary.
COPY --from=build /navaris-agent /usr/local/bin/navaris-agent

# Add OpenRC service for the agent.
RUN printf '#!/sbin/openrc-run\nname="navaris-agent"\ndescription="Navaris guest agent"\ncommand="/usr/local/bin/navaris-agent"\ncommand_background="yes"\npidfile="/run/navaris-agent.pid"\n' \
    > /etc/init.d/navaris-agent && \
    chmod +x /etc/init.d/navaris-agent && \
    rc-update add navaris-agent default

# Configure OpenRC for container/VM use — suppress warnings about cgroups/mtab.
RUN sed -i 's/^#rc_sys=""/rc_sys="lxc"/' /etc/rc.conf 2>/dev/null || true

# Pack the entire Alpine rootfs into an ext4 image.
# mke2fs -d populates the image from a directory without needing loop mount.
# Write to /tmp first to avoid the output file being included in its own source tree.
RUN mke2fs -t ext4 -d / -L rootfs /tmp/alpine-3.21.ext4 512M && \
    mkdir -p /opt/firecracker/images && \
    mv /tmp/alpine-3.21.ext4 /opt/firecracker/images/alpine-3.21.ext4

# ---- Stage 3: Runtime ----
FROM debian:bookworm-slim

# Pin Firecracker release version.
ARG FC_VERSION=v1.12.0

RUN apt-get update && apt-get install -y --no-install-recommends \
        wget iproute2 iptables e2fsprogs procps ca-certificates \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Download Firecracker and jailer binaries.
RUN ARCH=$(uname -m) && \
    wget -q "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz" \
        -O /tmp/fc.tgz && \
    tar -xzf /tmp/fc.tgz -C /tmp && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/firecracker-${FC_VERSION}-${ARCH} /usr/local/bin/firecracker && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/jailer-${FC_VERSION}-${ARCH} /usr/local/bin/jailer && \
    chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer && \
    rm -rf /tmp/fc.tgz /tmp/release-*

# Download Firecracker-compatible kernel.
RUN ARCH=$(uname -m) && \
    wget -q "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/vmlinux-6.1-${ARCH}.bin" \
        -O /opt/firecracker/vmlinux

# Create image directory and copy rootfs from stage 2.
RUN mkdir -p /opt/firecracker/images
COPY --from=rootfs /opt/firecracker/images/alpine-3.21.ext4 /opt/firecracker/images/alpine-3.21.ext4

# Copy navarisd from build stage.
COPY --from=build /navarisd /usr/local/bin/navarisd

# Copy entrypoint script.
COPY scripts/firecracker-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
```

**Notes:**
- The `FC_VERSION` ARG pins the Firecracker release. The exact kernel filename (`vmlinux-6.1-${ARCH}.bin`) may differ per release — check the release assets at `https://github.com/firecracker-microvm/firecracker/releases/tag/v1.12.0` and adjust the filename if needed.
- The `mke2fs -d` command packs the Alpine root (`/`) into a 512M ext4 image. The output is written to `/tmp` first to avoid self-reference (the output file being included in its own source tree). Alpine is ~15MB; 512M is generous — adjust if needed.
- The `rc_sys="lxc"` sed command configures OpenRC to skip cgroup/hardware checks that fail inside a VM with minimal kernel. If the sed target doesn't exist in the Alpine version, it's silently skipped.

- [ ] **Step 2: Verify Dockerfile syntax**

Run: `docker build --check -f Dockerfile.navarisd-firecracker .` (or just verify no syntax errors with `docker build --target build -f Dockerfile.navarisd-firecracker .`)

Note: A full build requires KVM and network access for downloads. If building locally without KVM, verify the build stage compiles by running just the first stage.

- [ ] **Step 3: Commit**

```bash
git add Dockerfile.navarisd-firecracker
git commit -m "feat: add Firecracker integration Dockerfile (3-stage build)"
```

---

### Task 3: Docker Compose

**Files:**
- Create: `docker-compose.integration-firecracker.yml`

**Reference files:**
- `docker-compose.integration.yml` — existing Incus compose file to mirror

- [ ] **Step 1: Create the compose file**

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

Key differences from `docker-compose.integration.yml`:
- No separate `incus` service — Firecracker runs inside navarisd's container
- No shared volumes — everything is self-contained
- `privileged: true` + `/dev/kvm` device for Firecracker/jailer
- `NAVARIS_BASE_IMAGE: alpine-3.21` (flat name, no slash)
- `NAVARIS_SKIP_SNAPSHOTS` and `NAVARIS_SKIP_PORTS` env vars for skip guards

- [ ] **Step 2: Commit**

```bash
git add docker-compose.integration-firecracker.yml
git commit -m "feat: add Firecracker integration Docker Compose"
```

---

### Task 4: Makefile Targets

**Files:**
- Modify: `Makefile`

**Reference:** The existing Makefile has 4 targets for Incus. Mirror the pattern for Firecracker.

- [ ] **Step 1: Add Firecracker targets to the Makefile**

Append to the end of `Makefile` (after the existing `integration-logs` target). **Use tabs for recipe indentation, not spaces.**

```makefile
FC_COMPOSE_FILE := docker-compose.integration-firecracker.yml

.PHONY: integration-test-firecracker integration-env-firecracker integration-env-firecracker-down integration-logs-firecracker

integration-test-firecracker:
	@docker compose -f $(FC_COMPOSE_FILE) --profile test up \
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

- [ ] **Step 2: Verify Makefile parses**

Run: `make -n integration-test-firecracker`
Expected: Prints the commands without executing them (no "missing separator" error)

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat: add Firecracker integration Makefile targets"
```

---

### Task 5: Test Skip Guards

**Files:**
- Modify: `test/integration/e2e_test.go` (snapshot section starts after line 114)
- Modify: `test/integration/snapshot_test.go:12` (top of TestSnapshotRestoreToSandbox)
- Modify: `test/integration/image_test.go:60` (top of TestImagePromoteFromSnapshot)
- Modify: `test/integration/port_test.go:12` (top of TestPortPublishListDelete)

**Context:**
- The Firecracker provider stubs return `ErrNotImplemented` for: `CreateSnapshot`, `RestoreSnapshot`, `DeleteSnapshot`, `PublishSnapshotAsImage`, `DeleteImage`, `GetImageInfo`, `PublishPort`, `UnpublishPort` (see `internal/provider/firecracker/stubs.go`)
- Tests that call these stubs will fail with 500 errors
- We add env-var-gated skip guards so these tests are skipped when running against Firecracker
- `NAVARIS_SKIP_SNAPSHOTS=1` — skips snapshot/image-from-snapshot tests
- `NAVARIS_SKIP_PORTS=1` — skips port forwarding tests
- Note: `TestImageRegisterListGetDelete` does NOT need a skip guard. Its `t.Cleanup` calls `DeleteImage` (a stub), but the cleanup handles the error gracefully (`t.Logf("warning: ...")` + early return). The main test body is metadata-only (no provider calls).

- [ ] **Step 1: Add skip guard to `test/integration/snapshot_test.go`**

Add at the very top of `TestSnapshotRestoreToSandbox`, before any other code:

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Skip("snapshots not supported by this backend")
}
```

The file does NOT import `"os"` — add it to the import block.

- [ ] **Step 2: Add skip guard to `test/integration/e2e_test.go`**

The snapshot section begins at line 116 with `snapOp, err := c.CreateSnapshot(...)`. We need to wrap lines 116–198 (from `CreateSnapshot` through the end of the test) in a skip check.

Insert these lines immediately after line 114 (`t.Logf("sandbox stopped")`), before the `snapOp` line:

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Log("end-to-end lifecycle test passed (snapshot section skipped)")
    return
}
```

Note: The existing `defer` at line 77 (`_, _ = c.DestroySandboxAndWait(...)`) will handle sandbox cleanup on return — do NOT add a manual destroy inside the skip guard.

The file already imports `"os"` — no import change needed. (Verify: check the imports.)

- [ ] **Step 3: Add skip guard to `test/integration/image_test.go`**

Add at the very top of `TestImagePromoteFromSnapshot` (line 61), before any other code:

```go
if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
    t.Skip("snapshots not supported by this backend")
}
```

The file does NOT import `"os"` — add it to the import block.

- [ ] **Step 4: Add skip guard to `test/integration/port_test.go`**

Add at the very top of `TestPortPublishListDelete` (line 13), before any other code:

```go
if os.Getenv("NAVARIS_SKIP_PORTS") == "1" {
    t.Skip("port forwarding not supported by this backend")
}
```

The file does NOT import `"os"` — add it to the import block.

- [ ] **Step 5: Verify tests still compile**

Run: `CGO_ENABLED=0 go test -tags integration -c -o /dev/null ./test/integration/`
Expected: Compiles successfully (exit 0)

- [ ] **Step 6: Verify skip guards work**

Run: `NAVARIS_SKIP_SNAPSHOTS=1 NAVARIS_SKIP_PORTS=1 go test -tags integration -list '.*' ./test/integration/ 2>&1 | head -20`
Expected: Lists test names without errors (we can't run them without a running navarisd, but compilation confirms the skip code is valid)

- [ ] **Step 7: Commit**

```bash
git add test/integration/e2e_test.go test/integration/snapshot_test.go test/integration/image_test.go test/integration/port_test.go
git commit -m "feat: add skip guards for snapshot and port tests (Firecracker compat)"
```

---

### Task 6: CI Workflow

**Files:**
- Create: `.github/workflows/integration-firecracker.yml`

**Reference:** `.github/workflows/integration.yml` — existing Incus CI workflow

- [ ] **Step 1: Create the workflow file**

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

- [ ] **Step 2: Verify YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/integration-firecracker.yml'))"`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/integration-firecracker.yml
git commit -m "ci: add Firecracker integration test workflow"
```

---

### Task 7: Local Smoke Test

This task verifies the entire stack works end-to-end on the developer's machine. **Requires KVM access** (bare-metal Linux or a VM with nested virtualization).

- [ ] **Step 1: Check KVM availability**

Run: `ls -la /dev/kvm`
Expected: Character device exists and is accessible. If not, skip this task — CI will validate.

- [ ] **Step 2: Build and run integration tests**

Run: `make integration-test-firecracker`
Expected: All non-skipped tests pass. Skipped tests show `--- SKIP:` with the expected messages.

- [ ] **Step 3: Verify skip messages appear**

In the test output, confirm these skip messages are present:
- `snapshots not supported by this backend` (from snapshot_test.go, image_test.go)
- `port forwarding not supported by this backend` (from port_test.go)
- `end-to-end lifecycle test passed (snapshot section skipped)` (from e2e_test.go, in verbose output — this is a `t.Log`, not a `t.Skip`)

- [ ] **Step 4: Verify health log confirms Firecracker backend**

In the test output, confirm: `backend=firecracker healthy=true`

- [ ] **Step 5: Clean up**

Run: `make integration-env-firecracker-down` (in case any containers are still running)

- [ ] **Step 6: Verify existing Incus tests still work**

Run: `make integration-test` (the Incus integration tests should be unaffected by the skip guards since the env vars are not set)
