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
            # Loop-backed btrfs pool. Incus enforces that any source under
            # /var/lib/incus must be exactly
            # /var/lib/incus/storage-pools/<pool-name> (no .img suffix, no
            # extra path component). Incus creates the loop file at that
            # path and mkfs.btrfs's it on first init.
            storage_pool_block=$(cat <<'EOF'
storage_pools:
  - name: default
    driver: btrfs
    config:
      source: /var/lib/incus/storage-pools/default
      size: 5GiB
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
