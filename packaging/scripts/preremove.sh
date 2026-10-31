#!/bin/sh
set -e
# Stop on removal, but not while upgrading (the new version restarts it).
if [ "$1" = "remove" ] && [ -d /run/systemd/system ]; then
    systemctl disable --now idrometer.service >/dev/null 2>&1 || true
fi
