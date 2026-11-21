#!/bin/bash -eu
set -o pipefail

# ARCHIVE_SERVER is 8.8.8.8 for rclone (a cloud drive has no single host).
# Some networks drop ICMP, so fall back to an HTTPS connection: otherwise the
# archive looks permanently unreachable and archiving never starts.
# shellcheck disable=SC2016  # $0 expands in the inner bash
ping -q -w 1 -c 1 "$1" > /dev/null 2>&1 ||
  timeout 5 bash -c 'exec 3<>"/dev/tcp/$0/443"' "$1" > /dev/null 2>&1
