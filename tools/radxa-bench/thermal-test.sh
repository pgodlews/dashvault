#!/bin/bash -eu
set -o pipefail
# Thermal test for the Radxa Zero 3 (run as root). Logs SoC/GPU temperature, CPU clock,
# load and Wi-Fi throughput every 2 s through these phases:
#   idle -> wifi copy -> disk write -> cpu stress -> combined -> cooldown
# Usage: thermal-test.sh SINK_HOST SINK_PORT [OUTDIR]
SINK_HOST=$1
SINK_PORT=$2
OUT=${3:-/mutable/thermal-test}
IDLE=${IDLE:-300} WIFI=${WIFI:-600} DISK=${DISK:-300} CPU=${CPU:-600} COMBO=${COMBO:-600} COOL=${COOL:-300}
mkdir -p "$OUT"
cd /tmp   # stress-ng needs a writable working directory; / is read-only
CSV="$OUT/thermal.csv"
PHASE_FILE="$OUT/phase"
SRC=/backingfiles/thermaltest-src.bin
DST=/backingfiles/thermaltest-dst.bin
echo idle > "$PHASE_FILE"

zone() { cat "/sys/class/thermal/thermal_zone$1/temp"; }
logger_loop() {
  echo "epoch,phase,soc_mC,gpu_mC,cpu_khz,governor,load1,wlan_tx_bytes,throttle_state" > "$CSV"
  while :; do
    printf '%s,%s,%s,%s,%s,%s,%s,%s,%s\n' "$(date +%s)" "$(cat "$PHASE_FILE")" "$(zone 0)" "$(zone 1)" \
      "$(cat /sys/devices/system/cpu/cpufreq/policy0/scaling_cur_freq)" \
      "$(cat /sys/devices/system/cpu/cpufreq/policy0/scaling_governor)" \
      "$(cut -d' ' -f1 /proc/loadavg)" "$(cat /sys/class/net/wlan0/statistics/tx_bytes)" \
      "$(cat /sys/class/thermal/cooling_device0/cur_state 2>/dev/null || echo na)" >> "$CSV"
    sleep 2
  done
}

wifi_send() {  # read a real file off the card and stream it to the sink until killed
  while :; do cat "$SRC" > "/dev/tcp/$SINK_HOST/$SINK_PORT" || sleep 1; done
}
disk_write() {  # synchronous writes like the car's, 160 MiB chunks, until killed
  while :; do dd if=/dev/urandom of="$DST" bs=4M count=40 oflag=dsync conv=fsync status=none; done
}

cleanup() {
  jobs -p | xargs -r kill 2>/dev/null || true
  pkill -f "stress-ng --cpu" 2>/dev/null || true
  rm -f "$SRC" "$DST"
}
trap cleanup EXIT

avail_mb=$(df -m --output=avail /backingfiles | tail -1)
[ "$avail_mb" -gt 3000 ] || { echo "not enough space in /backingfiles ($avail_mb MB)"; exit 1; }
echo "creating 1 GiB source file"
dd if=/dev/urandom of="$SRC" bs=4M count=256 status=none
sync; echo 3 > /proc/sys/vm/drop_caches

logger_loop &
phase() { echo "$1" > "$PHASE_FILE"; echo "$(date +%T) phase $1 (${2}s)"; }
run() { [ "$1" -gt 0 ]; }  # skip phases set to 0

if run "$IDLE"; then phase idle "$IDLE"; sleep "$IDLE"; fi

if run "$WIFI"; then phase wifi "$WIFI"; wifi_send & W=$!; sleep "$WIFI"; kill $W; pkill -P $W 2>/dev/null || true; fi

if run "$DISK"; then phase disk "$DISK"; disk_write & D=$!; sleep "$DISK"; kill $D; pkill -P $D 2>/dev/null || true; fi

if run "$CPU"; then phase cpu "$CPU"; stress-ng --cpu 4 --timeout "${CPU}s" --quiet; fi

phase combined "$COMBO"; wifi_send & W=$!; disk_write & D=$!
stress-ng --cpu 4 --timeout "${COMBO}s" --quiet
kill $W $D; pkill -P $W 2>/dev/null || true; pkill -P $D 2>/dev/null || true

phase cooldown "$COOL"; sleep "$COOL"
echo "$(date +%T) DONE"
