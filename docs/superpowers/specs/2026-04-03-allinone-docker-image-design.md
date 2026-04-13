# All-in-One Docker Image Design

## Overview

A single Docker image that runs both Incus and Firecracker backends in one container, with graceful degradation when KVM is unavailable. Provides a zero-friction onboarding experience for developers, demos, and production-lite deployments.

## Requirements

- Docker Engine 25+ and Docker Compose v2.15+ (required for `cgroup: host` syntax)

## Goals

- One `docker compose up` gives a fully functional Navaris with both backends
- Graceful degradation: Incus always available, Firecracker enabled only if `/dev/kvm` is present
- Persistent state across restarts via named volumes
- Configurable via environment variables
- Does not modify or break any existing Dockerfiles, compose files, or workflows

## Non-Goals

- High-availability or multi-node deployment
- Running without `--privileged` (Incus requires it)
- Building kernel/rootfs from source in this image (sourced from existing Firecracker image)

## Architecture

### Build Dependency

```
Dockerfile.navarisd-firecracker    (slow: kernel + rootfs build, ~15-20 min)
         │
         │  COPY --from= (kernel + rootfs artifacts only)
         ▼
Dockerfile                         (fast: Go build + Incus install, ~2-3 min)
  └── Stage 1: builds navarisd + navaris CLI from source
  └── Stage 2: copies kernel/rootfs from FC_IMAGE, builds runtime
```

The all-in-one `Dockerfile` references the Firecracker image via a build arg (`FC_IMAGE`) to pull pre-built kernel and rootfs artifacts only. Go binaries (`navarisd`, `navaris`) are built locally in Stage 1. This avoids duplicating the expensive kernel compilation.

### Dockerfile Structure

**Stage 1: Build Go binaries** (`golang:1.26-bookworm`)
- Compiles `navarisd` with `-tags firecracker,incus`
- Compiles `navaris` CLI (no build tags needed)
- Does NOT build `navaris-agent` (already embedded in rootfs images)

**Stage 2: Runtime** (`ubuntu:24.04`)
- Ubuntu 24.04 base — required for Incus via Zabbly PPA. This diverges from the Firecracker-only Dockerfile's `debian:bookworm-slim` base. This is safe because the Firecracker binary is a statically linked release and the rootfs ext4 images are self-contained.
- Installs Incus from Zabbly
- Installs Firecracker runtime dependencies: `iproute2`, `iptables`, `e2fsprogs`, `procps`, `wget`, `ca-certificates`
- Downloads Firecracker + jailer binaries (v1.15.0)
- `COPY --from=${FC_IMAGE}` the kernel (`/opt/firecracker/vmlinux`) and rootfs images (`/opt/firecracker/images/`)
- `COPY --from=build` the `navarisd` and `navaris` binaries
- Copies unified entrypoint script

### Unified Entrypoint (`scripts/allinone-entrypoint.sh`)

Merges logic from `incus-entrypoint.sh` and `firecracker-entrypoint.sh`:

1. **Start incusd** in background, wait up to 30s for readiness
2. **Initialize Incus** on first run (idempotent via `/var/lib/incus/.initialized` sentinel):
   - Preseed with `dir`-backed storage pool, no network (nftables unavailable in container)
3. **Pre-pull Incus images** from `INCUS_PRELOAD_IMAGE` env var (comma-separated). This runs to completion before starting navarisd, so no background process interference with `wait`.
4. **Set up Firecracker prerequisites**:
   - `sysctl -w net.ipv4.ip_forward=1`
   - `mkdir -p /srv/firecracker`
   - cgroup v2 delegation (move PID to child cgroup, enable subtree controllers)
5. **Detect `/dev/kvm`**:
   - Present: add Firecracker flags: `--firecracker-bin=/usr/local/bin/firecracker`, `--jailer-bin=/usr/local/bin/jailer`, `--kernel-path=/opt/firecracker/vmlinux`, `--image-dir=/opt/firecracker/images`, `--chroot-base=/srv/firecracker`, `--snapshot-dir=/srv/firecracker/snapshots`, `--enable-jailer=${NAVARIS_ENABLE_JAILER}`
   - Absent: log "KVM not available, Firecracker backend disabled", skip Firecracker flags entirely
