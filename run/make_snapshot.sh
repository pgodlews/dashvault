#!/bin/bash -eu
set -o pipefail

if [ "${BASH_SOURCE[0]}" != "$0" ]
then
  echo "${BASH_SOURCE[0]} must be executed, not sourced"
  return 1 # shouldn't use exit when sourced
fi

# shellcheck source=run/lib/paths.sh
source "$(dirname "$0")/lib/paths.sh"

if ! type -t log >/dev/null 2>&1; then
  function log {
    echo "$@"
  }
  export -f log
fi

if ! type -t losetup_find_show >/dev/null 2>&1; then
  function losetup_find_show {
    losetup -f --show "$@"
  }
  export -f losetup_find_show
fi

if [ "${FLOCKED:-}" != "$0" ]
then
  mkdir -p "${SNAPSHOTS_DIR}"
  if FLOCKED="$0" flock -E 99 "${SNAPSHOTS_DIR}" "$0" "$@" || case "$?" in
  99) echo "failed to lock snapshots dir"
      exit 99
      ;;
  *)  exit $?
      ;;
  esac
  then
    # success
    exit 0
  fi
fi

function linksnapshotfiletorecents {
  local file=$1
  local curmnt=$2
  local finalmnt=$3
  local recents="${TESLACAM_DIR}/RecentClips"

  filename=${file##/*/}
  if [[ ! "$filename" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}.* ]]
  then
    return
  fi

  filedate=${filename:0:10}
  if [ ! -d "$recents/$filedate" ]
  then
    mkdir -p "$recents/$filedate"
  fi
  ln -sf "${file/"$curmnt"/$finalmnt}" "$recents/$filedate"
}

function make_links_for_snapshot {
  local saved="${TESLACAM_DIR}/SavedClips"
  local sentry="${TESLACAM_DIR}/SentryClips"
  local track="${TESLACAM_DIR}/TeslaTrackMode"
  if [ ! -d $saved ]
  then
    mkdir -p $saved
  fi
  if [ ! -d $sentry ]
  then
    mkdir -p $sentry
  fi
  local curmnt="$1"
  local finalmnt="$2"
  log "making links for $curmnt, retargeted to $finalmnt"
  local restore_nullglob
  restore_nullglob=$(shopt -p nullglob)
  shopt -s nullglob
  for f in "$curmnt/TeslaCam/RecentClips/"*
  do
    #log "linking $f"
    linksnapshotfiletorecents "$f" "$curmnt" "$finalmnt"
  done
  # also link in any files that were moved to SavedClips
  for f in "$curmnt/TeslaCam/SavedClips"/*/*
  do
    #log "linking $f"
    linksnapshotfiletorecents "$f" "$curmnt" "$finalmnt"
    # also link it into a SavedClips folder
    local eventfolder=${f%/*}
    local eventtime=${eventfolder##/*/}
    if [ ! -d "$saved/$eventtime" ]
    then
      mkdir -p "$saved/$eventtime"
    fi
    ln -sf "${f/$curmnt/$finalmnt}" "$saved/$eventtime"
  done
  # and the same for SentryClips
  for f in "$curmnt/TeslaCam/SentryClips/"*/*
  do
    #log "linking $f"
    linksnapshotfiletorecents "$f" "$curmnt" "$finalmnt"
    local eventfolder=${f%/*}
    local eventtime=${eventfolder##/*/}
    if [ ! -d "$sentry/$eventtime" ]
    then
      mkdir -p "$sentry/$eventtime"
    fi
    ln -sf "${f/$curmnt/$finalmnt}" "$sentry/$eventtime"
  done
  # and finally the TrackMode files
  for f in "$curmnt/TeslaTrackMode/"*
  do
    if [ ! -d "$track" ]
    then
      mkdir -p "$track"
    fi
    ln -sf "$f" "$track"
  done
  log "made all links for $curmnt"
  $restore_nullglob
}

