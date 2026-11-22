#!/bin/bash -eu
# First-upgrade compatibility: old installers copy bin/ but ignore units/.
# Called at backend startup; wait for the installer's rootfs lock before writes.
set -o pipefail

# Boards set up before v0.1.1 let syslogd keep log files open on the root
# filesystem, which leaves it writable after any update or settings change.
# Keep logs in RAM, and put a board that was left writable back to read-only.
SYSLOG_DEFAULTS="${OTA_SYSLOG_DEFAULTS:-/etc/default/busybox-syslogd}"
SYSLOG_SETTING='SYSLOG_OPTS="-C128 -f /dev/null"'
if [ -f "$SYSLOG_DEFAULTS" ] && ! grep -qxF "$SYSLOG_SETTING" "$SYSLOG_DEFAULTS"; then
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
    systemctl try-restart busybox-syslogd.service
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
