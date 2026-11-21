#!/bin/bash -eu
set -o pipefail

if [[ $# -ne 1 || ! $1 =~ ^[0-9]+$ ]]
then
  echo "usage: $0 USED_BYTES" >&2
  exit 2
fi

used_bytes=$1
gib=$((1 << 30))
minimum=$((8 * gib))
headroom=$((5 * gib))
target=$((used_bytes + headroom))
if ((target < minimum))
then
  target=$minimum
fi

printf '%dG\n' $(((target + gib - 1) / gib))
