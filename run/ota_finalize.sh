#!/bin/bash -eu
# First-upgrade compatibility: old installers copy bin/ but ignore units/.
# Called at backend startup; wait for the installer's rootfs lock before writes.
set -o pipefail

# Boards set up before v0.1.1 let syslogd keep log files open on the root
# filesystem, which leaves it writable after any update or settings change.
# Keep logs in RAM, and put a board that was left writable back to read-only.
SYSLOG_DEFAULTS="${OTA_SYSLOG_DEFAULTS:-/etc/default/busybox-syslogd}"
if [ -f "$SYSLOG_DEFAULTS" ]; then
  # Newer BusyBox builds follow /etc/syslog.conf, so give them an empty config.
  # Older builds have no -f option and already log only to RAM with -C.
  SYSLOG_HELP="$(busybox syslogd --help 2>&1 || true)"
  case "$SYSLOG_HELP" in
    *"-f FILE"*) SYSLOG_SETTING='SYSLOG_OPTS="-C128 -f /dev/null"' ;;
    *) SYSLOG_SETTING='SYSLOG_OPTS="-C128"' ;;
  esac
  if ! grep -qxF "$SYSLOG_SETTING" "$SYSLOG_DEFAULTS"; then
    (
      # shellcheck disable=SC1090
      source "${OTA_TRANSACTION_LIB:-/root/bin/ota_transaction.sh}"
      ota_begin_transaction
      if findmnt --fstab -no OPTIONS "$OTA_ROOT_MOUNT" | tr ',' '\n' | grep -qx ro; then
        # shellcheck disable=SC2034  # read by ota_end_transaction
        OTA_ROOT_WAS_RO=true
      fi
      { grep -v '^SYSLOG_OPTS=' "$SYSLOG_DEFAULTS" || true; echo "$SYSLOG_SETTING"; } \
        > "$SYSLOG_DEFAULTS.new"
      chmod 644 "$SYSLOG_DEFAULTS.new"
      mv "$SYSLOG_DEFAULTS.new" "$SYSLOG_DEFAULTS"
      systemctl try-restart busybox-syslogd.service \
        || echo "WARNING: could not restart busybox-syslogd" >&2
    )
  fi
fi

# Older installers replace their own running script, which blocks their final
# remount. Once per installed version, put a read-only system back to read-only.
VERSION_FILE="${OTA_VERSION_FILE:-/root/teslausb_version}"
FINALIZED_VERSION="${OTA_FINALIZED_VERSION:-/mutable/teslausb_finalized_version}"
if [ -f "$VERSION_FILE" ] && [ -e "${OTA_SETUP_FINISHED:-/teslausb/TESLAUSB_SETUP_FINISHED}" ] \
  && ! cmp -s "$VERSION_FILE" "$FINALIZED_VERSION"; then
  (
    ROOT_LOCK="${OTA_LOCK_FILE:-/run/teslausb/rootfs.lock}"
    ROOT_MOUNT="${OTA_ROOT_MOUNT:-/}"
    mkdir -p "$(dirname "$ROOT_LOCK")"
    exec 8> "$ROOT_LOCK"
    flock -x 8
    if findmnt --fstab -no OPTIONS "$ROOT_MOUNT" | tr ',' '\n' | grep -qx ro \
      && findmnt -no OPTIONS "$ROOT_MOUNT" | tr ',' '\n' | grep -qx rw \
      && ! mount -o remount,ro "$ROOT_MOUNT"; then
      echo "WARNING: could not return $ROOT_MOUNT to read-only" >&2
      exit 0
    fi
    cp "$VERSION_FILE" "$FINALIZED_VERSION"
  )
fi

UNIT=teslausb-telegram-bot.service
PACKAGED_UNIT="${OTA_PACKAGED_TELEGRAM_UNIT:-/root/bin/$UNIT}"
UNIT_DIR="${OTA_INSTALL_UNITS:-/lib/systemd/system}"
if [ -f "${OTA_OVERRIDE_UNITS:-/etc/systemd/system}/$UNIT" ]; then
  UNIT_DIR="${OTA_OVERRIDE_UNITS:-/etc/systemd/system}"
fi
if [ ! -f "$PACKAGED_UNIT" ] || cmp -s "$PACKAGED_UNIT" "$UNIT_DIR/$UNIT"; then
  exit 0
fi
# shellcheck disable=SC1090
source "${OTA_TRANSACTION_LIB:-/root/bin/ota_transaction.sh}"
ota_begin_transaction
# Another backend startup could have completed the migration while we waited.
if cmp -s "$PACKAGED_UNIT" "$UNIT_DIR/$UNIT"; then
  exit 0
fi
BACKUP_ROOT="${OTA_BACKUP_ROOT:-/root/teslausb_backup}"
# The old installer already saved the previous runtime. Extend that same backup
# before changing the unit so the newly installed rollback can restore it too.
# shellcheck disable=SC2012
BACKUP_PATH="$(ls -1dt "$BACKUP_ROOT"/20* 2>/dev/null | head -1)"
[ -d "$BACKUP_PATH/bin" ] || { echo 'No OTA backup for Telegram unit migration' >&2; exit 1; }
if [ ! -f "$BACKUP_PATH/units/install-dir" ]; then
  mkdir -p "$BACKUP_PATH/units"
  if [ -f "$UNIT_DIR/$UNIT" ]; then
    cp -a "$UNIT_DIR/$UNIT" "$BACKUP_PATH/units/$UNIT"
  fi
  printf '%s\n' "$UNIT_DIR" > "$BACKUP_PATH/units/install-dir"
fi
WAS_ACTIVE=false
if systemctl is-active --quiet "$UNIT"; then
  WAS_ACTIVE=true
  systemctl stop "$UNIT"
fi
mkdir -p "$UNIT_DIR"
install -m 644 "$PACKAGED_UNIT" "$UNIT_DIR/$UNIT"
systemctl daemon-reload
if [ "$WAS_ACTIVE" = true ]; then
  systemctl start "$UNIT"
fi
