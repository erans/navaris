#!/bin/sh
set -e

if ! command -v systemctl >/dev/null 2>&1; then
    exit 0
fi

case "$1" in
    remove|purge|deconfigure)
        systemctl stop navarisd.service >/dev/null 2>&1 || true
        ;;
esac
