#!/bin/bash -eu
set -o pipefail

ARCHIVE_HOST_NAME="$1"

# Use bash's /dev/tcp rather than nc: nc (netcat) is an optional package that
# a fresh image may not have, and a missing nc exits 127, which looks exactly
# like "archive unreachable" and stalls archiving forever.
# shellcheck disable=SC2016  # $0/$1 expand in the inner bash
timeout 5 bash -c 'exec 3<>"/dev/tcp/$0/$1"' "$ARCHIVE_HOST_NAME" 445 > /dev/null 2>&1
