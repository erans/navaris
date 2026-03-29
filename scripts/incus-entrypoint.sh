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
    # Use preseed to skip network creation (nftables not available in container).
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

# Pre-pull base image for integration tests so it's available as a local alias.
# The Incus Go client API doesn't support remote:alias syntax — only the CLI does.
if [ -n "${INCUS_PRELOAD_IMAGE:-}" ]; then
    local_alias="${INCUS_PRELOAD_IMAGE#*:}"
    if ! incus image alias list --format csv | grep -q "^${local_alias},"; then
        echo "Pre-pulling image ${INCUS_PRELOAD_IMAGE} -> local alias ${local_alias}..."
        incus image copy "${INCUS_PRELOAD_IMAGE}" local: --alias "${local_alias}"
        echo "Image pre-pull complete."
    fi
fi

echo "Incus ready."
touch /tmp/incus-ready
# Keep the container alive by waiting on the daemon process.
wait "$INCUSD_PID"
