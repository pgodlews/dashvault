#!/bin/bash -eu
set -o pipefail
# Run antenna-test.sh once per Wi-Fi mode: each access point (band / mesh node) and
# power save on/off. Changes are temporary (never saved) and restored on exit;
# a reboot also clears them. Run as root on the board, with antenna-test.sh beside it.
# Usage: mode-matrix.sh SINK_HOST SINK_PORT PREFIX MODES_FILE
#   PREFIX is prepended to each run label (e.g. the antenna name).
#   MODES_FILE has one "label|bssid|power save" line per mode (see modes.example.txt).
#   Env: CON (NetworkManager connection, default WiFi), UPLOAD (s, default 150), IDLE (s, default 30).
SINK_HOST=$1 SINK_PORT=$2 PREFIX=$3 MODES_FILE=$4
UPLOAD=${UPLOAD:-150}
export IDLE=${IDLE:-30}
CON=${CON:-WiFi}
HERE=$(cd "$(dirname "$0")" && pwd)
SRC=/backingfiles/antennatest-src.bin

mapfile -t MODES < <(grep -v -e "^#" -e "^$" "$MODES_FILE")

connect() {  # $1 = bssid or empty
  nmcli con modify --temporary "$CON" 802-11-wireless.bssid "$1"
  nmcli --wait 45 con up "$CON" > /dev/null
  if [ -n "$1" ]
  then
    [ "$(iw dev wlan0 link | awk '/^Connected to/ {print $3}')" = "$1" ] || return 1
  fi
  local gw
  gw=$(ip -4 route show default dev wlan0 | awk '{print $3; exit}')
  ping -c 3 -W 2 "$gw" > /dev/null
}
restore() {
  echo "$(date +%T) restoring automatic access point choice"
  nmcli con modify --temporary "$CON" 802-11-wireless.bssid "" || true
  nmcli --wait 45 con up "$CON" > /dev/null || true
  iw dev wlan0 set power_save on || true
  rm -f "$SRC"
  iw dev wlan0 link | head -3
  echo "$(date +%T) MATRIX_DONE"
}
trap restore EXIT

for m in "${MODES[@]}"
do
  IFS='|' read -r label bssid ps <<< "$m"
  label="$PREFIX$label"
  echo "$(date +%T) === $label (bssid ${bssid:-auto}, power save $ps)"
  if ! connect "$bssid"
  then
    echo "$(date +%T) SKIP $label: could not connect"
    connect "" || true
    continue
  fi
  iw dev wlan0 set power_save "$ps"
  sleep 5
  "$HERE/antenna-test.sh" "$label" "$SINK_HOST" "$SINK_PORT" "$UPLOAD" | grep -E "DONE|Connected|signal|tx bitrate" || echo "$(date +%T) FAIL $label"
done
