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
touch /tmp/incus-ready

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
# Use &&/|| to prevent set -e from exiting before we can log.
wait -n "$INCUSD_PID" "$NAVARISD_PID" && exit_code=0 || exit_code=$?
echo "Process exited with code $exit_code, shutting down."
exit "$exit_code"
