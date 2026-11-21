#!/bin/bash -eu
# Called by the backend while holding the rootfs configuration transaction.
set -o pipefail
# shellcheck source=run/lib/paths.sh
source "$(dirname "$0")/lib/paths.sh"

case "${1:-}" in
  prepare)
    # Install everything the new backend needs (mount helper, rsync, ...).
    # Fail before stopping archiving, so a switch that cannot work is
    # refused with the reason instead of leaving the loop silently idle.
    if ! missing=$("$(dirname "$0")/check-deps.sh" --install core "${2:-none}" 2>&1); then
      echo "Cannot switch archive backend to '${2:-none}'; missing dependencies:" >&2
      grep -E '^[a-z]+: ' <<< "$missing" >&2 || printf '%s\n' "$missing" >&2
      exit 1
    fi
    systemctl stop teslausb.service
    # Never lazily unmount: a busy old destination must block reconfiguration.
    for target in "$ARCHIVE_MOUNT" "${MUSIC_ARCHIVE_MOUNT:-/mnt/musicarchive}"; do
      if findmnt --mountpoint "$target" >/dev/null; then
        timeout 30 umount "$target"
      fi
    done
    mkdir -p "$ARCHIVE_MOUNT" "${MUSIC_ARCHIVE_MOUNT:-/mnt/musicarchive}"
    ;;
  restart)
    systemctl daemon-reload
    systemctl restart teslausb.service
    ;;
  *) echo 'Usage: reconfigure-archive.sh prepare <backend> | restart' >&2; exit 2 ;;
esac
