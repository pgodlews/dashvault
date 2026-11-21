#!/bin/bash -eu
set -o pipefail
#
# check-provisioning.sh WIFI_ADAPTER [BLUETOOTH_ADAPTER]
#
# Tests first-run setup on a freshly flashed board waiting at its "Dashvault-XXXX"
# hotspot: the setup page, captive DNS, input checks, Bluetooth, and recovery from a
# wrong Wi-Fi password over both routes. It never sends working details, so the board
# ends up back at its hotspot, ready for a real setup.
#
# WIFI_ADAPTER must be a spare adapter with AP support. BLUETOOTH_ADAPTER defaults to
# hci0. Set SEQUENTIAL=1 for Pi Zero boards, which offer Bluetooth and the hotspot in
# turn. Needs Docker, NetworkManager, curl, dig, openssl, python3-dbus and sudo.

if [ $# -lt 1 ]
then
  sed -n '4,13p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

IF=$1
HCI=${2:-hci0}
SEQUENTIAL=${SEQUENTIAL:-0}
HERE=$(cd "$(dirname "$0")" && pwd)
LAB=$HERE/../wifi-lab/run-lab.sh
PORTAL=http://192.168.4.1
TARGET=dvlab-target
CONNECTION=dashvault-check
FAILED=0
status=""

if [ "$SEQUENTIAL" = 1 ]
then
  BLE_WAIT=720
  TARGET_HOLD=420
else
  BLE_WAIT=180
  TARGET_HOLD=270
fi

if ip -4 route show default | grep -q "dev $IF "
then
  echo "STOP: $IF carries the default route; use a spare adapter." >&2
  exit 1
fi

umask 077
WORK=$(mktemp -d)
cleanup () {
  sudo nmcli connection delete "$CONNECTION" > /dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

RIGHT_PSK=$(openssl rand -hex 12)
python3 - "$WORK/wrong.json" "$TARGET" <<'PY'
import json
import secrets
import sys

with open(sys.argv[1], "w") as request:
    json.dump(
        {
            "archive_type": "none",
            "wifi_ssid": sys.argv[2],
            "wifi_password": secrets.token_hex(12),
            "webui_username": "owner",
            "webui_password": secrets.token_hex(10),
        },
        request,
    )
PY

log () {
  echo "$(date +%T) $*"
}

expect () {
  local name=$1 got=$2 pattern=$3
  # shellcheck disable=SC2254  # The pattern is meant to match as a glob.
  case "$got" in
    $pattern) echo "ok    $name" ;;
    *)
      echo "FAIL  $name: ${got:0:110}"
      FAILED=$((FAILED + 1))
      ;;
  esac
}

join_hotspot () {
  local ssid=""
  for _ in $(seq 1 "$1")
  do
    ssid=$(sudo nmcli -t --escape no -f SSID device wifi list ifname "$IF" --rescan yes 2> /dev/null \
      | grep -m1 '^Dashvault-' || true)
    if [ -n "$ssid" ]
    then
      break
    fi
    sleep 4
  done
  if [ -z "$ssid" ]
  then
    return 1
  fi
  sudo nmcli connection delete "$CONNECTION" > /dev/null 2>&1 || true
  sudo nmcli connection add type wifi ifname "$IF" con-name "$CONNECTION" autoconnect no \
    ssid "$ssid" ipv4.never-default yes ipv4.ignore-auto-dns yes ipv6.method disabled > /dev/null
  for _ in 1 2 3
  do
    if sudo nmcli connection up "$CONNECTION" > /dev/null 2>&1
    then
      break
    fi
    sleep 3
  done
  for _ in 1 2 3 4 5 6
  do
    status=$(curl -s -m 6 "$PORTAL/api/status" || true)
    if [ -n "$status" ]
    then
      log "joined $ssid"
      return 0
    fi
    sleep 2
  done
  return 1
}

