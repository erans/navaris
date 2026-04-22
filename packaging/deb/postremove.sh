#!/bin/sh
set -e

if ! command -v systemctl >/dev/null 2>&1; then
    exit 0
fi

case "$1" in
    remove|purge)
        systemctl disable navarisd.service >/dev/null 2>&1 || true
        ;;
esac

systemctl daemon-reload >/dev/null 2>&1 || true
