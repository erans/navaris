# Native Linux Install

Navaris can be installed directly on a Linux host without Docker. The intended
release format is a `.tar.gz` containing the Navaris binaries plus packaging
artifacts for `systemd`.

## Scope

Initial native packaging targets:

- Linux `amd64`
- Linux `arm64`
- `systemd`-managed hosts such as Debian

The main Navaris release should include:

- `navarisd`
- `navaris`
- `navaris-mcp`
- `navaris-agent`
- `scripts/install-native.sh`
- `packaging/systemd/navarisd.service`
- `packaging/systemd/navarisd-launch.sh`
- `packaging/systemd/navarisd.env.example`

The main release should **not** include:

- Firecracker guest kernels
- Firecracker rootfs images

## Firecracker Runtime Strategy

Navaris treats Firecracker itself as a host runtime dependency, not as a binary
vendored into the main Navaris release tarball.

The supported bootstrap path is:

1. Install Navaris from the release tarball.
2. If Firecracker support is desired, run
   `scripts/install-firecracker-runtime.sh`.
3. Provide guest assets separately: a kernel at `NAVARIS_KERNEL_PATH` and a
   rootfs image directory at `NAVARIS_IMAGE_DIR`.

This keeps the app release small and lets us pin Firecracker independently of
guest kernels and images.

## Install Firecracker And Jailer

The installer downloads a pinned upstream Firecracker release for the host
architecture, verifies the published SHA256 file, and installs:

- `firecracker`
- `jailer`

Example:

```bash
sudo ./scripts/install-firecracker-runtime.sh \
  --version v1.15.1 \
  --bin-dir /usr/local/lib/navaris/firecracker/bin \
  --link-dir /usr/local/bin
```

By default the script supports:

- `amd64` -> upstream `x86_64`
- `arm64` -> upstream `aarch64`

## Guest Asset Layout

Firecracker guest assets are operator-managed and intentionally separate from
the runtime installer.

Suggested layout:

```text
/var/lib/navaris/firecracker/
  vmlinux
  images/
  snapshots/
  vm/
```

Suggested environment file values:

```bash
NAVARIS_FIRECRACKER_BIN=/usr/local/lib/navaris/firecracker/bin/firecracker
NAVARIS_JAILER_BIN=/usr/local/lib/navaris/firecracker/bin/jailer
NAVARIS_KERNEL_PATH=/var/lib/navaris/firecracker/vmlinux
NAVARIS_IMAGE_DIR=/var/lib/navaris/firecracker/images
NAVARIS_CHROOT_BASE=/var/lib/navaris/firecracker/vm
NAVARIS_SNAPSHOT_DIR=/var/lib/navaris/firecracker/snapshots
NAVARIS_ENABLE_JAILER=true
```

If these are unset, `navarisd` should run without the Firecracker backend and
continue serving whichever backends are configured, such as Incus.

## Storage Backend (Copy-on-Write)

Navaris clones rootfs files via a pluggable storage backend (full reference:
[docs/storage-backends.md](storage-backends.md)). On a CoW-capable host the
clone is a metadata-only `ioctl(FICLONE)` — N sandboxes spawned from one
template share blocks instead of paying N × rootfsSize.

Recommended host filesystems for the storage roots
(`NAVARIS_IMAGE_DIR`, `NAVARIS_CHROOT_BASE`, `NAVARIS_SNAPSHOT_DIR`):

- **btrfs** — native CoW. Consider `nodatacow` on the storage subvolume to
  reduce fragmentation under heavy random writes inside guest rootfs images.
- **XFS** — must be created with `mkfs.xfs -m reflink=1`. Reflink cannot be
  enabled in place on an existing XFS filesystem.
- **bcachefs** — recent kernels.
- **ext4 / tmpfs / NFS** — no reflink; clones fall back to a full byte copy.

Place all three storage roots on the same filesystem to maximise CoW
coverage. Select the mode at startup with `NAVARIS_STORAGE_MODE` (`auto` —
default — probes each root; `copy` forces full copy; `reflink` is a hard
precondition that fails startup on a non-CoW root).

Memory-CoW forking of running Firecracker sandboxes is exposed via
`POST /v1/sandboxes/{id}/fork` — see [docs/sandbox-fork.md](sandbox-fork.md).

For Incus, navaris does not implement CoW directly — Incus uses its own
storage pools. The daemon checks the configured pool driver at startup and
warns on `dir`/`lvm`. Set `NAVARIS_INCUS_STRICT_POOL_COW=true` to upgrade
the warning to a startup error.

## Install From A Release Tarball

After extracting the release archive, run:

```bash
sudo ./scripts/install-native.sh
```

That installer:

- places the binaries under `/usr/local/bin`
- places support files under `/usr/local/lib/navaris`
- installs the `systemd` unit
- creates `/etc/navaris/navarisd.env` if it does not already exist
- creates `/var/lib/navaris` and the Firecracker directory skeleton

Useful installer options:

```bash
sudo ./scripts/install-native.sh --enable --start
sudo ./scripts/install-native.sh --prefix /opt/navaris --skip-systemd
```

## systemd

The repo includes a `systemd` wrapper script so native installs can configure
`navarisd` through `/etc/navaris/navarisd.env` without requiring the daemon
itself to read env vars directly.

If you prefer to install the files manually instead of using the installer:

```bash
sudo install -d /etc/navaris /usr/local/lib/navaris
sudo install -m 0644 packaging/systemd/navarisd.service /etc/systemd/system/navarisd.service
sudo install -m 0755 packaging/systemd/navarisd-launch.sh /usr/local/lib/navaris/navarisd-launch.sh
sudo install -m 0644 packaging/systemd/navarisd.env.example /etc/navaris/navarisd.env
sudo systemctl daemon-reload
sudo systemctl enable --now navarisd
```

## Release Automation

The repo ships a tag-driven workflow at
`.github/workflows/release.yml`.

On version tags such as `v0.1.0`, GitHub Actions will:

1. Build release tarballs and `.deb` packages for `linux-amd64` and `linux-arm64`.
2. Include Navaris binaries and packaging files.
3. Generate `SHA256SUMS`.
4. Publish or update the matching GitHub Release.

The archive assembly is handled by `scripts/package-release.sh`.

Firecracker guest kernels and rootfs images remain outside those main release
assets.
