#!/bin/bash
# Single source of truth for teslausb filesystem paths.
# Source this file; do not execute it directly.
# All paths can be overridden before sourcing (useful for tests).

# Persistent R/W overlay — survives across camera-USB disconnects.
export MUTABLE_DIR="${MUTABLE_DIR:-/mutable}"

# Backing image files (cam_disk.bin, music_disk.bin).
export BACKING_DIR="${BACKING_DIR:-/backingfiles}"

# Snapshot store (one sub-dir per snapshot).
export SNAPSHOTS_DIR="${SNAPSHOTS_DIR:-${BACKING_DIR}/snapshots}"

# Ephemeral snapshot mount targets (tmpfs; re-created on each boot).
export SNAP_MNT_BASE="${SNAP_MNT_BASE:-/tmp/snapshots}"

# Camera FAT image, loopback-mounted here.
export CAM_MOUNT="${CAM_MOUNT:-/mnt/cam}"

# Archive share mount point (CIFS / NFS / etc.).
export ARCHIVE_MOUNT="${ARCHIVE_MOUNT:-/mnt/archive}"

# Music share mount point (empty unless a music drive is configured).
export MUSIC_ARCHIVE_MOUNT="${MUSIC_ARCHIVE_MOUNT:-}"

# Archiveloop log (on MUTABLE_DIR so it persists across readonly root).
export LOG_FILE="${LOG_FILE:-${MUTABLE_DIR}/archiveloop.log}"

# Archive state file — written by archiveloop, read by the Go backend.
# Lives under /run (tmpfs) so it is always writable without remounting.
export ARCHIVE_STATE_FILE="${ARCHIVE_STATE_FILE:-/run/teslausb/archive_state}"

# Cam disk image.
export CAM_DISK="${CAM_DISK:-${BACKING_DIR}/cam_disk.bin}"

# TeslaCam overlay source (what the car writes to through the USB gadget).
export TESLACAM_DIR="${TESLACAM_DIR:-${MUTABLE_DIR}/TeslaCam}"
