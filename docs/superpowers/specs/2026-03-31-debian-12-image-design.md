# Debian 12 Image Support for Incus and Firecracker Providers

**Date**: 2026-03-31
**Status**: Approved

## Summary

Add Debian 12 (Bookworm) as a second supported guest OS image for both the Incus and Firecracker providers, alongside the existing Alpine 3.21. Run the full integration test suite against both images in CI using a GitHub Actions matrix strategy.

## Context

Navaris currently ships a single guest OS image (Alpine 3.21) for each provider. Adding Debian 12 broadens compatibility testing and gives users a mainstream, systemd-based image. The two providers have different image delivery mechanisms:

- **Firecracker**: ext4 rootfs files baked into the Docker image at build time
- **Incus**: images pulled from a remote registry at container startup

## Design

### 1. Firecracker: Debian 12 Rootfs Dockerfile Stage

Add a new stage `rootfs-debian` in `Dockerfile.navarisd-firecracker` after the existing `rootfs` (Alpine) stage. The new stage:

- Uses `debian:12-slim` as the base
- Installs `systemd`, `systemd-sysv`, `e2fsprogs`, and `fuse3`
- Copies the `navaris-agent` binary from the build stage
- Creates a systemd unit file at `/etc/systemd/system/navaris-agent.service` and enables it
- Packs the rootfs into a 1024MB ext4 image (`debian-12.ext4`)

The runtime stage copies both images:
```
/opt/firecracker/images/alpine-3.21.ext4   (existing)
/opt/firecracker/images/debian-12.ext4     (new)
```

**Systemd unit file**:
```ini
[Unit]
Description=Navaris guest agent
After=network.target

[Service]
ExecStart=/usr/local/bin/navaris-agent
Restart=on-failure
StandardOutput=journal+console
StandardError=journal+console

[Install]
WantedBy=multi-user.target
```

**Key differences from Alpine stage**:
- systemd instead of OpenRC (no `/etc/init.d/` script, no `rc-update`)
- `systemd-sysv` is required — it provides the `/sbin/init` symlink to `/lib/systemd/systemd` that the kernel's default init path expects
- Includes `lib64` in the staging `for d in ...` loop (Debian x86_64 has it, Alpine does not)
- 1024MB image size (Debian minimal ~400MB; Alpine ~50MB)
- No `rc_sys="lxc"` sed hack (systemd auto-detects container/VM environment)

**VM memory**: The Firecracker provider currently hardcodes `MemSizeMib: 128` in `sandbox.go`. Debian 12 with systemd requires ~200-256 MiB to boot reliably. Increase the default to `256` MiB. This also benefits Alpine (more headroom for workloads) and is still very lightweight.

**Kernel dependencies**: systemd requires `CONFIG_CGROUPS`, `CONFIG_TMPFS`, `CONFIG_DEVTMPFS`, `CONFIG_INOTIFY_USER`, `CONFIG_SIGNALFD`, `CONFIG_TIMERFD`, and `CONFIG_EPOLL`. All of these are enabled by the x86_64 `defconfig` that the kernel build starts from. No kernel config changes are needed. The existing `CONFIG_FUSE_FS=y` set in the Dockerfile already covers FUSE for both images.

**Docker image size impact**: Adding the 1024MB Debian ext4 increases the uncompressed navarisd-firecracker Docker image by ~1 GiB. Compressed (layer gzip), this is roughly 300-400 MB additional. Acceptable for CI — the kernel build layer is already the dominant build-time cost.

### 2. Incus: Multi-Image Preload

Update `scripts/incus-entrypoint.sh` to support comma-separated values in `INCUS_PRELOAD_IMAGE`. The current single-image logic:

```bash
local_alias="${INCUS_PRELOAD_IMAGE#*:}"
incus image copy "${INCUS_PRELOAD_IMAGE}" local: --alias "${local_alias}"
```

Becomes a loop:
```bash
IFS=',' read -ra IMAGES <<< "${INCUS_PRELOAD_IMAGE}"
for img in "${IMAGES[@]}"; do
    img="$(echo "$img" | xargs)"  # trim whitespace
    local_alias="${img#*:}"
    incus image copy "${img}" local: --alias "${local_alias}"
done
```

Update `docker-compose.integration.yml` to preload both images:
```yaml
INCUS_PRELOAD_IMAGE: images:alpine/3.21,images:debian/12
```

Both images are always preloaded regardless of which image the matrix job tests. This doubles the Incus startup time (~60s → ~120s) but avoids parameterizing the Incus container per matrix job, which would require rebuilding the container or adding a sidecar. The preload cost is acceptable since it happens once per job.

No Go code changes needed — the Incus provider already resolves images by alias at sandbox creation time.

### 3. CI: GitHub Actions Matrix

Both workflow files add a `strategy.matrix` with image entries. The `NAVARIS_BASE_IMAGE` env var is set in the workflow step's `env:` block; Docker Compose picks it up via `${NAVARIS_BASE_IMAGE:-default}` variable substitution from the host environment.

**`.github/workflows/integration-firecracker.yml`**:
```yaml
strategy:
  matrix:
    image: [alpine-3.21, debian-12]
steps:
  # ...
  - name: Run integration tests (${{ matrix.image }})
    run: make integration-test-firecracker
    env:
      NAVARIS_BASE_IMAGE: ${{ matrix.image }}
```

**`.github/workflows/integration.yml`**:
```yaml
strategy:
  matrix:
    image: [alpine/3.21, debian/12]
steps:
  # ...
  - name: Run integration tests (${{ matrix.image }})
    run: make integration-test
    env:
      NAVARIS_BASE_IMAGE: ${{ matrix.image }}
```

**Compose files** change hardcoded image values to env-var-with-default:
- `docker-compose.integration.yml`: `NAVARIS_BASE_IMAGE: ${NAVARIS_BASE_IMAGE:-alpine/3.21}`
- `docker-compose.integration-firecracker.yml`: `NAVARIS_BASE_IMAGE: ${NAVARIS_BASE_IMAGE:-alpine-3.21}`

Makefile targets remain unchanged — compose inherits `NAVARIS_BASE_IMAGE` from the shell environment.

## Files Modified

| File | Change |
|------|--------|
| `Dockerfile.navarisd-firecracker` | Add `rootfs-debian` stage; copy `debian-12.ext4` in runtime stage |
| `internal/provider/firecracker/sandbox.go` | Increase `MemSizeMib` from 128 to 256 |
| `scripts/incus-entrypoint.sh` | Loop over comma-separated `INCUS_PRELOAD_IMAGE` |
| `docker-compose.integration.yml` | Parameterize `NAVARIS_BASE_IMAGE`; add `debian/12` to preload list |
| `docker-compose.integration-firecracker.yml` | Parameterize `NAVARIS_BASE_IMAGE` |
| `.github/workflows/integration.yml` | Add matrix strategy with `alpine/3.21` and `debian/12` |
| `.github/workflows/integration-firecracker.yml` | Add matrix strategy with `alpine-3.21` and `debian-12` |

## What Does NOT Change

- No Go code changes beyond the MemSizeMib bump (domain model, providers, tests, CLI unchanged)
- No kernel changes (same vmlinux works for both guest OSes; defconfig includes all systemd dependencies)
- No new test files (same test suite runs against each image via matrix)
- Alpine 3.21 behavior is unchanged (memory increase is beneficial)
