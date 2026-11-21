#!/bin/bash -eu
# run/provisioning/start.sh — TeslaUSB first-run provisioning orchestrator.
#
# Gate: exits immediately if the device is already configured
# (the provisioning_done sentinel exists, or the setup conf already
# names an archive backend). Writers store ARCHIVE_SYSTEM,
# ARCHIVE_SERVER, RSYNC_SERVER, or RCLONE_DRIVE — not ARCHIVE_HOST.
#
# Pi Zero W / Zero 2W (single antenna — CYW43438/CYW43526):
#   1. Run BLE GATT peripheral for BLE_TIMEOUT seconds.
#   2. If not provisioned yet, tear down BLE and start open WiFi AP.
#
# Pi 3 / 4 / 5 (dual antenna or USB WiFi dongle):
#   Run BLE and AP concurrently; kill both once provisioned.
#
# Success: the BLE or portal script writes the configuration file and touches
# /boot/firmware/provisioning_done. This script then reboots, and rc.local runs
# the normal setup from that file, joining Wi-Fi first. If it cannot join,
# rc.local deletes the file and starts provisioning again.

set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONF_FILE="${SETUP_CONF_FILE:-/root/teslausb_setup_variables.conf}"
SENTINEL="${PROVISIONING_SENTINEL:-/boot/firmware/provisioning_done}"
BLE_TIMEOUT="${BLE_TIMEOUT:-90}"    # seconds to wait for a BLE client before AP fallback
BLE_MAX="${BLE_MAX:-900}"           # hard limit while a BLE client is filling in the form
AP_TIMEOUT="${AP_TIMEOUT:-600}"     # seconds AP stays open before giving up
CAPTIVE_DNS=/etc/NetworkManager/dnsmasq-shared.d/teslausb-captive.conf

log() { echo "[provisioning] $*" >&2; }

# ── gate: already provisioned? ────────────────────────────────────────────────

if [ -f "$SENTINEL" ]; then
    log "Sentinel $SENTINEL exists — provisioning already done. Exiting."
    exit 0
fi

if [ -f "$CONF_FILE" ]; then
    if grep -qE '^\s*(export\s+)?(ARCHIVE_SYSTEM|ARCHIVE_SERVER|RSYNC_SERVER|RCLONE_DRIVE)=' "$CONF_FILE" 2>/dev/null; then
        log "Archive configuration present in $CONF_FILE — device already configured. Exiting."
        exit 0
    fi
fi

log "No configuration detected — starting provisioning."

# Ensure rootfs and boot partition are writable so BLE/portal can write
# /root/teslausb_setup_variables.conf, NetworkManager profiles, and the sentinel.
mount -o remount,rw / 2>/dev/null || true
if [ -d /boot/firmware ]; then
    mount -o remount,rw /boot/firmware 2>/dev/null || true
elif [ -d /boot ]; then
    mount -o remount,rw /boot 2>/dev/null || true
fi

# The hotspot is open and the image's login is still the well-known default, so
# SSH must not be reachable from it. It comes back with the next boot, by which
# time rc.local has replaced the password.
systemctl stop ssh.socket ssh.service 2>/dev/null || true
if systemctl is-active --quiet ssh.service || systemctl is-active --quiet ssh.socket; then
    log "Could not stop SSH; refusing to open the setup hotspot."
    exit 1
fi

# The image ships nginx enabled, and the portal needs port 80.
systemctl stop nginx.service 2>/dev/null || true

# Sends every name to the portal. Removed once provisioning succeeds, so it has
# to be put back when a failed Wi-Fi join returns here.
mkdir -p "$(dirname "$CAPTIVE_DNS")"
echo 'address=/#/192.168.4.1' > "$CAPTIVE_DNS"

# Wi-Fi may still be blocked or settling this early in the first boot.
rfkill unblock wifi 2>/dev/null || true
rfkill unblock bluetooth 2>/dev/null || true
nmcli radio wifi on 2>/dev/null || true
for _ in $(seq 1 30); do
    if nmcli -t -f DEVICE,STATE device 2>/dev/null | grep -qE '^wlan0:(disconnected|connected)'; then
        break
    fi
    sleep 1
done