6. **Assemble navarisd command** with:
   - Incus flags (always): `--incus-socket=/var/lib/incus/unix.socket`
   - Firecracker flags (conditional on KVM, see above)
   - Common flags: `--listen=${NAVARIS_LISTEN}`, `--db-path=${NAVARIS_DB_PATH}`, `--auth-token=${NAVARIS_AUTH_TOKEN}`, `--log-level=${NAVARIS_LOG_LEVEL}`
   - `--host-interface` is omitted (auto-detection is sufficient inside the container — the default interface, typically `eth0`, is correct for masquerade)
7. **Start navarisd** in background, capture PID
8. **Wait on both processes** using `wait -n "$INCUSD_PID" "$NAVARISD_PID"` (bash 5.2 ships with Ubuntu 24.04, which supports `wait -n` with explicit PIDs) — container exits if either crashes

Note on `NAVARIS_ENABLE_JAILER`: the navarisd binary reads CLI flags, not environment variables. The entrypoint maps this env var to the `--enable-jailer` flag explicitly. The default is `false` because the jailer's chroot/uid isolation conflicts with Docker's own namespacing.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NAVARIS_AUTH_TOKEN` | (empty = no auth) | Bearer token for API authentication |
| `NAVARIS_LOG_LEVEL` | `info` | Log level: debug, info, warn, error |
| `NAVARIS_LISTEN` | `:8080` | HTTP server bind address |
| `NAVARIS_DB_PATH` | `/var/lib/navaris/navaris.db` | SQLite database file path |
| `INCUS_PRELOAD_IMAGE` | `images:alpine/3.21,images:debian/12` | Comma-separated images to pre-pull |
| `NAVARIS_ENABLE_JAILER` | `false` | Maps to `--enable-jailer` flag (disabled in Docker) |

Optional flags (`--concurrency`, `--gc-interval`, `--otlp-endpoint`, `--otlp-protocol`, `--service-name`) use their navarisd defaults and are not exposed as environment variables. Users who need them can override the entrypoint or extend the compose file.

### Docker Compose (`docker-compose.yml`)

The base compose file does NOT include `/dev/kvm` in `devices` — this ensures it works on hosts without KVM. A separate profile enables Firecracker support.

```yaml
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

  # Profile for KVM-enabled hosts (adds /dev/kvm for Firecracker support)
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

Usage:
- **Without KVM** (Incus only): `docker compose up navaris`
- **With KVM** (Incus + Firecracker): `docker compose --profile kvm up`

Notes:
- `privileged: true` + `cgroup: host`: required for Incus
- `start_period: 45s`: accounts for first-run Incus init + image pre-pull
- `wget` is installed in the image for healthcheck use
- Environment variables use `${VAR:-default}` syntax to honor defaults from the entrypoint
- Three named volumes separate DB, Incus, and Firecracker state

### Makefile Targets

| Target | Description |
|--------|-------------|
| `docker-build` | Builds `navarisd-firecracker` image, then builds all-in-one image |
| `docker-up` | Runs `docker-build` then `docker compose up navaris` |
| `docker-up-kvm` | Runs `docker-build` then `docker compose --profile kvm up` |
| `docker-down` | Runs `docker compose --profile kvm down` (covers both profiles) |

## Files Changed

### New Files
- `Dockerfile` — all-in-one multi-stage image
- `docker-compose.yml` — single-service compose for local use (with KVM profile)
- `scripts/allinone-entrypoint.sh` — unified entrypoint

### Modified Files
- `Makefile` — add `docker-build`, `docker-up`, `docker-up-kvm`, `docker-down` targets

### Untouched Files
- `Dockerfile.navarisd` — Incus-only deployments
- `Dockerfile.navarisd-firecracker` — Firecracker-only deployments + artifact source
- `Dockerfile.incus` — integration tests
- `Dockerfile.test` — integration tests
- `docker-compose.integration*.yml` — all integration test compose files
- `scripts/incus-entrypoint.sh` — used by Dockerfile.incus
- `scripts/firecracker-entrypoint.sh` — used by Dockerfile.navarisd-firecracker
