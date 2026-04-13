# All-in-One Docker Image Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a single Docker image that runs Incus + Firecracker + navarisd in one container with graceful KVM degradation.

**Architecture:** Multi-stage Dockerfile pulls pre-built kernel/rootfs from existing Firecracker image, installs Incus on Ubuntu 24.04, builds Go binaries locally. Unified entrypoint starts incusd, detects KVM, then launches navarisd with appropriate flags. Compose file provides KVM profile for Firecracker enablement.

**Tech Stack:** Docker, Docker Compose, bash, Go build tags

**Spec:** `docs/superpowers/specs/2026-04-03-allinone-docker-image-design.md`

---

### Task 1: Create the unified entrypoint script

**Files:**
- Create: `scripts/allinone-entrypoint.sh`
- Reference: `scripts/incus-entrypoint.sh` (Incus init logic)
- Reference: `scripts/firecracker-entrypoint.sh` (cgroup v2 + IP forwarding logic)

- [ ] **Step 1: Write the entrypoint script**

Create `scripts/allinone-entrypoint.sh`:

```bash
#!/bin/bash
set -eu

# ---- Incus Setup (from incus-entrypoint.sh) ----

# Start incusd in the background so we can initialise it.
incusd --group incus-admin &
INCUSD_PID=$!

# Wait for the daemon to become reachable.
echo "Waiting for incusd to become reachable..."
ready=false
for i in $(seq 1 30); do
    if incus info &>/dev/null; then
        ready=true
        break
    fi
    sleep 1
done
if [ "$ready" != "true" ]; then
    echo "ERROR: incusd did not become reachable after 30 seconds" >&2
    exit 1
fi

# Initialize Incus on first run (idempotent via sentinel file).
if [ ! -f /var/lib/incus/.initialized ]; then
    echo "Initializing Incus..."
    cat <<PRESEED | incus admin init --preseed
storage_pools:
  - name: default
    driver: dir
profiles:
  - name: default
    devices:
      root:
        type: disk
        path: /
        pool: default
PRESEED
    touch /var/lib/incus/.initialized
fi

# Pre-pull Incus images (runs to completion before starting navarisd).
if [ -n "${INCUS_PRELOAD_IMAGE:-}" ]; then
    IFS=',' read -ra IMAGES <<< "${INCUS_PRELOAD_IMAGE}"
    for img in "${IMAGES[@]}"; do
        img="$(echo "$img" | xargs)"
        local_alias="${img#*:}"
        if ! incus image alias list --format csv | grep -q "^${local_alias},"; then
            echo "Pre-pulling image ${img} -> local alias ${local_alias}..."
            incus image copy "${img}" local: --alias "${local_alias}"
            echo "Image pre-pull complete: ${local_alias}"
        fi
    done
fi

echo "Incus ready."

# ---- Firecracker Setup (from firecracker-entrypoint.sh) ----

# Enable IP forwarding for VM networking.
sysctl -w net.ipv4.ip_forward=1

# Create base directory for Firecracker VM data.
mkdir -p /srv/firecracker

# On cgroup v2, move PID 1 into a child cgroup so the root cgroup is free
# for the jailer to manage.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    echo "cgroup v2 detected, setting up delegation..."
    mkdir -p /sys/fs/cgroup/init
    echo $$ > /sys/fs/cgroup/init/cgroup.procs
    for c in $(cat /sys/fs/cgroup/cgroup.controllers); do
        echo "+$c" > /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || true
    done
fi

# ---- Assemble navarisd flags ----

NAVARIS_AUTH_TOKEN="${NAVARIS_AUTH_TOKEN:-}"
NAVARIS_LOG_LEVEL="${NAVARIS_LOG_LEVEL:-info}"
NAVARIS_LISTEN="${NAVARIS_LISTEN:-:8080}"
NAVARIS_DB_PATH="${NAVARIS_DB_PATH:-/var/lib/navaris/navaris.db}"
NAVARIS_ENABLE_JAILER="${NAVARIS_ENABLE_JAILER:-false}"

# Ensure DB directory exists.
mkdir -p "$(dirname "$NAVARIS_DB_PATH")"

ARGS=(
    --listen="$NAVARIS_LISTEN"
    --db-path="$NAVARIS_DB_PATH"
    --log-level="$NAVARIS_LOG_LEVEL"
    --incus-socket=/var/lib/incus/unix.socket
)

if [ -n "$NAVARIS_AUTH_TOKEN" ]; then
    ARGS+=(--auth-token="$NAVARIS_AUTH_TOKEN")
fi

# Enable Firecracker if /dev/kvm is available.
if [ -c /dev/kvm ]; then
    echo "KVM detected, enabling Firecracker backend."
    ARGS+=(
        --firecracker-bin=/usr/local/bin/firecracker
        --jailer-bin=/usr/local/bin/jailer
        --kernel-path=/opt/firecracker/vmlinux
        --image-dir=/opt/firecracker/images
        --chroot-base=/srv/firecracker
        --snapshot-dir=/srv/firecracker/snapshots
        --enable-jailer="$NAVARIS_ENABLE_JAILER"
    )
else
    echo "KVM not available, Firecracker backend disabled."
fi

# ---- Start navarisd ----

navarisd "${ARGS[@]}" &
NAVARISD_PID=$!

echo "navarisd started (PID $NAVARISD_PID)."

# Wait on both processes — exit if either crashes.
wait -n "$INCUSD_PID" "$NAVARISD_PID"
exit_code=$?
echo "Process exited with code $exit_code, shutting down."
exit "$exit_code"
```

