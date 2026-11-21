#!/bin/bash -eu
set -o pipefail
#
# os-update.sh — install operating system package updates (apt) on a board
# whose root filesystem is normally read-only.
#
# Usage:
#   os-update.sh check               list pending updates; writes nothing to the card
#   os-update.sh apply [--no-reboot] install them, then reboot
#   os-update.sh repair              finish an update that lost power (runs at boot)
#
# Why not plain "apt-get upgrade":
#   - / and the boot partition are read-only and have to be remounted;
#   - the Go backend and the app updater remount / read-only when they finish
#     a write, so the upgrade must hold the same lock they take;
#   - /tmp and /var/tmp are RAM, too small for rebuilding an initramfs;
#   - a config-file question from dpkg would hang an unattended run;
#   - a package upgrade reverts two package files that setup edits;
#   - the car can cut power at any time, so an interrupted upgrade has to be
#     finished at the next boot.
#
# What it cannot protect against: power loss while the kernel or boot files are
# being written can leave the board unable to boot. Run "apply" while the board
# has steady power.

LOCK_FILE="${OS_UPDATE_LOCK_FILE:-/run/teslausb/rootfs.lock}"
STATE_DIR="${OS_UPDATE_STATE_DIR:-/mutable}"
MARKER="$STATE_DIR/os-update.in-progress"
LOG_FILE="${OS_UPDATE_LOG:-$STATE_DIR/os-update.log}"
WORK_DIR="${OS_UPDATE_WORK_DIR:-/var/cache/teslausb-os-update}"
CHECK_DIR="${OS_UPDATE_CHECK_DIR:-/tmp/os-update-check}"
ETC_DIR="${OS_UPDATE_ETC_DIR:-/etc}"
LIB_DIR="${OS_UPDATE_LIB_DIR:-/usr/lib}"
SELF="${OS_UPDATE_SELF:-/root/bin/os-update.sh}"
REPAIR_UNIT="teslausb-os-update-repair.service"
# Free space wanted on / beyond what is downloaded: packages unpack to several
# times their download size before the old files are removed.
UNPACK_FACTOR=4
SPARE_KB=153600

REMOUNTED_RO=()
LOCK_FD=""
REBOOT_AFTER=false

function log () {
  local line
  line="$(date '+%Y-%m-%d %H:%M:%S') $*"
  echo "$line"
  if [ -d "$(dirname "$LOG_FILE")" ]
  then
    echo "$line" >> "$LOG_FILE" 2> /dev/null || true
  fi
}

function die () {
  log "ERROR: $*"
  exit 1
}

function require_root () {
  if [ "${OS_UPDATE_SKIP_ROOT_CHECK:-false}" != "true" ] && [ "$(id -u)" -ne 0 ]
  then
    echo "os-update: run as root (sudo -i)" >&2
    exit 1
  fi
}

function is_read_only () {
  findmnt -no OPTIONS "$1" 2> /dev/null | tr ',' '\n' | grep -qx ro
}

function boot_mounts () {
  local mountpoint
  for mountpoint in /boot/firmware /boot
  do
    if findmnt "$mountpoint" > /dev/null 2>&1
    then
      echo "$mountpoint"
    fi
  done
}

# Take the lock the backend and the app updater use around their own
# remounts, then make / and the boot partition writable.
function begin_writes () {
  mkdir -p "$(dirname "$LOCK_FILE")"
  exec {LOCK_FD}> "$LOCK_FILE"
  if ! flock -w 600 -x "$LOCK_FD"
  then
    die "another update or configuration change is still running"
  fi
  trap end_writes EXIT

  local mountpoint mountpoints
  mapfile -t mountpoints < <(echo /; boot_mounts)
  for mountpoint in "${mountpoints[@]}"
  do
    if is_read_only "$mountpoint"
    then
      mount -o remount,rw "$mountpoint"
      REMOUNTED_RO+=("$mountpoint")
    fi
  done
}

