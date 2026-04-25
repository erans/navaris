#!/bin/bash
set -eu

# Enable IP forwarding for VM networking.
sysctl -w net.ipv4.ip_forward=1

# Create base directory for Firecracker VM data (used by both jailer and non-jailer modes).
mkdir -p /srv/firecracker

# Optional btrfs loop mount over /srv/firecracker for CoW e2e testing.
# Set NAVARIS_FC_BTRFS_LOOP=1 to activate. The loop file lives at
# /var/lib/navaris-fc-btrfs.img and is mkfs'd on first activation.
# Idempotent: a no-op if /srv/firecracker is already a btrfs mount.
if [ "${NAVARIS_FC_BTRFS_LOOP:-0}" = "1" ]; then
    if [ "$(stat -f -c %T /srv/firecracker 2>/dev/null)" != "btrfs" ]; then
        loop_img="${NAVARIS_FC_BTRFS_LOOP_IMG:-/var/lib/navaris-fc-btrfs.img}"
        loop_size="${NAVARIS_FC_BTRFS_LOOP_SIZE:-5G}"
        echo "Setting up btrfs loop mount at /srv/firecracker (img=$loop_img size=$loop_size)..."
        if [ ! -f "$loop_img" ]; then
            truncate -s "$loop_size" "$loop_img"
            mkfs.btrfs -q "$loop_img"
        fi
        # mount auto-allocates a loop device when given a regular file.
        mount -o loop "$loop_img" /srv/firecracker
        echo "  /srv/firecracker is now btrfs (fstype=$(stat -f -c %T /srv/firecracker))"
    else
        echo "/srv/firecracker is already btrfs; skipping loop setup."
    fi

    # Stage rootfs images onto the btrfs mount so the image-dir storage
    # root also passes the strict-reflink probe. The Dockerfile bakes
    # images into /opt/firecracker/images on the container's overlay
    # filesystem, which has no FICLONE — so we copy them once into
    # /srv/firecracker/images and point --image-dir there in the compose.
    if [ -d /opt/firecracker/images ] && [ ! -d /srv/firecracker/images ]; then
        echo "Staging rootfs images from /opt/firecracker/images to /srv/firecracker/images..."
        mkdir -p /srv/firecracker/images
        cp -a /opt/firecracker/images/. /srv/firecracker/images/
        echo "  staged $(ls -1 /srv/firecracker/images | wc -l) image(s)"
    fi
fi

# On cgroup v2, the jailer needs to write cgroup.subtree_control, which
# requires the current process to NOT be in the target cgroup. Move PID 1
# into a child cgroup so the root cgroup is free for the jailer to manage.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    echo "cgroup v2 detected, setting up delegation..."
    echo "  controllers: $(cat /sys/fs/cgroup/cgroup.controllers)"
    echo "  current cgroup: $(cat /proc/self/cgroup)"

    mkdir -p /sys/fs/cgroup/init
    echo $$ > /sys/fs/cgroup/init/cgroup.procs

    # Enable all available controllers for child cgroups.
    for c in $(cat /sys/fs/cgroup/cgroup.controllers); do
        echo "+$c" > /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || true
    done
    echo "  subtree_control: $(cat /sys/fs/cgroup/cgroup.subtree_control)"
else
    echo "cgroup v1 detected (or cgroup not mounted)"
    ls -la /sys/fs/cgroup/ || true
fi

# Exec navarisd with all arguments.
exec navarisd "$@"
