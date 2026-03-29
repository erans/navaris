#!/bin/bash
set -eu

# Initialize Incus on first run (idempotent).
if ! incus info &>/dev/null; then
    echo "Initializing Incus..."
    incus admin init --auto
fi

echo "Starting incusd..."
exec incusd --group incus-admin
