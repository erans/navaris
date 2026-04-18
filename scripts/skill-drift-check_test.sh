#!/usr/bin/env bash
# Tests for skill-drift-check.sh.
#
# Strategy: build the CLI once, then run the detector against:
#   1. The real skills/ directory — expect exit 0.
#   2. A temp directory with the golden "bad" SKILL.md — expect non-zero.
#
# Runs from the repo root.

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

SCRIPT=scripts/skill-drift-check.sh
if [ ! -x "$SCRIPT" ]; then
    echo "FAIL: $SCRIPT does not exist or is not executable"
    exit 1
fi

echo "building navaris CLI for drift test..."
go build -o ./navaris-drift-test ./cmd/navaris
export NAVARIS="$PWD/navaris-drift-test"
trap 'rm -f "$PWD/navaris-drift-test"' EXIT

# 1. Real skills/ should pass.
if ! "$SCRIPT" skills >/dev/null 2>&1; then
    echo "FAIL: drift check reports drift on the real skills/ directory"
    exit 1
fi
echo "OK: real skills/ passes drift check"

# 2. Golden bad fixture should fail.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"; rm -f "$PWD/navaris-drift-test"' EXIT
mkdir -p "$tmp/bad-skill"
cp scripts/testdata/bad-skill/SKILL.md "$tmp/bad-skill/SKILL.md"
rc=0
err=$("$SCRIPT" "$tmp" 2>&1 >/dev/null) || rc=$?
if [ "${rc:-0}" -eq 0 ]; then
    echo "FAIL: drift check passed on the golden bad fixture (expected failure)"
    exit 1
fi
if ! echo "$err" | grep -qF "frobnicate"; then
    echo "FAIL: drift check failed for unexpected reason: $err"
    exit 1
fi
echo "OK: golden bad fixture fails drift check (with the right error)"

echo "PASS: drift detector tests"
