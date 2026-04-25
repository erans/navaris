#!/bin/sh
# Verifies that btrfs at /srv/firecracker actually shares extents after a
# reflink call. This complements the strict-mode startup probe in navarisd:
#
#   - The startup probe (--storage-mode=reflink in BuildRegistry) calls
#     ioctl(FICLONE) and checks that it returns success. So a healthy
#     navarisd proves "the kernel ACCEPTS FICLONE on this filesystem".
#
#   - This script proves "the kernel actually DEDUPLICATES extents after
#     FICLONE". A buggy kernel that returned success from FICLONE but
#     silently full-copied would pass the startup probe and fail this.
#
# We do this self-contained instead of inspecting post-test artifacts
# because the test-runner cleans up its sandboxes/snapshots on exit, so
# by the time we get here only the staged images remain — and those are
# independent files with no reflinks between them by design.
#
# Exit codes:
#   0 — kernel correctly shares extents after reflink
#   1 — preconditions not met (no btrfs, no source image, etc.)
#   2 — kernel returned success but did not share extents

set -eu

target="${1:-/srv/firecracker}"

if ! command -v btrfs >/dev/null 2>&1; then
    echo "ERROR: btrfs command not in path" >&2
    exit 1
fi

fstype=$(stat -f -c %T "$target" 2>/dev/null || echo unknown)
if [ "$fstype" != "btrfs" ]; then
    echo "ERROR: $target is not btrfs (fstype=$fstype)" >&2
    exit 1
fi

# Use one of the staged images as the source — we know it's a real file
# of substantial size on this btrfs.
src=$(find "$target/images" -maxdepth 1 -type f -name '*.ext4' 2>/dev/null | head -n 1)
if [ -z "$src" ]; then
    echo "ERROR: no source image under $target/images — entrypoint should have staged one" >&2
    exit 1
fi
src_size=$(stat -c %s "$src")
echo "Source: $src ($src_size bytes)"

dst="$target/.cow-sharing-check.ext4"
# Clean up the dst on every exit path including failures.
trap 'rm -f "$dst" 2>/dev/null || true' EXIT INT TERM

# `cp --reflink=always` issues FICLONE under the hood. If the kernel
# doesn't support it on this fs, cp exits non-zero — but our strict
# probe already passed, so this should succeed.
if ! cp --reflink=always "$src" "$dst" 2>&1; then
    echo "FAIL: cp --reflink=always failed unexpectedly (strict probe should have caught this earlier)"
    exit 2
fi

echo
echo "btrfs filesystem du --raw output for src + clone:"
btrfs filesystem du --raw "$src" "$dst"

# Aggregate across the two files. With a working reflink:
#   - Total ≈ 2 × src_size (logical size)
#   - Exclusive should be small (most blocks are shared)
#   - Set shared should be the bulk of the data
#
# With a buggy "FICLONE-but-actually-copies" kernel:
#   - Total ≈ 2 × src_size
#   - Exclusive ≈ Total
#   - Set shared = 0
btrfs filesystem du --raw "$src" "$dst" | awk -v ssize="$src_size" '
    BEGIN { total = 0; exclusive = 0; shared = 0 }
    NR == 1 { next }                       # header
    /^[[:space:]]*Total/ { next }          # final summary
    /^[[:space:]]*$/ { next }
    {
        gsub(/-/, "0", $1); gsub(/-/, "0", $2); gsub(/-/, "0", $3)
        total     += $1 + 0
        exclusive += $2 + 0
        shared    += $3 + 0
    }
    END {
        printf "\nAggregate: total=%d exclusive=%d shared=%d\n", total, exclusive, shared
        if (shared <= 0) {
            print "FAIL: cp --reflink=always succeeded but no shared extents — kernel returned success without deduplicating"
            exit 2
        }
        # The clone should account for nearly all of src_size as shared.
        # Allow some slack for btrfs metadata granularity.
        threshold = int(ssize * 0.8)
        if (shared < threshold) {
            printf "FAIL: shared %d < 80%% of source size %d (threshold %d)\n", shared, ssize, threshold
            exit 2
        }
        printf "OK: shared %d >= 80%% of source size %d (threshold %d) — kernel correctly deduplicates after FICLONE\n", shared, ssize, threshold
    }
'
