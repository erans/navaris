#!/usr/bin/env bash
# Verify every navaris command referenced in a SKILL.md resolves on the
# checked-in CLI. Exit 0 if every reference is live, non-zero otherwise.
#
# Usage: skill-drift-check.sh <skills-dir>
#
# Env:
#   NAVARIS   path to the navaris binary (default: ./navaris, built on the fly)
#
# Catches: removed/renamed top-level groups and removed/renamed subcommands.
# Does NOT catch: flag-level drift (renamed/removed flags), changed flag types,
# changed default values, or changes to flag short forms.
# Limitation: the subverb regex ([a-z]{2,}...) still matches short prose words
# (e.g. "is", "to") appearing after a group name; avoid embedding navaris group
# names in prose sentences inside SKILL.md files.

set -euo pipefail

SKILLS_DIR="${1:-skills}"
if [ ! -d "$SKILLS_DIR" ]; then
    echo "usage: $0 <skills-dir>" >&2
    exit 2
fi

# Declare cleanup variables up front so the cleanup function can reference them
# safely regardless of which code path runs.
tmp=""
auto_built_navaris=""

cleanup() {
    if [ -n "${tmp:-}" ]; then rm -f "$tmp"; fi
    if [ -n "${auto_built_navaris:-}" ]; then rm -f "$auto_built_navaris"; fi
}
trap cleanup EXIT

NAVARIS="${NAVARIS:-./navaris}"
if [ ! -x "$NAVARIS" ]; then
    echo "building navaris CLI for drift check..." >&2
    auto_built_navaris=$(mktemp -t navaris-drift-XXXXXX)
    go build -o "$auto_built_navaris" ./cmd/navaris
    NAVARIS="$auto_built_navaris"
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

# Regex anchored on the known groups — avoids false positives from prose like
# "navaris uses..." or "navaris supports...".
# Subverb must be ≥2 chars (and each hyphen-separated part ≥2 chars) to
# reduce false matches on short prose words like "is", "a", "x".
pat="navaris +(project|sandbox|snapshot|session|image|operation|port) +[a-z]{2,}([-][a-z]{2,})*"

set +e
grep -rhoE "$pat" "$SKILLS_DIR" | sort -u > "$tmp"
rc=$?
set -e
if [ "$rc" -gt 1 ]; then
    echo "drift: grep failed with exit $rc" >&2
    exit "$rc"
fi

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
