#!/bin/bash -eu
set -o pipefail
# Delegate to the active backend's disconnect-archive.sh.
# ARCHIVE_SYSTEM is inherited from archiveloop's exported environment.
BACKEND="${ARCHIVE_SYSTEM:-none}"
SCRIPT="/root/bin/lib/backends/${BACKEND}/disconnect-archive.sh"
if [ ! -x "$SCRIPT" ]; then
  echo "disconnect-archive: no script for backend '${BACKEND}'" >&2
  exit 1
fi
exec "$SCRIPT" "$@"