- [ ] **Step 2: Make it executable**

Run: `chmod +x scripts/allinone-entrypoint.sh`

- [ ] **Step 3: Verify with shellcheck**

Run: `shellcheck scripts/allinone-entrypoint.sh`
Expected: No errors (warnings about `read -ra` on POSIX are fine — we use bash explicitly).

- [ ] **Step 4: Commit**

```bash
git add scripts/allinone-entrypoint.sh
git commit -m "feat: add unified entrypoint for all-in-one Docker image"
```

---

### Task 2: Create the all-in-one Dockerfile

**Files:**
- Create: `Dockerfile`
- Reference: `Dockerfile.navarisd-firecracker` (Firecracker binary download pattern)
- Reference: `Dockerfile.incus` (Incus installation from Zabbly PPA)

- [ ] **Step 1: Write the Dockerfile**

Create `Dockerfile`:

```dockerfile
# ---- Stage 1: Build Go binaries ----
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags firecracker,incus -o /navarisd ./cmd/navarisd
RUN CGO_ENABLED=0 go build -o /navaris ./cmd/navaris

# ---- Stage 2: Runtime ----
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH="/opt/incus/bin:${PATH}"
ENV LD_LIBRARY_PATH="/opt/incus/lib"

# Install Incus from Zabbly PPA.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gpg && \
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --dearmor -o /etc/apt/keyrings/zabbly.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/zabbly.gpg] https://pkgs.zabbly.com/incus/stable $(. /etc/os-release && echo ${VERSION_CODENAME}) main" \
        > /etc/apt/sources.list.d/zabbly-incus.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends incus && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Firecracker runtime dependencies.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        iproute2 iptables e2fsprogs procps wget && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Download Firecracker and jailer binaries.
ARG FC_VERSION=v1.15.0
RUN ARCH=$(uname -m) && \
    wget -q "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz" \
        -O /tmp/fc.tgz && \
    tar -xzf /tmp/fc.tgz -C /tmp && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/firecracker-${FC_VERSION}-${ARCH} /usr/local/bin/firecracker && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/jailer-${FC_VERSION}-${ARCH} /usr/local/bin/jailer && \
    chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer && \
    rm -rf /tmp/fc.tgz /tmp/release-*

# Copy kernel and rootfs images from pre-built Firecracker image.
ARG FC_IMAGE=navarisd-firecracker
RUN mkdir -p /opt/firecracker/images
COPY --from=${FC_IMAGE} /opt/firecracker/vmlinux /opt/firecracker/vmlinux
COPY --from=${FC_IMAGE} /opt/firecracker/images/ /opt/firecracker/images/

# Copy Go binaries.
COPY --from=build /navarisd /usr/local/bin/navarisd
COPY --from=build /navaris /usr/local/bin/navaris

# Copy entrypoint.
COPY scripts/allinone-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 2: Verify the Dockerfile parses correctly**

Run: `docker build --check -f Dockerfile .` (or `docker buildx build --check -f Dockerfile .`)
If `--check` is not available, just verify the syntax looks correct by inspection — the real build test happens in Task 4.

- [ ] **Step 3: Commit**

```bash
git add Dockerfile
git commit -m "feat: add all-in-one Dockerfile with Incus + Firecracker"
```

---

### Task 3: Create the docker-compose.yml

**Files:**
- Create: `docker-compose.yml`
- Reference: `docker-compose.integration-mixed.yml` (privilege/cgroup/device patterns)

- [ ] **Step 1: Write the compose file**

Create `docker-compose.yml`:

```yaml
# All-in-one Navaris: Incus + Firecracker in a single container.
#
# Usage:
#   Incus only:              docker compose up navaris
#   Incus + Firecracker:     docker compose --profile kvm up
#
# Requirements:
#   Docker Engine 25+, Docker Compose v2.15+
#   Firecracker requires /dev/kvm on the host (Linux with KVM support)

