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

# Initialize Incus on first run (idempotent).
if ! incus query /1.0/instances &>/dev/null 2>&1; then
    echo "Initializing Incus..."
    incus admin init --auto
fi

echo "Incus ready."
# Keep the container alive by waiting on the daemon process.
wait "$INCUSD_PID"
