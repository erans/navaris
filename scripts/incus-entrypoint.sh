#!/bin/bash
set -eu

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

# Initialize Incus on first run (use a sentinel file for idempotency).
if [ ! -f /var/lib/incus/.initialized ]; then
    echo "Initializing Incus..."

    # Storage driver selection. Default "dir" preserves the historical
    # integration-test behavior. Set INCUS_STORAGE_DRIVER=btrfs to exercise
    # the CoW path (used by the integration-incus-cow leg).
    driver="${INCUS_STORAGE_DRIVER:-dir}"
    case "$driver" in
        dir)
            storage_pool_block=$(cat <<'EOF'
storage_pools:
  - name: default
    driver: dir
EOF
)
            ;;
        btrfs)
            # We provision the btrfs filesystem ourselves and hand Incus a
            # ready-mounted directory. The alternatives — letting Incus
            # auto-create a loop file via the source/size config — fail in
            # a Docker volume because Incus's btrfs driver checks that the
            # *parent* of the source path is already btrfs and refuses with
            # "Provided path does not reside on a btrfs filesystem". By
            # pre-mounting the loop file at the source path we satisfy that
            # check and the preseed becomes a plain "use this btrfs mount".
            btrfs_img=/var/lib/incus/btrfs.img
            btrfs_mount=/var/lib/incus/storage-pools/default
            if [ "$(stat -f -c %T "$btrfs_mount" 2>/dev/null)" != "btrfs" ]; then
                echo "Provisioning btrfs loop at $btrfs_mount (img=$btrfs_img, 5 GiB)..."
                mkdir -p "$btrfs_mount"
                if [ ! -f "$btrfs_img" ]; then
                    truncate -s 5G "$btrfs_img"
                    mkfs.btrfs -q "$btrfs_img"
                fi
                mount -o loop "$btrfs_img" "$btrfs_mount"
                echo "  $btrfs_mount fstype=$(stat -f -c %T "$btrfs_mount")"
            fi
            storage_pool_block=$(cat <<'EOF'
storage_pools:
  - name: default
    driver: btrfs
    config:
      source: /var/lib/incus/storage-pools/default
EOF
)
            ;;
        *)
            echo "ERROR: unsupported INCUS_STORAGE_DRIVER=$driver (expected dir|btrfs)" >&2
            exit 1
            ;;
    esac

    cat <<PRESEED | incus admin init --preseed
networks:
  - name: incusbr0
    type: bridge
    config:
      ipv4.address: 10.75.0.1/24
      ipv4.nat: "true"
      ipv6.address: none
${storage_pool_block}
profiles:
  - name: default
    devices:
      root:
        type: disk
        path: /
        pool: default
      eth0:
        type: nic
        network: incusbr0
        name: eth0
PRESEED
    touch /var/lib/incus/.initialized
fi

# Pre-pull base image for integration tests so it's available as a local alias.
# The Incus Go client API doesn't support remote:alias syntax — only the CLI does.
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

echo "Incus ready."
touch /tmp/incus-ready
# Keep the container alive by waiting on the daemon process.
wait "$INCUSD_PID"
