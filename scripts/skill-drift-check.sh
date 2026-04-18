#!/usr/bin/env bash
# Verify every navaris command referenced in a SKILL.md resolves on the
# checked-in CLI. Exit 0 if every reference is live, non-zero otherwise.
#
# Usage: skill-drift-check.sh <skills-dir>
#
# Env:
#   NAVARIS   path to the navaris binary (default: ./navaris, built on the fly)

set -euo pipefail

SKILLS_DIR="${1:-skills}"
if [ ! -d "$SKILLS_DIR" ]; then
    echo "usage: $0 <skills-dir>" >&2
    exit 2
fi

NAVARIS="${NAVARIS:-./navaris}"
if [ ! -x "$NAVARIS" ]; then
    echo "building navaris CLI for drift check..." >&2
    go build -o ./navaris ./cmd/navaris
    NAVARIS="./navaris"
fi

groups=(project sandbox snapshot session image operation port)

fail=0

# 1. Every top-level group must appear in the root --help output.
root_help=$("$NAVARIS" --help 2>&1)
for g in "${groups[@]}"; do
    if ! echo "$root_help" | grep -qE "^  $g\b"; then
        echo "drift: 'navaris $g' not found in root --help (top-level group missing)" >&2
        fail=1
    fi
done

# 2. Every `navaris <group> <subverb>` invocation mentioned in any SKILL.md
#    must accept --help on the current CLI.
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

# Regex anchored on the known groups — avoids false positives from prose like
# "navaris uses..." or "navaris supports...".
pat="navaris +(project|sandbox|snapshot|session|image|operation|port) +[a-z][a-z-]*"
grep -rhoE "$pat" "$SKILLS_DIR" | sort -u > "$tmp"

while read -r line; do
    [ -z "$line" ] && continue
    cmd=${line#navaris }
    # cmd is "<group> <subverb>", e.g. "sandbox create" or "sandbox wait-state".
    group="${cmd%% *}"
    subverb="${cmd#* }"
    # Verify the subverb appears as a listed subcommand in the group's --help
    # output. This avoids Cobra's --help short-circuit behaviour, which always
    # exits 0 regardless of whether the subverb actually exists.
    if ! "$NAVARIS" "$group" --help 2>&1 | grep -qE "^  $subverb\b"; then
        echo "drift: 'navaris $cmd' not found in 'navaris $group --help'" >&2
        fail=1
    fi
done < "$tmp"

if [ "$fail" -eq 1 ]; then
    echo "FAIL: skill command references drift from the checked-in CLI" >&2
    exit 1
fi

echo "OK: all skill command references resolve"