leave_hotspot () {
  sudo nmcli connection down "$CONNECTION" > /dev/null 2>&1 || true
  sudo nmcli connection delete "$CONNECTION" > /dev/null 2>&1 || true
}

submit () {
  curl -s -m 15 -o "$WORK/reply" -w '%{http_code}' -H 'Content-Type: application/json' \
    -d "$1" "$PORTAL/api/configure" || true
}

bluetooth () {
  local wait=$1
  shift
  BLE_CLIENT_ADAPTER=$HCI timeout $((wait + 25)) python3 -B "$HERE/ble_client.py" "$@" "$wait" 2>&1 || true
}

# Offers the network the request names, with a different password, and checks that the
# board tried it and was refused.
offer_target () {
  local out=$WORK/lab-$1 seen
  LAB_PSK=$RIGHT_PSK LAB_ONLY=wpa2-psk LAB_SSID=$TARGET LAB_HOLD=$TARGET_HOLD \
    bash "$LAB" "$IF" "$out" 0 > "$WORK/lab.log" 2>&1 || true
  seen=$(python3 - "$out/results.json" <<'PY'
import json
import sys

try:
    with open(sys.argv[1]) as results:
        observed = json.load(results)["observed"].get("wpa2-psk", {})
except (OSError, ValueError):
    print("no lab results")
    sys.exit()
if observed.get("negotiated"):
    print("board joined with a wrong password")
elif any("PSK-MISMATCH" in event for event in observed.get("events", [])):
    print("refused")
else:
    print("board never tried the network")
PY
  )
  expect "router refused the wrong password ($1)" "$seen" refused
}

log "hotspot and setup page"
if ! join_hotspot 60
then
  echo "FAIL  no Dashvault hotspot found"
  exit 1
fi
expect "status answers" "$status" '*"ip": "192.168.4.1"*'
networks=$(curl -s -m 8 "$PORTAL/api/scan" \
  | python3 -c 'import json, sys; print(len(json.load(sys.stdin)))' 2> /dev/null || echo 0)
expect "Wi-Fi list has networks" "$networks" '[1-9]*'
expect "any URL serves the setup page" \
  "$(curl -s -m 8 "$PORTAL/generate_204" | grep -c '<title>Dashvault Setup' || true)" 1
expect "captive DNS" "$(dig +short +time=3 +tries=1 @192.168.4.1 example.com 2>&1 | head -1)" 192.168.4.1
expect "short password refused" \
  "$(submit '{"archive_type":"none","webui_username":"owner","webui_password":"short"}')" 400

if [ "$SEQUENTIAL" != 1 ]
then
  log "Bluetooth alongside the hotspot"
  expect "Bluetooth reads the Wi-Fi list" "$(bluetooth 110 read)" '*wifi list over bluetooth*'
  expect "hotspot still answers" \
    "$(curl -s -m 6 -o /dev/null -w '%{http_code}' "$PORTAL/api/status" || true)" 200
fi

log "wrong Wi-Fi password over the hotspot"
expect "details accepted" "$(submit "@$WORK/wrong.json")" 200
sleep 2
leave_hotspot
offer_target hotspot
if join_hotspot 90
then
  expect "error shown after hotspot attempt" "$status" "*Could not join*$TARGET*"
else
  expect "hotspot came back" "not found" found
fi
leave_hotspot

log "wrong Wi-Fi password over Bluetooth"
expect "Bluetooth setup accepted" "$(bluetooth "$BLE_WAIT" configure "$WORK/wrong.json")" '*status after: ok*'
offer_target bluetooth
if join_hotspot 90
then
  expect "error shown after Bluetooth attempt" "$status" "*Could not join*$TARGET*"
else
  expect "hotspot came back" "not found" found
fi
leave_hotspot

if [ "$FAILED" -gt 0 ]
then
  log "$FAILED check(s) FAILED"
  exit 1
fi
log "all checks passed; the board is back at its setup hotspot"
