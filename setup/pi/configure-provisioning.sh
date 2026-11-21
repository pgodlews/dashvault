#!/bin/bash -eu
# configure-provisioning.sh — install the TeslaUSB provisioning service.
#
# This script is idempotent.  It:
#   1. Installs required packages (avahi-daemon, python3-dbus, python3-gi,
#      network-manager, bluez).
#   2. Sets the mDNS hostname to "dashvault" so the device is reachable at
#      dashvault.local once it connects to the user's network.
#   3. Writes a NetworkManager dnsmasq-shared snippet that redirects all DNS
#      queries to the AP IP (captive portal).
#   4. Copies provisioning scripts to /root/provisioning/.
#   5. Installs the teslausb-provisioning.service systemd unit.
#
# Should be called from setup-teslausb (or run manually as root).

set -o pipefail

PROVISIONING_SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../../run/provisioning"
PROVISIONING_DEST="/root/provisioning"
SERVICE_SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/teslausb-provisioning.service"

log() { echo "[configure-provisioning] $*"; }

# ── packages ───────────────────────────────────────────────────────────────────

log "Installing required packages…"
apt-get install -y --no-install-recommends \
    avahi-daemon \
    avahi-utils \
    python3-dbus \
    python3-gi \
    network-manager \
    bluez \
    libnss-mdns

# ── mDNS hostname ─────────────────────────────────────────────────────────────

log "Setting hostname to 'dashvault' for dashvault.local mDNS…"
hostnamectl set-hostname dashvault

# Ensure avahi publishes the new hostname.
systemctl enable avahi-daemon
systemctl restart avahi-daemon

# ── NetworkManager dnsmasq captive-portal config ──────────────────────────────

DNSMASQ_SHARED_DIR="/etc/NetworkManager/dnsmasq-shared.d"
mkdir -p "$DNSMASQ_SHARED_DIR"

log "Writing captive-portal DNS redirect…"
cat > "$DNSMASQ_SHARED_DIR/teslausb-captive.conf" <<'EOF'
# Redirect all DNS queries to the AP IP for captive portal detection.
# This file is only active when NetworkManager brings up a shared (AP) connection.
address=/#/192.168.4.1
EOF

# ── install provisioning scripts ──────────────────────────────────────────────

log "Installing provisioning scripts to $PROVISIONING_DEST…"
mkdir -p "$PROVISIONING_DEST"
install -m755 "$PROVISIONING_SRC/start.sh"          "$PROVISIONING_DEST/"
install -m755 "$PROVISIONING_SRC/scan_wifi.py"       "$PROVISIONING_DEST/"
install -m755 "$PROVISIONING_SRC/portal_server.py"   "$PROVISIONING_DEST/"
install -m644 "$PROVISIONING_SRC/portal.html"        "$PROVISIONING_DEST/"
install -m644 "$PROVISIONING_SRC/provisioning_config.py" "$PROVISIONING_DEST/"
install -m755 "$PROVISIONING_SRC/ble_service.py"     "$PROVISIONING_DEST/"

# ── systemd unit ──────────────────────────────────────────────────────────────

log "Installing systemd unit…"
install -m644 "$SERVICE_SRC" /etc/systemd/system/teslausb-provisioning.service
systemctl daemon-reload
systemctl enable teslausb-provisioning.service

log "Provisioning service installed."
log "It will start automatically on next boot if the device is not yet configured."
