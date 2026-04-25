#!/bin/sh
# Verifies that snapshot/sandbox rootfs files on /srv/firecracker share
# btrfs extents with their source images — i.e. that ReflinkBackend is
# actually doing copy-on-write at the kernel level rather than full
# byte copies that happen to look right.
#
# Strategy: btrfs filesystem du --raw reports Total / Exclusive / Set
# shared bytes per file. A reflinked clone has Exclusive ≈ 0 and
# Set shared ≈ Total. A full copy has Exclusive ≈ Total and Set
# shared ≈ 0.
#
# Pass criterion: at least one rootfs.ext4 under /srv/firecracker has
# Set shared > 0. That's a direct, kernel-level assertion that FICLONE
# actually deduplicated extents on this filesystem.
#
# Exit codes:
#   0 — sharing confirmed
#   1 — preconditions not met (no rootfs files, not btrfs, etc.)
#   2 — sharing NOT detected (clones look like full copies)

set -eu

target="${1:-/srv/firecracker}"

if ! command -v btrfs >/dev/null 2>&1; then
    echo "ERROR: btrfs command not available in this image" >&2
    exit 1
fi

fstype=$(stat -f -c %T "$target" 2>/dev/null || echo unknown)
if [ "$fstype" != "btrfs" ]; then
    echo "ERROR: $target is not btrfs (fstype=$fstype)" >&2
    exit 1
fi

# Find every rootfs.ext4 under target. The integration test creates
# at least one snapshot (test/integration/snapshot_test.go), so by
# the time this script runs there should be at least one snapshot
# rootfs in addition to the staged image rootfs.
files=$(find "$target" -type f -name 'rootfs.ext4' -o -type f -name '*.ext4' 2>/dev/null | sort -u)
if [ -z "$files" ]; then
    echo "ERROR: no rootfs.ext4 / *.ext4 files under $target — integration test should have populated some" >&2
    exit 1
fi
file_count=$(printf "%s\n" "$files" | wc -l)
echo "Inspecting $file_count file(s) under $target:"
echo "$files" | sed 's/^/  /'
echo

echo "btrfs filesystem du --raw output:"
btrfs filesystem du --raw $files

# Re-run with awk to compute aggregates and assert on shared bytes.
btrfs filesystem du --raw $files | awk '
    BEGIN { found = 0; total_shared = 0; total_apparent = 0; total_exclusive = 0 }
    NR == 1 { next }                       # header row "     Total   Exclusive  Set shared  Filename"
    /^[[:space:]]*Total/ { next }          # final summary row
    /^[[:space:]]*$/ { next }              # blank lines
    {
        # btrfs prints "-" for entries it cannot inspect (e.g. holes-only files);
        # treat those as zero for safety.
        gsub(/-/, "0", $1); gsub(/-/, "0", $2); gsub(/-/, "0", $3)
        total = $1 + 0; exclusive = $2 + 0; shared = $3 + 0
        total_apparent  += total
        total_exclusive += exclusive
        total_shared    += shared
        if (shared > 0) { found++ }
    }
    END {
        printf "\nAggregate: total=%d exclusive=%d shared=%d (%d file(s) with shared extents)\n", \
            total_apparent, total_exclusive, total_shared, found
        if (found == 0 || total_shared == 0) {
            print "FAIL: no shared extents detected — reflink/CoW not working"
            exit 2
        }
        # Stronger sanity: shared should be a meaningful fraction of total.
        # With 1 image + N snapshots that fully share, total_shared ≈
        # (N+1) × image_size and total_exclusive should be small. We
        # require total_shared >= 50% of total_apparent.
        threshold = int(total_apparent / 2)
        if (total_shared < threshold) {
            printf "FAIL: shared %d < 50%% of apparent %d (threshold %d) — most data is exclusive, looks copied\n", total_shared, total_apparent, threshold
            exit 2
        }
        printf "OK: shared %d >= 50%% of apparent %d (threshold %d) — extent sharing confirmed\n", total_shared, total_apparent, threshold
    }
'
