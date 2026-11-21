#!/bin/bash -eu
set -o pipefail
# Delegate to the active backend's archive-clips.sh.
# ARCHIVE_SYSTEM is inherited from archiveloop's exported environment.
BACKEND="${ARCHIVE_SYSTEM:-none}"
SCRIPT="/root/bin/lib/backends/${BACKEND}/archive-clips.sh"
if [ ! -x "$SCRIPT" ]; then
  echo "archive-clips: no script for backend '${BACKEND}'" >&2
  exit 1
fi
exec "$SCRIPT" "$@"