# BlueZ's plugins act as GATT clients: as soon as a phone or laptop connects they
# read its services (battery, audio...). If one of those needs encryption, BlueZ
# asks to pair, and when the other side refuses, the link drops before setup can
# send anything. Provisioning only needs the GATT server and advertising, which
# are in the core, so run without plugins. The override is in /run, so the next
# boot gets the normal Bluetooth service back.
bluetooth_server_only() {
    local daemon override=/run/systemd/system/bluetooth.service.d/teslausb-provisioning.conf
    daemon="$(systemctl show -p ExecStart --value bluetooth.service 2>/dev/null \
        | sed -n 's/.*path=\([^ ;]*\).*/\1/p' | head -1)"
    [ -x "$daemon" ] || return 0
    [ -e "$override" ] && return 0
    mkdir -p "$(dirname "$override")"
    printf '[Service]\nExecStart=\nExecStart=%s --noplugin=*\n' "$daemon" > "$override"
    systemctl daemon-reload
    systemctl restart bluetooth.service || true
    for _ in $(seq 1 15); do
        if bluetoothctl list 2>/dev/null | grep -q Controller; then
            return 0
        fi
        sleep 1
    done
    log "No Bluetooth adapter came up."
}
bluetooth_server_only || true

# ── hardware detection ────────────────────────────────────────────────────────

is_zero_w() {
    local model
    model="$(cat /proc/device-tree/model 2>/dev/null || true)"
    [[ "$model" == *"Zero W"* ]] || [[ "$model" == *"Zero 2 W"* ]]
}

# ── pre-scan WiFi ─────────────────────────────────────────────────────────────
# Scan up-front so both BLE and AP paths have fresh data the moment they start.

log "Pre-scanning nearby WiFi networks…"
SCAN_JSON="$(python3 "$SCRIPT_DIR/scan_wifi.py" 2>/dev/null || echo '[]')"
export TESLAUSB_WIFI_SCAN="$SCAN_JSON"
NETWORK_COUNT="$(echo "$SCAN_JSON" | python3 -c \
    'import json,sys; print(len(json.load(sys.stdin)))' 2>/dev/null || echo '?')"
log "Pre-scan complete: $NETWORK_COUNT networks found."

# ── helpers ───────────────────────────────────────────────────────────────────

check_done() { [ -f "$SENTINEL" ]; }

run_ble() {
    log "Starting BLE GATT peripheral (waits ${BLE_TIMEOUT}s for a client)…"
    BLE_TIMEOUT="$BLE_TIMEOUT" timeout "$BLE_MAX" python3 "$SCRIPT_DIR/ble_service.py" || true
    log "BLE phase ended."
}

run_ap() {
    log "Starting open WiFi AP captive portal (timeout ${AP_TIMEOUT}s)…"
    timeout "$AP_TIMEOUT" python3 "$SCRIPT_DIR/portal_server.py" || true
    log "AP phase ended."
}

# ── main flow ────────────────────────────────────────────────────────────────

if is_zero_w; then
    log "Pi Zero W/2W detected — sequential mode (BLE → AP)."

    run_ble
    if check_done; then
        log "Provisioned via BLE."
    else
        log "BLE phase complete without provisioning; falling back to WiFi AP."
        run_ap
    fi
else
    log "Non-Zero Pi detected — parallel mode (BLE + AP)."
    BLE_TIMEOUT="$AP_TIMEOUT"

    run_ble &
    BLE_PID=$!
    run_ap &
    AP_PID=$!

    # Poll until the sentinel appears or both helpers exit.
    while kill -0 "$BLE_PID" 2>/dev/null || kill -0 "$AP_PID" 2>/dev/null; do
        if check_done; then
            kill "$BLE_PID" "$AP_PID" 2>/dev/null || true
            break
        fi
        sleep 2
    done
    wait "$BLE_PID" "$AP_PID" 2>/dev/null || true
fi

# ── post-provisioning ─────────────────────────────────────────────────────────

if check_done; then
    log "Provisioning complete. Rebooting into setup."
    rm -f "$CAPTIVE_DNS"
    sync
    # Let the browser or Bluetooth client see the result first.
    sleep 3
    systemctl reboot
    exit 0
fi

log "Provisioning timed out without completion."
exit 1
