#!/bin/bash
set -eu

# Enable IP forwarding for VM networking.
sysctl -w net.ipv4.ip_forward=1

# Create chroot base for jailer.
mkdir -p /srv/firecracker

# On cgroup v2, the jailer needs to write cgroup.subtree_control, which
# requires the current process to NOT be in the target cgroup. Move PID 1
# into a child cgroup so the root cgroup is free for the jailer to manage.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    mkdir -p /sys/fs/cgroup/init
    echo $$ > /sys/fs/cgroup/init/cgroup.procs
    # Enable controllers the jailer expects.
    for c in $(cat /sys/fs/cgroup/cgroup.controllers); do
        echo "+$c" > /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || true
    done
fi

# Exec navarisd with all arguments.
exec navarisd "$@"
