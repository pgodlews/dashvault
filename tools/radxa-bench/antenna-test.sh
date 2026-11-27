#!/bin/bash -eu
set -o pipefail
# Wi-Fi antenna test for the Radxa Zero 3 (run as root).
# Logs every 2 s: signal, link rates, retries, frequency, Wi-Fi bytes sent, SoC temp, CPU clock.
# Phases: idle (signal with no traffic) -> upload (a 1 GiB file read off the card, streamed to the sink).
# Usage: antenna-test.sh LABEL SINK_HOST SINK_PORT [UPLOAD_SECONDS]
LABEL=$1 SINK_HOST=$2 SINK_PORT=$3 UPLOAD=${4:-300} IDLE=${IDLE:-60}
OUT=/mutable/antenna-test
mkdir -p "$OUT"
cd /tmp
CSV="$OUT/$LABEL.csv"
PHASE_FILE="$OUT/.phase"
SRC=/backingfiles/antennatest-src.bin

link_info() {  # bssid freq signal tx_mbit tx_mcs rx_mbit (the AIC8800 driver gives no station dump)
  iw dev wlan0 link | awk '
    /^Connected to/ {b=$3} /freq:/ {f=int($2)} /signal:/ {s=$2}
    /tx bitrate:/ {tx=$3; for (i=4;i<=NF;i++) if ($i ~ /MCS$/) m=$i" "$(i+1)}
    /rx bitrate:/ {rx=$3}
    END {gsub(/ /,"",m); printf "%s,%s,%s,%s,%s,%s", b, f, s, tx, m, rx}'
}
logger_loop() {
  echo "epoch,label,phase,bssid,freq_mhz,signal_dbm,tx_mbit,tx_mcs,rx_mbit,wlan_tx_bytes,wlan_tx_errors,soc_mC,cpu_khz" > "$CSV"
  while :; do
    printf '%s,%s,%s,%s,%s,%s,%s,%s\n' "$(date +%s)" "$LABEL" "$(cat "$PHASE_FILE")" "$(link_info)" \
      "$(cat /sys/class/net/wlan0/statistics/tx_bytes)" "$(cat /sys/class/net/wlan0/statistics/tx_errors)" \
      "$(cat /sys/class/thermal/thermal_zone0/temp)" \
      "$(cat /sys/devices/system/cpu/cpufreq/policy0/scaling_cur_freq)" >> "$CSV"
    sleep 2
  done
}
cleanup() { jobs -p | xargs -r kill 2>/dev/null || true; pkill -f "cat $SRC" 2>/dev/null || true; }  # SRC is kept for the next run
trap cleanup EXIT

[ -e "$SRC" ] || dd if=/dev/urandom of="$SRC" bs=4M count=256 status=none
sync; echo 3 > /proc/sys/vm/drop_caches
iw dev wlan0 link
echo idle > "$PHASE_FILE"; logger_loop &
echo "$(date +%T) idle ${IDLE}s"; sleep "$IDLE"
echo upload > "$PHASE_FILE"; echo "$(date +%T) upload ${UPLOAD}s"
( while :; do cat "$SRC" > "/dev/tcp/$SINK_HOST/$SINK_PORT" || sleep 1; done ) & W=$!
sleep "$UPLOAD"; kill $W; pkill -f "cat $SRC" || true
sleep 2
echo "$(date +%T) DONE $CSV"
