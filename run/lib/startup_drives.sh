#!/bin/bash
# Decides what archiveloop may do to the USB drive images when it starts.
# Source this file; do not execute it directly.
#
# archiveloop also starts after a service restart, when the car may still be
# writing to the drive. Repairing an image then can damage clips, so startup
# repair only runs once the image is attached to nothing and mounted nowhere.

USB_GADGET_ROOT="${USB_GADGET_ROOT:-/sys/kernel/config/usb_gadget/teslausb}"
UDC_CLASS_ROOT="${UDC_CLASS_ROOT:-/sys/class/udc}"
DISABLE_GADGET_SCRIPT="${DISABLE_GADGET_SCRIPT:-/root/bin/disable_gadget.sh}"
STARTUP_DRIVE_RETRY_SECONDS="${STARTUP_DRIVE_RETRY_SECONDS:-5}"

function usb_gadget_is_present () {
  [ -d "$USB_GADGET_ROOT" ]
}

# Any state other than "not attached" counts as in use, including an unknown one.
function usb_host_may_be_using_drives () {
  local udc state
  if ! udc=$(cat "$USB_GADGET_ROOT/UDC" 2> /dev/null)
  then
    return 0
  fi
  [ -n "$udc" ] || return 1
  state=$(cat "$UDC_CLASS_ROOT/$udc/state" 2> /dev/null || echo unknown)
  [ "$state" != "not attached" ]
}

function mounted_usb_drive_mounts () {
  local mount_point
  for mount_point in "$CAM_MOUNT" "${MUSIC_MOUNT:-/mnt/music}" \
    "${LIGHTSHOW_MOUNT:-/mnt/lightshow}" "${BOOMBOX_MOUNT:-/mnt/boombox}"
  do
    if findmnt --mountpoint "$mount_point" > /dev/null 2>&1
    then
      printf '%s\n' "$mount_point"
    fi
  done
}

# Prints boot, unmount, keep, detach or detach_unmount.
function startup_drive_action () {
  local mounted
  mounted=$(mounted_usb_drive_mounts)
  if ! usb_gadget_is_present
  then
    if [ -n "$mounted" ]
    then
      echo unmount
    else
      echo boot
    fi
  elif [ -n "$mounted" ]
  then
    echo detach_unmount
  elif usb_host_may_be_using_drives
  then
    echo keep
  else
    echo detach
  fi
}

# Returns 0 when the images are free for startup repair, and 1 when the drive
# must stay attached. It never lazy-unmounts, and it keeps retrying rather than
# repairing or attaching an image that is still in use.
function prepare_drives_for_startup () {
  local action mount_point failed reported=false previous_state
  previous_state=$(cat "$ARCHIVE_STATE_FILE" 2> /dev/null || echo idle)
  while true
  do
    action=$(startup_drive_action)
    failed=false
    case "$action" in
      boot)
        [ "$reported" = false ] || write_archive_state "$previous_state"
        return 0
        ;;
      keep)
        [ "$reported" = false ] || write_archive_state "$previous_state"
        log "USB drive may be in use, leaving it attached and skipping startup repair."
        return 1
        ;;
      detach|detach_unmount)
        [ "$reported" = true ] || log "Detaching USB drive before startup repair..."
        "$DISABLE_GADGET_SCRIPT" >> "$LOG_FILE" 2>&1 || true
        if usb_gadget_is_present
        then
          failed=true
        fi
        ;;
      unmount)
        while IFS= read -r mount_point
        do
          [ "$reported" = true ] || log "Unmounting $mount_point before startup repair..."
          umount "$mount_point" >> "$LOG_FILE" 2>&1 || failed=true
        done < <(mounted_usb_drive_mounts)
        ;;
      *)
        failed=true
        ;;
    esac
    if [ "$failed" = true ]
    then
      if [ "$reported" = false ]
      then
        log "Could not release the USB drive safely, retrying."
        write_archive_state "error: Could not release the USB drive safely"
        reported=true
      fi
      sleep "$STARTUP_DRIVE_RETRY_SECONDS"
    fi
  done
}