# Experimental (RECENTCLIPS_PRUNE_SNAPSHOTS): delete RecentClips of cameras not in
# RECENTCLIPS_CAMERAS from a snapshot. Only TeslaCam/RecentClips is touched;
# SavedClips, SentryClips and track mode keep every camera, and so does the
# drive the car writes to. The front camera is kept while the telemetry map is
# on, because tracks are read from it. Prints the number of files deleted.
function prune_recent_cameras () {
  local root="$1"
  local wanted="${RECENTCLIPS_CAMERAS:-}"
  wanted="${wanted//,/ }"
  if [ -z "${wanted// /}" ] || [ ! -d "$root/TeslaCam/RecentClips" ]
  then
    echo 0
    return
  fi
  if [ "${TELEMETRY_MAP_ENABLED:-false}" = "true" ]
  then
    wanted+=" front"
  fi
  local camera skip=""
  for camera in front back left_repeater right_repeater left_pillar right_pillar
  do
    case " $wanted " in
      *" $camera "*) ;;
      *) skip+="${skip:+|}$camera" ;;
    esac
  done
  local -i removed=0
  if [ -n "$skip" ]
  then
    local -r pattern="^[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}-[0-9]{2}-[0-9]{2}-($skip)\.mp4$"
    local restore_nullglob f
    # shopt -p exits 1 when the option is off; that is not an error here.
    restore_nullglob=$(shopt -p nullglob || true)
    shopt -s nullglob
    for f in "$root/TeslaCam/RecentClips/"*.mp4 "$root/TeslaCam/RecentClips/"*/*.mp4
    do
      if [[ "${f##*/}" =~ $pattern ]] && [ -f "$f" ] && [ ! -L "$f" ]
      then
        rm -f -- "$f"
        removed+=1
      fi
    done
    $restore_nullglob
  fi
  echo "$removed"
}

# Mount a new snapshot read-write, prune it, and trim it so the deleted clips'
# blocks are released once no other snapshot or the car's drive holds them.
# Runs before the snapshot is mounted read-only or linked into RecentClips, so
# nothing reads it meanwhile. A power cut here leaves the snapshot without a
# .toc, and the next run deletes it as incomplete. Failures never fail the
# snapshot: it is kept unpruned.
function prune_snapshot () {
  local image="$1"
  if [ "${RECENTCLIPS_PRUNE_SNAPSHOTS:-false}" != "true" ] || [ -z "${RECENTCLIPS_CAMERAS:-}" ]
  then
    return 0
  fi
  local tmpmnt removed start
  start=$(date +%s.%N)
  tmpmnt=$(mktemp -d)
  if ! /root/bin/mountimage "$image" "$tmpmnt" rw
  then
    log "pruning: could not mount $image, keeping every camera"
    rmdir "$tmpmnt"
    return 0
  fi
  removed=$(prune_recent_cameras "$tmpmnt") || removed="?"
  if [ "$removed" != "0" ]
  then
    fstrim "$tmpmnt" || log "pruning: fstrim failed, space is released when the snapshot is deleted"
  fi
  umount "$tmpmnt" || log "pruning: could not unmount $tmpmnt"
  rmdir "$tmpmnt" || true
  log "pruning: removed $removed RecentClips file(s) from other cameras in $(awk -v s="$start" -v e="$(date +%s.%N)" 'BEGIN {printf "%.1f", e - s}')s"
}

