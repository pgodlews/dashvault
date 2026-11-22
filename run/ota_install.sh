#!/bin/bash -eu
# ota_install.sh — apply a teslausb OTA update from an extracted release dir.
#
# Usage (dispatched by teslausb-backend as an independent systemd service):
#   ota_install.sh <extract_dir>
#
# The extract_dir must contain:
#   VERSION           — new version string
#   bin/              — scripts + Go backend binary
#   www/              — pre-built React UI dist
#
# This script:
#   1. Backs up current installation to /root/teslausb_backup/<timestamp>/
#   2. Prunes old backups (keeps 2 most recent)
#   3. Stops teslausb and teslausb-backend services
#   4. Installs new files
#   5. Restarts services
#   6. Writes the new version to /root/teslausb_version
set -o pipefail

# This script replaces its own file below. Run from a copy in RAM: bash would
# otherwise hold the deleted file open, and root could not return to read-only.
if [ -z "${OTA_SCRIPT_RELOCATED:-}" ]; then
  relocated="$(mktemp "${TMPDIR:-/tmp}/ota_install.XXXXXX")"
  cp "$0" "$relocated"
  OTA_SCRIPT_RELOCATED=1 exec "$BASH" -eu "$relocated" "$@"
fi
rm -f "$0"

# shellcheck disable=SC1090
source "${OTA_TRANSACTION_LIB:-/root/bin/ota_transaction.sh}"
ota_begin_transaction

EXTRACT_DIR="${1:?Usage: ota_install.sh <extract_dir>}"
BACKUP_ROOT="${OTA_BACKUP_ROOT:-/root/teslausb_backup}"
LOG_FILE="${OTA_LOG_FILE:-/var/log/teslausb_update.log}"
VERSION_FILE="${OTA_VERSION_FILE:-/root/teslausb_version}"
INSTALL_BIN="${OTA_INSTALL_BIN:-/root/bin}"
INSTALL_PROVISIONING="${OTA_INSTALL_PROVISIONING:-/root/provisioning}"
INSTALL_BACKEND_BIN="${OTA_INSTALL_BACKEND_BIN:-/var/www/html/go-backend/teslausb-backend}"
INSTALL_WWW="${OTA_INSTALL_WWW:-/var/www/html/new}"

INSTALL_UNITS="${OTA_INSTALL_UNITS:-/lib/systemd/system}"
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

# --- read new version --------------------------------------------------------
NEW_VERSION="$(cat "$EXTRACT_DIR/VERSION" 2>/dev/null)" || die "no VERSION file in $EXTRACT_DIR"
NEW_VERSION="${NEW_VERSION// /}"
[ -n "$NEW_VERSION" ] || die "VERSION file is empty"
log "Installing version: $NEW_VERSION"

# --- backup current installation --------------------------------------------
STAMP="$(date -u '+%Y-%m-%dT%H%M%S')"
BACKUP_PATH="$BACKUP_ROOT/$STAMP"
log "Backing up current installation to $BACKUP_PATH"
mkdir -p "$BACKUP_PATH/bin" "$BACKUP_PATH/www"
cp -a "$INSTALL_BIN"/. "$BACKUP_PATH/bin/" 2>/dev/null || true
if [ -d "$INSTALL_PROVISIONING" ]; then
  mkdir -p "$BACKUP_PATH/provisioning"
  cp -a "$INSTALL_PROVISIONING"/. "$BACKUP_PATH/provisioning/" 2>/dev/null || true
fi
if [ -f "$INSTALL_BACKEND_BIN" ]; then
  cp "$INSTALL_BACKEND_BIN" "$BACKUP_PATH/backend"
fi
if [ -d "$INSTALL_WWW" ]; then
  cp -a "$INSTALL_WWW/." "$BACKUP_PATH/www/" 2>/dev/null || true
fi
if [ -f "$VERSION_FILE" ]; then
  cp "$VERSION_FILE" "$BACKUP_PATH/VERSION"
fi

# Record whether the old installation had a unit, including a local override.
mkdir -p "$BACKUP_PATH/units"
for unit_dir in "$INSTALL_UNITS" "${OTA_OVERRIDE_UNITS:-/etc/systemd/system}"; do
  if [ -f "$unit_dir/$TELEGRAM_UNIT" ]; then
    cp -a "$unit_dir/$TELEGRAM_UNIT" "$BACKUP_PATH/units/$TELEGRAM_UNIT"
  fi
done

# --- prune old backups (keep 2 most recent) ----------------------------------
# shellcheck disable=SC2012
ls -1dt "$BACKUP_ROOT"/20* 2>/dev/null | tail -n +3 | xargs -r rm -rf
log "Old backups pruned (keeping 2 most recent)"

# --- stop services -----------------------------------------------------------
log "Stopping teslausb services..."
ota_stop_services
sleep 1