function end_writes () {
  local status=$?
  trap - EXIT
  rm -rf "$WORK_DIR"
  sync
  local mountpoint
  for mountpoint in ${REMOUNTED_RO[@]+"${REMOUNTED_RO[@]}"}
  do
    mount -o remount,ro "$mountpoint" || log "could not remount $mountpoint read-only; it will be after a reboot"
  done
  if [ -n "$LOCK_FD" ]
  then
    flock -u "$LOCK_FD" || true
  fi
  if [ "$REBOOT_AFTER" = true ] && [ "$status" -eq 0 ]
  then
    systemctl reboot
  fi
  exit "$status"
}

APT_OPTIONS=(
  -y
  -o Dpkg::Options::=--force-confdef
  -o Dpkg::Options::=--force-confold
  -o APT::Get::Upgrade-Allow-New=true
)

function apt_env () {
  # TMPDIR on the card: update-initramfs needs more room than the RAM-backed
  # /var/tmp has on a 512 MB board.
  mkdir -p "$WORK_DIR"
  export TMPDIR="$WORK_DIR"
  export DEBIAN_FRONTEND=noninteractive
  export APT_LISTCHANGES_FRONTEND=none
  export NEEDRESTART_MODE=a
  export LC_ALL=C
}

# Setup edits two files that packages own. An upgrade puts the packaged
# versions back, so keep our versions in /etc, where they take precedence and
# survive upgrades.
function keep_setup_overrides () {
  local unit="$LIB_DIR/systemd/system/fake-hwclock.service"
  if [ -e "$unit" ] && [ -L "$ETC_DIR/fake-hwclock.data" ]
  then
    # The saved clock lives on /mutable, so the unit must wait for that mount.
    mkdir -p "$ETC_DIR/systemd/system"
    sed 's/^Before=.*/After=mutable.mount/' "$unit" > "$ETC_DIR/systemd/system/fake-hwclock.service"
  fi

  # Only where setup made that edit: the packaged file still carries it, or an
  # earlier run already saved it.
  local tmpfiles="$LIB_DIR/tmpfiles.d/var.conf"
  if [ -e "$tmpfiles" ] && { grep -q 'spool[[:space:]]*1777' "$tmpfiles" || [ -e "$ETC_DIR/tmpfiles.d/var.conf" ]; }
  then
    mkdir -p "$ETC_DIR/tmpfiles.d"
    sed 's/spool[[:space:]]*0755/spool 1777/g' "$tmpfiles" > "$ETC_DIR/tmpfiles.d/var.conf"
  fi
}

# A unit that finishes an interrupted update at the next boot. It is written
# here rather than by setup so that boards updated over the air get it too.
# It does not hold back teslausb.service: the car gets its drive either way.
function install_repair_unit () {
  local unit_file="$ETC_DIR/systemd/system/$REPAIR_UNIT"
  mkdir -p "$(dirname "$unit_file")"
  cat > "$unit_file" << EOF
[Unit]
Description=Finish an interrupted operating system update
ConditionPathExists=$MARKER
RequiresMountsFor=$STATE_DIR
After=local-fs.target

[Service]
Type=oneshot
ExecStart=$SELF repair
TimeoutStartSec=30min

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload || true
  systemctl enable "$REPAIR_UNIT" > /dev/null 2>&1 || log "could not enable $REPAIR_UNIT"
}

