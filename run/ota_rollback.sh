#!/bin/bash -eu
# ota_rollback.sh — restore the most recent teslausb backup.
#
# Usage (dispatched by teslausb-backend as an independent systemd service):
#   ota_rollback.sh
#
# Finds the newest timestamped dir under /root/teslausb_backup/, restores
# bin/, backend binary, www/, and the VERSION file, then restarts services.
set -o pipefail

# This script restores over its own file below. Run from a copy in RAM so bash
# never reads changed content or holds a deleted file open on the root disk.
if [ -z "${OTA_SCRIPT_RELOCATED:-}" ]; then
  relocated="$(mktemp "${TMPDIR:-/tmp}/ota_rollback.XXXXXX")"
  cp "$0" "$relocated"
  OTA_SCRIPT_RELOCATED=1 exec "$BASH" -eu "$relocated" "$@"
fi
rm -f "$0"

# shellcheck disable=SC1090
source "${OTA_TRANSACTION_LIB:-/root/bin/ota_transaction.sh}"
ota_begin_transaction

BACKUP_ROOT="${OTA_BACKUP_ROOT:-/root/teslausb_backup}"
LOG_FILE="${OTA_LOG_FILE:-/var/log/teslausb_update.log}"
VERSION_FILE="${OTA_VERSION_FILE:-/root/teslausb_version}"
INSTALL_BIN="${OTA_INSTALL_BIN:-/root/bin}"
INSTALL_PROVISIONING="${OTA_INSTALL_PROVISIONING:-/root/provisioning}"
INSTALL_BACKEND_BIN="${OTA_INSTALL_BACKEND_BIN:-/var/www/html/go-backend/teslausb-backend}"
INSTALL_WWW="${OTA_INSTALL_WWW:-/var/www/html/new}"

export TELEGRAM_UNIT=teslausb-telegram-bot.service
export TELEGRAM_WAS_ACTIVE=false
if systemctl is-active --quiet "$TELEGRAM_UNIT"; then
  TELEGRAM_WAS_ACTIVE=true
fi

log() {
  echo "[$(date -u '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}

die() {
  log "ERROR: $*"
  exit 1
}

# --- find most recent backup -------------------------------------------------
# shellcheck disable=SC2012
BACKUP_PATH="$(ls -1dt "$BACKUP_ROOT"/20* 2>/dev/null | head -1 || true)"
[ -n "$BACKUP_PATH" ] || die "no backup found under $BACKUP_ROOT"
log "=== Starting rollback from $BACKUP_PATH ==="

# --- stop services -----------------------------------------------------------
log "Stopping teslausb services..."
ota_stop_services
sleep 1

# --- restore scripts ---------------------------------------------------------
if [ -d "$BACKUP_PATH/bin" ] && [ "$(ls -A "$BACKUP_PATH/bin" 2>/dev/null)" ]; then
  log "Restoring bin files..."
  cp -a "$BACKUP_PATH/bin"/. "$INSTALL_BIN/"
fi
if [ -d "$BACKUP_PATH/provisioning" ] && [ "$(ls -A "$BACKUP_PATH/provisioning" 2>/dev/null)" ]; then
  log "Restoring provisioning files..."
  mkdir -p "$INSTALL_PROVISIONING"
  cp -a "$BACKUP_PATH/provisioning"/. "$INSTALL_PROVISIONING/"
fi

# --- restore Go backend binary -----------------------------------------------
if [ -f "$BACKUP_PATH/backend" ]; then
  log "Restoring Go backend binary..."
  install -m 755 "$BACKUP_PATH/backend" "$INSTALL_BACKEND_BIN"
fi

# --- restore UI --------------------------------------------------------------
if [ -d "$BACKUP_PATH/www" ] && [ "$(ls -A "$BACKUP_PATH/www" 2>/dev/null)" ]; then
  log "Restoring UI..."
  rm -rf "$INSTALL_WWW"
  cp -a "$BACKUP_PATH/www" "$INSTALL_WWW"
fi

# --- restore service units --------------------------------------------------
if [ -f "$BACKUP_PATH/units/install-dir" ]; then
  UNIT_DIR="$(cat "$BACKUP_PATH/units/install-dir")"
  if [ -f "$BACKUP_PATH/units/$TELEGRAM_UNIT" ]; then
    install -m 644 "$BACKUP_PATH/units/$TELEGRAM_UNIT" "$UNIT_DIR/$TELEGRAM_UNIT"
  else
    rm -f "$UNIT_DIR/$TELEGRAM_UNIT"
  fi
  systemctl daemon-reload
fi

# --- restore version ---------------------------------------------------------
if [ -f "$BACKUP_PATH/VERSION" ]; then
  cp "$BACKUP_PATH/VERSION" "$VERSION_FILE"
  log "Restored version: $(cat "$VERSION_FILE")"
fi

# --- remove the consumed backup ----------------------------------------------
rm -rf "$BACKUP_PATH"
log "Removed backup $BACKUP_PATH"

# --- restart services --------------------------------------------------------
log "Starting teslausb services..."
ota_start_services

log "=== Rollback complete ==="
