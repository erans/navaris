# Debian 12 Image Support Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Debian 12 (Bookworm) as a second guest OS image for both Incus and Firecracker providers, with CI matrix testing against both Alpine and Debian.

**Architecture:** New Dockerfile stage builds a Debian 12 ext4 rootfs with systemd + navaris-agent. Incus entrypoint gains multi-image preload. CI workflows use GitHub Actions matrix to test both images in parallel.

**Tech Stack:** Docker multi-stage builds, systemd, ext4 (mke2fs), GitHub Actions matrix, Docker Compose env substitution

**Spec:** `docs/superpowers/specs/2026-03-31-debian-12-image-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `Dockerfile.navarisd-firecracker` | Modify | Add `rootfs-debian` stage + copy in runtime stage |
| `internal/provider/firecracker/sandbox.go` | Modify | Bump MemSizeMib 128 → 256 |
| `scripts/incus-entrypoint.sh` | Modify | Multi-image preload loop |
| `docker-compose.integration.yml` | Modify | Parameterize NAVARIS_BASE_IMAGE, add debian/12 preload |
| `docker-compose.integration-firecracker.yml` | Modify | Parameterize NAVARIS_BASE_IMAGE |
| `.github/workflows/integration.yml` | Modify | Add matrix strategy |
| `.github/workflows/integration-firecracker.yml` | Modify | Add matrix strategy |

---

### Task 1: Add Debian 12 rootfs stage to Firecracker Dockerfile

**Files:**
- Modify: `Dockerfile.navarisd-firecracker:45-74` (after Alpine rootfs stage)
- Modify: `Dockerfile.navarisd-firecracker:100-102` (runtime stage COPY)

- [ ] **Step 1: Add the `rootfs-debian` stage after the Alpine `rootfs` stage**

Insert after line 74 (end of Alpine rootfs stage), before the runtime stage:

```dockerfile
# ---- Stage 3b: Build Debian 12 rootfs ext4 image ----
FROM debian:12-slim AS rootfs-debian

RUN apt-get update && apt-get install -y --no-install-recommends \
        systemd systemd-sysv e2fsprogs fuse3 \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Copy the agent binary.
COPY --from=build /navaris-agent /usr/local/bin/navaris-agent

# Systemd unit for the agent.
RUN printf '[Unit]\nDescription=Navaris guest agent\nAfter=network.target\n\n[Service]\nExecStart=/usr/local/bin/navaris-agent\nRestart=on-failure\nStandardOutput=journal+console\nStandardError=journal+console\n\n[Install]\nWantedBy=multi-user.target\n' \
    > /etc/systemd/system/navaris-agent.service && \
    systemctl enable navaris-agent.service

# Pack the Debian rootfs into an ext4 image.
RUN mkdir /staging && \
    for d in bin etc home lib lib64 media mnt opt root run sbin srv usr var; do \
        [ -d "/$d" ] && cp -a "/$d" /staging/; \
    done && \
    mkdir -p /staging/tmp && chmod 1777 /staging/tmp && \
    mkdir -p /staging/proc /staging/sys /staging/dev && \
    mke2fs -t ext4 -d /staging -L rootfs /tmp/debian-12.ext4 1024M && \
    mkdir -p /opt/firecracker/images && \
    mv /tmp/debian-12.ext4 /opt/firecracker/images/debian-12.ext4
```

- [ ] **Step 2: Add COPY for Debian image in the runtime stage**

In the runtime stage, after the existing Alpine rootfs COPY line (`COPY --from=rootfs ...`), add:

```dockerfile
COPY --from=rootfs-debian /opt/firecracker/images/debian-12.ext4 /opt/firecracker/images/debian-12.ext4
```

- [ ] **Step 3: Verify Dockerfile syntax**

Run: `docker build --check -f Dockerfile.navarisd-firecracker .` (or just verify with `docker build --target rootfs-debian -f Dockerfile.navarisd-firecracker .` if `--check` is not available — this validates the parse without building everything).

- [ ] **Step 4: Commit**

```bash
git add Dockerfile.navarisd-firecracker
git commit -m "feat: add Debian 12 rootfs stage to Firecracker Dockerfile

Adds rootfs-debian stage with systemd, systemd-sysv, e2fsprogs, fuse3.
Creates navaris-agent.service systemd unit and packs 1024M ext4 image."
```

---

### Task 2: Increase Firecracker VM memory to 256 MiB

**Files:**
- Modify: `internal/provider/firecracker/sandbox.go:139`

- [ ] **Step 1: Change MemSizeMib from 128 to 256**

In `internal/provider/firecracker/sandbox.go`, line 139, change:

```go
// Before:
MemSizeMib: fcsdk.Int64(128),
// After:
MemSizeMib: fcsdk.Int64(256),
```

- [ ] **Step 2: Verify compilation**

Run: `CGO_ENABLED=0 go build -tags firecracker ./internal/provider/firecracker/...`

- [ ] **Step 3: Commit**

```bash
git add internal/provider/firecracker/sandbox.go
git commit -m "feat: increase Firecracker VM memory to 256 MiB

Debian 12 with systemd requires ~200-256 MiB to boot reliably.
Also benefits Alpine workloads with more headroom."
```

---

### Task 3: Update Incus entrypoint for multi-image preload

**Files:**
- Modify: `scripts/incus-entrypoint.sh:44-51`

- [ ] **Step 1: Replace single-image preload with comma-separated loop**

In `scripts/incus-entrypoint.sh`, replace lines 44-51:

```bash
# Before (lines 44-51):
if [ -n "${INCUS_PRELOAD_IMAGE:-}" ]; then
    local_alias="${INCUS_PRELOAD_IMAGE#*:}"
    if ! incus image alias list --format csv | grep -q "^${local_alias},"; then
        echo "Pre-pulling image ${INCUS_PRELOAD_IMAGE} -> local alias ${local_alias}..."
        incus image copy "${INCUS_PRELOAD_IMAGE}" local: --alias "${local_alias}"
        echo "Image pre-pull complete."
    fi