function pending_packages () {
  # "Inst name [old] (new origin [arch])" lines of a simulated upgrade.
  apt-get "$@" -s -o APT::Get::Upgrade-Allow-New=true upgrade 2> /dev/null | awk '
    $1 == "Inst" {
      old = ""; new = $3
      if ($3 ~ /^\[/) { old = $3; new = $4 }
      gsub(/[\[\]()]/, "", old); gsub(/[()]/, "", new)
      printf "%s %s %s\n", $2, (old == "" ? "-" : old), new
    }'
}

function download_kb () {
  # --print-uris lists "'uri' file size hash" for everything to download.
  apt-get "${APT_OPTIONS[@]}" --print-uris upgrade 2> /dev/null | awk '
    /^\047/ { bytes += $3 }
    END { printf "%d\n", (bytes + 1023) / 1024 }'
}

function free_kb () {
  df -Pk "$1" | awk 'NR == 2 { print $4 }'
}

function cmd_check () {
  # Everything goes to RAM, so this works with the root filesystem read-only.
  rm -rf "$CHECK_DIR"
  mkdir -p "$CHECK_DIR/lists/partial" "$CHECK_DIR/archives/partial"
  local options=(
    -o "Dir::State::lists=$CHECK_DIR/lists"
    -o "Dir::Cache::archives=$CHECK_DIR/archives"
    -o Dir::Cache::pkgcache=
    -o Dir::Cache::srcpkgcache=
    -o Acquire::GzipIndexes=true
    -o Debug::NoLocking=true
  )
  export LC_ALL=C
  if ! apt-get "${options[@]}" -qq update
  then
    rm -rf "$CHECK_DIR"
    echo "os-update: could not reach the package servers" >&2
    exit 1
  fi
  local pending
  pending="$(pending_packages "${options[@]}")"
  rm -rf "$CHECK_DIR"
  if [ -z "$pending" ]
  then
    echo "The operating system is up to date."
    return 0
  fi
  echo "$(wc -l <<< "$pending" | tr -d ' ') package update(s) available:"
  awk '{ printf "  %-32s %s -> %s\n", $1, $2, $3 }' <<< "$pending"
  echo "Install them with: sudo $SELF apply"
}

function cmd_apply () {
  local reboot_after=true
  if [ "${1:-}" = "--no-reboot" ]
  then
    reboot_after=false
  fi

  begin_writes
  apt_env
  log "os-update: starting"
  install_repair_unit
  keep_setup_overrides

  # Finish anything an earlier run left half-configured before adding to it.
  dpkg --configure -a || die "dpkg could not finish a previous installation"
  apt-get -qq update || die "could not reach the package servers"

  local pending count
  pending="$(pending_packages)"
  if [ -z "$pending" ]
  then
    log "os-update: nothing to do"
    return 0
  fi
  count="$(wc -l <<< "$pending" | tr -d ' ')"

  local need have
  need=$(( $(download_kb) * UNPACK_FACTOR + SPARE_KB ))
  have="$(free_kb /)"
  if [ "$have" -lt "$need" ]
  then
    die "not enough free space on /: $((have / 1024)) MB free, about $((need / 1024)) MB wanted for $count packages. Nothing was changed."
  fi

  log "os-update: upgrading $count package(s)"
  date > "$MARKER"
  sync
  if ! apt-get "${APT_OPTIONS[@]}" upgrade
  then
    die "apt-get upgrade failed; the update will be retried at the next boot. See $LOG_FILE"
  fi
  apt-get clean || true
  keep_setup_overrides
  rm -f "$MARKER"
  sync
  log "os-update: installed $count package(s)"

  if [ "$reboot_after" = true ]
  then
    log "os-update: rebooting"
    REBOOT_AFTER=true # end_writes reboots once the filesystems are settled
  else
    log "os-update: reboot the board to finish (a new kernel only loads after a reboot)"
  fi
}

function cmd_repair () {
  if [ ! -e "$MARKER" ]
  then
    echo "os-update: no interrupted update"
    return 0
  fi
  begin_writes
  apt_env
  log "os-update: finishing an update that was interrupted"
  # dpkg replays its journal; this needs no network.
  if dpkg --configure -a
  then
    # Unpack anything that was downloaded but not yet installed, if we are online.
    apt-get "${APT_OPTIONS[@]}" -f install || log "os-update: could not complete missing packages (offline?)"
    keep_setup_overrides
    rm -f "$MARKER"
    log "os-update: interrupted update finished"
  else
    log "os-update: dpkg could not finish; run 'sudo $SELF apply' when the board is online"
  fi
}

case "${1:-}" in
  check)
    cmd_check
    ;;
  apply)
    require_root
    cmd_apply "${2:-}"
    ;;
  repair)
    require_root
    cmd_repair
    ;;
  *)
    sed -n '4,10p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