# --- install scripts ---------------------------------------------------------
if [ -d "$EXTRACT_DIR/bin" ]; then
  log "Installing bin files..."
  for f in "$EXTRACT_DIR/bin"/*; do
    [ -f "$f" ] || continue
    base="$(basename "$f")"
    case "$base" in
      teslausb-backend|teslausb-backend.arm64|teslausb-backend.armv7|teslausb-backend.armv6) continue ;;
    esac
    dest="$INSTALL_BIN/$base"
    install -m 755 "$f" "$dest"
    log "  installed $dest"
  done
  if [ -d "$EXTRACT_DIR/bin/lib" ]; then
    mkdir -p "$INSTALL_BIN/lib"
    for f in "$EXTRACT_DIR/bin/lib"/*; do
      [ -f "$f" ] || continue
      install -m 755 "$f" "$INSTALL_BIN/lib/$(basename "$f")"
    done
    if [ -d "$EXTRACT_DIR/bin/lib/backends" ]; then
      mkdir -p "$INSTALL_BIN/lib/backends"
      cp -a "$EXTRACT_DIR/bin/lib/backends"/. "$INSTALL_BIN/lib/backends/"
    fi
  fi
  if [ -d "$EXTRACT_DIR/bin/provisioning" ]; then
    log "Installing provisioning files..."
    mkdir -p "$INSTALL_PROVISIONING"
    for f in "$EXTRACT_DIR/bin/provisioning"/*; do
      [ -f "$f" ] || continue
      base="$(basename "$f")"
      mode=755
      case "$base" in *.html) mode=644 ;; esac
      install -m "$mode" "$f" "$INSTALL_PROVISIONING/$base"
    done
  fi
fi

# --- install Go backend binary -----------------------------------------------
install_backend_for_architecture() {
  if [ ! -f "$EXTRACT_DIR/bin/teslausb-backend" ] \
    && [ ! -f "$EXTRACT_DIR/bin/teslausb-backend.arm64" ] \
    && [ ! -f "$EXTRACT_DIR/bin/teslausb-backend.armv7" ] \
    && [ ! -f "$EXTRACT_DIR/bin/teslausb-backend.armv6" ]; then
    return 0
  fi
  local machine selected=""
  machine="$(uname -m)"
  case "$machine" in
    aarch64|arm64)
      if [ -f "$EXTRACT_DIR/bin/teslausb-backend.arm64" ]; then
        selected="$EXTRACT_DIR/bin/teslausb-backend.arm64"
      elif [ -f "$EXTRACT_DIR/bin/teslausb-backend" ]; then
        selected="$EXTRACT_DIR/bin/teslausb-backend"
      fi
      ;;
    armv6l)
      if [ -f "$EXTRACT_DIR/bin/teslausb-backend.armv6" ]; then
        selected="$EXTRACT_DIR/bin/teslausb-backend.armv6"
      fi
      ;;
    armv7l|armhf)
      if [ -f "$EXTRACT_DIR/bin/teslausb-backend.armv7" ]; then
        selected="$EXTRACT_DIR/bin/teslausb-backend.armv7"
      fi
      ;;
    *)
      die "unsupported architecture $machine"
      ;;
  esac
  if [ -z "$selected" ]; then
    die "no teslausb-backend binary for $machine"
  fi
  log "Installing Go backend binary for $machine..."
  install -m 755 "$selected" "$INSTALL_BACKEND_BIN"
}
install_backend_for_architecture

# --- install provisioning setup scripts --------------------------------------
if [ -d "$EXTRACT_DIR/setup" ]; then
  log "Installing provisioning setup files..."
  mkdir -p "${OTA_INSTALL_SETUP:-/root/setup}"
  cp -a "$EXTRACT_DIR/setup/." "${OTA_INSTALL_SETUP:-/root/setup}/"
  if [ -f "$EXTRACT_DIR/setup/teslausb-provisioning.service" ]; then
    install -m 644 "$EXTRACT_DIR/setup/teslausb-provisioning.service" \
      "${OTA_INSTALL_SYSTEMD:-/etc/systemd/system}/teslausb-provisioning.service"
    systemctl daemon-reload
  fi
fi

# --- install UI --------------------------------------------------------------
if [ -d "$EXTRACT_DIR/www" ] && [ "$(ls -A "$EXTRACT_DIR/www" 2>/dev/null)" ]; then
  log "Installing UI..."
  rm -rf "$INSTALL_WWW"
  cp -a "$EXTRACT_DIR/www" "$INSTALL_WWW"
fi

# --- install service units --------------------------------------------------
if [ -f "$EXTRACT_DIR/units/$TELEGRAM_UNIT" ]; then
  # Respect /etc's unit precedence, updating it when an older setup used it.
  UNIT_DIR="$INSTALL_UNITS"
  if [ -f "${OTA_OVERRIDE_UNITS:-/etc/systemd/system}/$TELEGRAM_UNIT" ]; then
    UNIT_DIR="${OTA_OVERRIDE_UNITS:-/etc/systemd/system}"
  fi
  printf '%s\n' "$UNIT_DIR" > "$BACKUP_PATH/units/install-dir"
  mkdir -p "$UNIT_DIR"
  install -m 644 "$EXTRACT_DIR/units/$TELEGRAM_UNIT" "$UNIT_DIR/$TELEGRAM_UNIT"
  systemctl daemon-reload
fi

# --- dependencies ------------------------------------------------------------
# A new version may call tools the old one did not. The car may have no
# internet, so a failed install must not abort the update; archiveloop
# reports anything still missing as an archive error.
if [ -x "$INSTALL_BIN/check-deps.sh" ]; then
  log "Checking runtime dependencies..."
  if deps_out=$("$INSTALL_BIN/check-deps.sh" --install 2>&1); then
    log "  all dependencies present"
  else
    log "  WARNING: still missing after install attempt:"
    grep -E '^[a-z]+: ' <<< "$deps_out" | while IFS= read -r line; do log "    $line"; done || true
  fi
fi

# --- update version file -----------------------------------------------------
echo "$NEW_VERSION" > "$VERSION_FILE"
log "Version updated to $NEW_VERSION"

# --- restart services --------------------------------------------------------
log "Starting teslausb services..."
ota_start_services

log "=== Update to $NEW_VERSION complete ==="