fi
```

With:

```bash
# After:
if [ -n "${INCUS_PRELOAD_IMAGE:-}" ]; then
    IFS=',' read -ra IMAGES <<< "${INCUS_PRELOAD_IMAGE}"
    for img in "${IMAGES[@]}"; do
        img="$(echo "$img" | xargs)"  # trim whitespace
        local_alias="${img#*:}"
        if ! incus image alias list --format csv | grep -q "^${local_alias},"; then
            echo "Pre-pulling image ${img} -> local alias ${local_alias}..."
            incus image copy "${img}" local: --alias "${local_alias}"
            echo "Image pre-pull complete: ${local_alias}"
        fi
    done
fi
```

- [ ] **Step 2: Verify syntax**

Run: `bash -n scripts/incus-entrypoint.sh`

Expected: no output (clean parse).

- [ ] **Step 3: Commit**

```bash
git add scripts/incus-entrypoint.sh
git commit -m "feat: support comma-separated INCUS_PRELOAD_IMAGE

Loops over multiple images to preload both Alpine and Debian
for integration tests."
```

---

### Task 4: Parameterize NAVARIS_BASE_IMAGE in compose files

**Files:**
- Modify: `docker-compose.integration.yml:9,57`
- Modify: `docker-compose.integration-firecracker.yml:45`

- [ ] **Step 1: Update docker-compose.integration.yml**

Two changes in `docker-compose.integration.yml`:

1. Line 9 — add `debian/12` to preload list:
```yaml
# Before:
INCUS_PRELOAD_IMAGE: images:alpine/3.21
# After:
INCUS_PRELOAD_IMAGE: images:alpine/3.21,images:debian/12
```

2. Line 57 — parameterize NAVARIS_BASE_IMAGE:
```yaml
# Before:
NAVARIS_BASE_IMAGE: alpine/3.21
# After:
NAVARIS_BASE_IMAGE: ${NAVARIS_BASE_IMAGE:-alpine/3.21}
```

- [ ] **Step 2: Update docker-compose.integration-firecracker.yml**

Line 45 — parameterize NAVARIS_BASE_IMAGE:
```yaml
# Before:
NAVARIS_BASE_IMAGE: alpine-3.21
# After:
NAVARIS_BASE_IMAGE: ${NAVARIS_BASE_IMAGE:-alpine-3.21}
```

- [ ] **Step 3: Verify compose files parse correctly**

Run:
```bash
docker compose -f docker-compose.integration.yml config --quiet
docker compose -f docker-compose.integration-firecracker.yml config --quiet
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.integration.yml docker-compose.integration-firecracker.yml
git commit -m "feat: parameterize NAVARIS_BASE_IMAGE in compose files

Supports CI matrix by allowing env var override with defaults.
Adds debian/12 to Incus preload list."
```

---

### Task 5: Add GitHub Actions matrix to both workflow files

**Files:**
- Modify: `.github/workflows/integration.yml`
- Modify: `.github/workflows/integration-firecracker.yml`

- [ ] **Step 1: Update integration.yml (Incus)**

Replace the full file content with:

```yaml
name: Integration Tests

on:
  push:
    branches: [main]
  pull_request:

jobs:
  integration:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    strategy:
      matrix:
        image: [alpine/3.21, debian/12]
    steps:
      - uses: actions/checkout@v4
      - name: Run integration tests (${{ matrix.image }})
        run: make integration-test
        env:
          NAVARIS_BASE_IMAGE: ${{ matrix.image }}
```

- [ ] **Step 2: Update integration-firecracker.yml**

Replace the full file content with:

```yaml
name: Integration Tests (Firecracker)
on:
  push:
    branches: [main]
  pull_request:

jobs:
  integration:
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    strategy:
      matrix:
        image: [alpine-3.21, debian-12]
    steps:
      - uses: actions/checkout@v4
      - name: Enable KVM
        run: |
          if [ ! -e /dev/kvm ]; then
            echo "::error::KVM not available on this runner"
            exit 1
          fi
          sudo chmod 666 /dev/kvm
      - name: Run integration tests (${{ matrix.image }})
        run: make integration-test-firecracker
        env:
          NAVARIS_BASE_IMAGE: ${{ matrix.image }}
```

- [ ] **Step 3: Validate workflow YAML syntax**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/integration.yml')); yaml.safe_load(open('.github/workflows/integration-firecracker.yml')); print('OK')"
```

Expected: `OK`

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/integration.yml .github/workflows/integration-firecracker.yml
git commit -m "feat: add CI matrix for Alpine and Debian integration tests

Both workflows now run the full test suite against alpine and debian-12
images in parallel using GitHub Actions matrix strategy."
```

---

### Task 6: Push and verify CI

- [ ] **Step 1: Push to remote**

```bash
git push
```

- [ ] **Step 2: Watch CI runs**

Check that all four matrix jobs appear:
- Integration Tests / integration (alpine/3.21)
- Integration Tests / integration (debian/12)
- Integration Tests (Firecracker) / integration (alpine-3.21)
- Integration Tests (Firecracker) / integration (debian-12)

Run: `gh run list --limit 4`

- [ ] **Step 3: Monitor for completion**

All 4 jobs should pass. The Alpine jobs should behave identically to before (same image, more memory). The Debian jobs validate that the new image boots, navaris-agent starts via systemd, and all 21 integration tests pass.