services:
  navaris:
    build:
      context: .
      dockerfile: Dockerfile
    privileged: true
    cgroup: host
    ports:
      - "${NAVARIS_HOST_PORT:-8080}:8080"
    environment:
      NAVARIS_AUTH_TOKEN: ${NAVARIS_AUTH_TOKEN:-}
      NAVARIS_LOG_LEVEL: ${NAVARIS_LOG_LEVEL:-info}
      INCUS_PRELOAD_IMAGE: ${INCUS_PRELOAD_IMAGE:-images:alpine/3.21,images:debian/12}
    volumes:
      - navaris-data:/var/lib/navaris
      - incus-data:/var/lib/incus
      - firecracker-data:/srv/firecracker
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/v1/health"]
      interval: 5s
      timeout: 5s
      retries: 30
      start_period: 45s

  navaris-kvm:
    extends:
      service: navaris
    devices:
      - /dev/kvm:/dev/kvm
    profiles:
      - kvm

volumes:
  navaris-data:
  incus-data:
  firecracker-data:
```

- [ ] **Step 2: Validate compose file syntax**

Run: `docker compose -f docker-compose.yml config --quiet`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add docker-compose.yml
git commit -m "feat: add docker-compose.yml for all-in-one local usage"
```

---

### Task 4: Update the Makefile

**Files:**
- Modify: `Makefile` (append new targets after existing content)

- [ ] **Step 1: Add the new targets**

Append to `Makefile`:

```makefile
# ---- All-in-one Docker image ----

.PHONY: docker-build docker-up docker-up-kvm docker-down

docker-build:
	docker build -f Dockerfile.navarisd-firecracker -t navarisd-firecracker .
	docker build -t navaris .

docker-up: docker-build
	docker compose up navaris

docker-up-kvm: docker-build
	docker compose --profile kvm up

docker-down:
	docker compose --profile kvm down
```

- [ ] **Step 2: Verify make targets parse**

Run: `make -n docker-build`
Expected: Prints the two `docker build` commands without executing them.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat: add Makefile targets for all-in-one Docker image"
```

---

### Task 5: Build and smoke test

This task verifies the full build chain works. Requires a machine with Docker installed.

- [ ] **Step 1: Build the Firecracker artifact image**

Run: `docker build -f Dockerfile.navarisd-firecracker -t navarisd-firecracker .`
Expected: Completes successfully (may take 15-20 min on first build due to kernel compilation).

- [ ] **Step 2: Build the all-in-one image**

Run: `docker build -t navaris .`
Expected: Completes successfully in ~2-3 min. Key things to verify in output:
- Incus installed from Zabbly PPA
- Firecracker binary downloaded
- Kernel and rootfs images copied from `navarisd-firecracker`
- Go binaries compiled with `firecracker,incus` tags

- [ ] **Step 3: Test without KVM (Incus only)**

Run: `docker compose up -d navaris`
Wait ~45s for healthcheck, then:
Run: `docker compose exec navaris wget -q -O- --header "Authorization: Bearer " http://localhost:8080/v1/health`
Expected: JSON response showing `incus` provider healthy, no `firecracker` provider.

Run: `docker compose down`

- [ ] **Step 4: Test with KVM (if available)**

Run: `docker compose --profile kvm up -d`
Wait ~45s for healthcheck, then:
Run: `docker compose exec navaris-kvm wget -q -O- --header "Authorization: Bearer " http://localhost:8080/v1/health`
Expected: JSON response showing both `incus` and `firecracker` providers healthy.

Run: `docker compose --profile kvm down`

- [ ] **Step 5: Test CLI is available inside container**

Run: `docker compose up -d navaris`
Run: `docker compose exec navaris navaris --help`
Expected: CLI help output showing available commands.

Run: `docker compose down`

- [ ] **Step 6: Commit (if any fixes were needed)**

If any fixes were applied during smoke testing, commit them:
```bash
git add -A
git commit -m "fix: address issues found during all-in-one smoke test"
```
