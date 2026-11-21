#!/bin/bash

OTA_LOCK_FILE="${OTA_LOCK_FILE:-/run/teslausb/rootfs.lock}"
OTA_ROOT_MOUNT="${OTA_ROOT_MOUNT:-/}"
OTA_ROOT_WAS_RO=false

# Stop the backend socket before its service. A browser reconnect would
# otherwise socket-activate the old binary while the new one is being copied.
function ota_stop_services() {
  if [ "${TELEGRAM_WAS_ACTIVE:-false}" = "true" ]
  then
    systemctl stop "${TELEGRAM_UNIT:-teslausb-telegram-bot.service}" || true
  fi
  systemctl stop teslausb-backend.socket || true
  systemctl stop teslausb-backend || true
  systemctl stop teslausb || true
  OTA_SERVICES_STOPPED=true
}

function ota_start_services() {
  systemctl start teslausb-backend.socket || true
  systemctl start teslausb-backend || true
  systemctl start teslausb || true
  if [ "${TELEGRAM_WAS_ACTIVE:-false}" = "true" ]
  then
    systemctl start "${TELEGRAM_UNIT:-teslausb-telegram-bot.service}"
  fi
}

function ota_end_transaction() {
  local status=$?
  trap - EXIT
  if [ "$status" -ne 0 ] && [ "${OTA_SERVICES_STOPPED:-false}" = "true" ]
  then
    systemctl start teslausb-backend.socket || true
    systemctl start teslausb-backend || true
    systemctl start teslausb || true
    if [ "${TELEGRAM_WAS_ACTIVE:-false}" = "true" ]
    then
      systemctl start "${TELEGRAM_UNIT:-teslausb-telegram-bot.service}" || true
    fi
  fi
  if [ "$OTA_ROOT_WAS_RO" = "true" ]
  then
    mount -o remount,ro "$OTA_ROOT_MOUNT"
  fi
  flock -u "$OTA_LOCK_FD" || true
}

function ota_begin_transaction() {
  mkdir -p "$(dirname "$OTA_LOCK_FILE")"
  exec {OTA_LOCK_FD}>"$OTA_LOCK_FILE"
  flock -x "$OTA_LOCK_FD"

  if findmnt -no OPTIONS "$OTA_ROOT_MOUNT" | tr ',' '\n' | grep -qx ro
  then
    OTA_ROOT_WAS_RO=true
  fi
  mount -o remount,rw "$OTA_ROOT_MOUNT"
  trap ota_end_transaction EXIT
}
