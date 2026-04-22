#!/bin/sh
set -e

if ! command -v systemctl >/dev/null 2>&1; then
    exit 0
fi

systemctl daemon-reload >/dev/null 2>&1 || true

case "$1" in
    configure)
        if systemctl is-active --quiet navarisd.service; then
            systemctl try-restart navarisd.service >/dev/null 2>&1 || true
        fi
        ;;
esac
