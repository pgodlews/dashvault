#!/bin/bash
# Execute the three release backend binaries under QEMU user-mode emulation.
#
# Usage: tests/qemu-backend.sh <directory>
# The directory must contain:
#   teslausb-backend.arm64
#   teslausb-backend.armv7
#   teslausb-backend.armv6
#
# arm64 and armv7 must serve HTTP on a CPU of that architecture.
# The armv6 binary must serve HTTP on ARM1176 (Pi Zero W).
# The armv7 binary must not, because that CPU cannot run it.
set -euo pipefail

DIR="${1:?usage: tests/qemu-backend.sh <directory>}"

qemu_for() {
  local arch="$1" candidate
  for candidate in "qemu-${arch}-static" "qemu-${arch}"; do
    if command -v "$candidate" >/dev/null 2>&1; then
      command -v "$candidate"
      return 0
    fi
  done
  echo "QEMU user emulator for ${arch} is not installed (qemu-user)" >&2
  return 1
}

wait_for_http() {
  local pid="$1" port="$2" log="$3" code tries
  tries=0
  while [ "$tries" -lt 40 ]; do
    tries=$((tries + 1))
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "backend exited before it was ready" >&2
      cat "$log" >&2
      return 1
    fi
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 1 "http://127.0.0.1:${port}/api/status" || true)"
    if [ "$code" = "401" ] || [ "$code" = "200" ]; then
      return 0
    fi
    sleep 0.25
  done
  echo "backend did not answer on port ${port}" >&2
  cat "$log" >&2
  return 1
}

smoke() {
  local label="$1" qemu="$2" cpu="$3" bin="$4" port="$5"
  local log pid
  log="$(mktemp)"
  echo "QEMU smoke: ${label}"
  "$qemu" -cpu "$cpu" "$bin" -addr "127.0.0.1:${port}" >"$log" 2>&1 &
  pid=$!
  if wait_for_http "$pid" "$port" "$log"; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    rm -f "$log"
    return 0
  fi
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  rm -f "$log"
  return 1
}

# An armv7 backend must fault on the original Zero's ARM1176. A timeout means
# it kept running, which is also a failure of this guard.
expect_zero_w_rejects_armv7() {
  local qemu="$1" bin="$2" log status
  log="$(mktemp)"
  echo "QEMU guard: armv7 binary must not run on ARM1176"
  set +e
  timeout 8 "$qemu" -cpu arm1176 "$bin" -addr "127.0.0.1:0" >"$log" 2>&1
  status=$?
  set -e
  if grep -q "teslausb-backend ready" "$log"; then
    echo "armv7 backend started on ARM1176" >&2
    cat "$log" >&2
    rm -f "$log"
    return 1
  fi
  rm -f "$log"
  if [ "$status" -eq 124 ]; then
    echo "armv7 backend kept running on ARM1176" >&2
    return 1
  fi
  if [ "$status" -eq 0 ]; then
    echo "armv7 backend exited cleanly on ARM1176" >&2
    return 1
  fi
}

QEMU_A64="$(qemu_for aarch64)"
QEMU_ARM="$(qemu_for arm)"

for name in teslausb-backend.arm64 teslausb-backend.armv7 teslausb-backend.armv6; do
  if [ ! -x "${DIR}/${name}" ]; then
    echo "missing ${DIR}/${name}" >&2
    exit 1
  fi
done

smoke "arm64 on Cortex-A53" "$QEMU_A64" cortex-a53 "${DIR}/teslausb-backend.arm64" 18081
smoke "armv7 on Cortex-A7" "$QEMU_ARM" cortex-a7 "${DIR}/teslausb-backend.armv7" 18082
smoke "armv6 on ARM1176" "$QEMU_ARM" arm1176 "${DIR}/teslausb-backend.armv6" 18083
expect_zero_w_rejects_armv7 "$QEMU_ARM" "${DIR}/teslausb-backend.armv7"
echo "QEMU backend smoke passed"