function snapshot {
  # since taking a snapshot doesn't take much extra space, do that first,
  # before cleaning up old snapshots to maintain free space.
  local oldnum=-1
  local newnum=0
  if stat ${SNAPSHOTS_DIR}/snap-*/snap.bin > /dev/null 2>&1
  then
    oldnum=$(find ${SNAPSHOTS_DIR}/snap-* -maxdepth 1 -name snap.bin | sort | tail -1 | tr -c -d '[:digit:]' | sed 's/^0*//' )
    [ -z "$oldnum" ] && oldnum=0
    newnum=$((oldnum + 1))
  fi
  local oldname
  local newsnapdir
  oldname=${SNAPSHOTS_DIR}/snap-$(printf "%06d" "$oldnum")/snap.bin

  # check that the previous snapshot is complete
  if [ ! -e "${oldname}.toc" ] && [ "$oldnum" != "-1" ]
  then
    log "previous snapshot was incomplete, deleting"
    rm -rf "$(dirname "$oldname")"
    newnum=$((oldnum))
    oldnum=$((oldnum - 1))
    oldname=${SNAPSHOTS_DIR}/snap-$(printf "%06d" "$oldnum")/snap.bin
  fi

  newsnapdir=${SNAPSHOTS_DIR}/snap-$(printf "%06d" $newnum)
  newsnapmnt=${SNAP_MNT_BASE}/snap-$(printf "%06d" $newnum)

  local newsnapname=$newsnapdir/snap.bin
  log "taking snapshot of cam disk in $newsnapdir"

  if mount | grep ${CAM_DISK}
  then
    echo "snapshot already mounted"
  fi

  SNAPDIR=$(dirname "$newsnapname")
  if [ ! -d "$SNAPDIR" ]
  then
    mkdir -p "$SNAPDIR"
  fi

  if [ -e "$newsnapname" ]
  then
    umount "$newsnapmnt" || true
    rm -rf "$newsnapname"
  fi

  # make a copy-on-write snapshot of the current image
  cp --reflink=always ${CAM_DISK} "$newsnapname"
  # at this point we have a snapshot of the cam image, which is completely
  # independent of the still in-use image exposed to the car

  # create loopback and scan the partition table, this will create an additional
  # loop device in addition to the main loop device, e.g. /dev/loop0 and
  # /dev/loop0p1

  # Use -p repair arg. It works with vfat and exfat.
  LOOP=$(losetup_find_show -P "$newsnapname")
  PARTLOOP=${LOOP}p1

  if [ "$1" = "fsck" ]
  then
    fsck "$PARTLOOP" -- -p || true
  fi

  losetup -d "$LOOP"

  prune_snapshot "$newsnapname"

  # if needed, manually mount the image and check/fix timestamps
  # shellcheck disable=SC1091
  if [ "$(getconf LONG_BIT)" = "32" ] && [ "$(. /etc/os-release && echo "${VERSION_ID:-}")" = "12" ]
  then
    local -r tmpmnt=$(mktemp -d)
    /root/bin/mountimage "$newsnapname" "$tmpmnt" rw
    find "$tmpmnt" -newerat 20380101 -print0 | xargs -0 -r touch
    umount "$tmpmnt"
    rmdir "$tmpmnt"
  fi

  while ! systemctl --quiet is-active autofs
  do
    log "waiting for autofs to be active"
    sleep 1
  done
  log "took snapshot"

  # Wait for automount to mount directory
  for i in $(seq 1 30); do
    if [ -d "$newsnapmnt/TeslaCam" ]; then
      break
    fi
    ls "$newsnapmnt" >/dev/null 2>&1 || true
    sleep 0.2
  done

  # check whether this snapshot is actually different from the previous one
  find "$newsnapmnt" -type f -printf '%s %P\n' > "${newsnapname}.toc_"
  log "comparing new snapshot with $oldname"
  has_changes=false
  if [ ! -e "${oldname}.toc" ]; then
    has_changes=true
  elif (set +o pipefail; diff "${oldname}.toc" "${newsnapname}.toc_" | grep -qe '^>'); then
    has_changes=true
  fi

  if [ "$has_changes" = true ]
  then
    ln -s "$newsnapmnt" "$newsnapdir/mnt"
    make_links_for_snapshot "$newsnapmnt" "$newsnapdir/mnt"
    mv "${newsnapname}.toc_" "${newsnapname}.toc"
  else
    log "new snapshot is identical to previous one, discarding"
    /root/bin/release_snapshot.sh "$newsnapdir"
    rm -rf "$newsnapdir"
  fi
}

if ! snapshot "${1:-fsck}"
then
  log "failed to take snapshot"
fi
