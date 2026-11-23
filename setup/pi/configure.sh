#!/bin/bash -eu
set -o pipefail

if [ "${BASH_SOURCE[0]}" != "$0" ]
then
  echo "${BASH_SOURCE[0]} must be executed, not sourced"
  return 1 # shouldn't use exit when sourced
fi

function log_progress () {
  if declare -F setup_progress > /dev/null
  then
    setup_progress "configure: $1"
    return
  fi
  echo "configure: $1"
}

if [ "${FLOCKED:-}" != "$0" ]
then
  PARENT="$(ps -o comm= $PPID)"
  if [ "$PARENT" != "setup-teslausb" ]
  then
    log_progress "STOP: $0 must be called from setup-teslausb: $PARENT"
    exit 1
  fi

  if FLOCKED="$0" flock -en -E 99 "$0" "$0" "$@" || case "$?" in
  99) echo already running
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

ARCHIVE_SYSTEM=${ARCHIVE_SYSTEM:-none}

function check_variable () {
    local var_name="$1"
    if [ -z "${!var_name+x}" ]
    then
        log_progress "STOP: Define the variable $var_name like this: export $var_name=value"
        exit 1
    fi
}

# as of March 2021, Raspberry Pi OS still includes a 3 year old version of
# rsync, which has a bug (https://bugzilla.samba.org/show_bug.cgi?id=10494)
# that breaks archiving from snapshots.
# Check that the default rsync works correctly, and install a newer version
# if needed.
function check_default_rsync {
  if ! hash rsync
  then
    apt install rsync
  fi

  rm -rf /tmp/rsynctest
  mkdir -p /tmp/rsynctest/src /tmp/rsynctest/dst
  echo testfile > /tmp/testfile.dat
  echo testfile.dat > /tmp/filelist
  ln -s /tmp/testfile.dat /tmp/rsynctest/src/
  if rsync -avhRL --remove-source-files --no-perms --omit-dir-times --files-from=/tmp/filelist /tmp/rsynctest/src/ /tmp/rsynctest/dst
  then
    if [ -s /tmp/rsynctest/dst/testfile.dat ] && ! [ -e /tmp/rsynctest/src/testfile.dat ]
    then
      rm -rf /tmp/rsynctest
      return 0
    fi
  fi
  return 1
}

function install_prebuilt_rsync {
  local arch
  arch="$(uname -m)"
  if [ "$arch" = "aarch64" ]
  then
    curl -L --fail -o /usr/local/bin/rsync https://github.com/marcone/rsync/releases/download/v3.2.3-arm64/rsync
  elif [[ $arch =~ arm* ]]
  then
    curl -L --fail -o /usr/local/bin/rsync https://github.com/marcone/rsync/releases/download/v3.2.3-rpi/rsync
  else
    log_progress "No prebuilt rsync for '$arch'"
    return 1
  fi
}

function check_rsync {
  if check_default_rsync
  then
    log_progress "rsync seems to work OK"
    return 0
  fi

  log_progress "default rsync doesn't work, installing prebuilt 3.2.3"
  if install_prebuilt_rsync
  then
    chmod a+x /usr/local/bin/rsync
    apt install -y libxxhash0 libssl-dev
    if check_default_rsync
    then
      log_progress "rsync works OK now"
      return 0
    fi
  fi

  log_progress "STOP: rsync doesn't work correctly"
  log_progress "(using '$(which rsync)')"
  exit 1
}

function check_archive_configs () {
    log_progress "Checking archive configs: "

    case "$ARCHIVE_SYSTEM" in
        rsync)
            check_variable "RSYNC_USER"
            check_variable "RSYNC_SERVER"
            check_variable "RSYNC_PATH"
            export ARCHIVE_SERVER="$RSYNC_SERVER"
            check_rsync
            ;;
        rclone)
            check_variable "RCLONE_DRIVE"
            check_variable "RCLONE_PATH"
            export ARCHIVE_SERVER="8.8.8.8" # since it's a cloud hosted drive we'll just set this to google dns
            ;;
        cifs)
            if [ -e /backingfiles/cam_disk.bin ]
            then
              check_variable "SHARE_NAME"
            fi
            check_variable "SHARE_USER"
            check_variable "SHARE_PASSWORD"
            check_variable "ARCHIVE_SERVER"
            check_rsync
            ;;
         nfs)
            export ARCHIVE_SERVER="${NFS_SERVER:-${ARCHIVE_SERVER:-}}"
            export SHARE_NAME="${NFS_PATH:-${SHARE_NAME:-}}"
            if [ -e /backingfiles/cam_disk.bin ]
            then
              check_variable "SHARE_NAME"
            fi
            check_variable "ARCHIVE_SERVER"
            check_rsync
            ;;
        none)
            export ARCHIVE_SERVER=localhost
            ;;
        *)
            log_progress "STOP: Unrecognized archive system: $ARCHIVE_SYSTEM"
            exit 1
            ;;
    esac

    log_progress "done"
}

function get_archive_module () {

    case "$ARCHIVE_SYSTEM" in
        rsync)
            echo "run/rsync_archive"
            ;;
        rclone)
            echo "run/rclone_archive"
            ;;
        cifs)
            echo "run/cifs_archive"
            ;;
         nfs)
            echo "run/nfs_archive"
            ;;
        none)
            echo "run/none_archive"
            ;;
        *)
            log_progress "Internal error: Attempting to configure unrecognized archive system: $ARCHIVE_SYSTEM"
            exit 1
            ;;
    esac
}

function pip3_install () {
  rm -f "/usr/lib/$(py3versions -d)/EXTERNALLY-MANAGED"
  pip3 install "$@"
}

function check_and_install_temperature_monitor () {
  local install_path="$1"

  if [[ -n "${TEMPERATURE_WARNING:+x}" && ! "$TEMPERATURE_WARNING" =~ ^[+-]?[0-9]+$ ]]
  then
    log_progress "STOP: You're trying to set up a temperature Warning threshold that is not an integer."
    exit 1
  fi
  if [[ -n "${TEMPERATURE_CAUTION:+x}" && ! "$TEMPERATURE_CAUTION" =~ ^[+-]?[0-9]+$ ]]
    then
    log_progress "STOP: You're trying to set up a temperature Caution threshold that is not an integer."
    exit 1
  fi
  if [[ -n "${TEMPERATURE_INTERVAL:+x}" && ( ! "$TEMPERATURE_INTERVAL" =~ ^[0-9]+$ || "$TEMPERATURE_INTERVAL" -lt 1 ) ]]
  then
    log_progress "STOP: You're trying to set up a fixed-interval temperature logging time that is not a positive integer of at least 1 minute."
    exit 1
  fi

  log_progress 'Installing temperature monitor script'
  copy_script run/temperature_monitor "$install_path"

  return 0
}

function install_archive_scripts () {
  local install_path="$1"
  local archive_module="$2"

  log_progress "Installing base archive scripts into $install_path"
  copy_script setup/pi/envsetup.sh "$install_path"
  copy_script run/archiveloop "$install_path"
  copy_script run/reconfigure-archive.sh "$install_path"
  copy_script run/check-deps.sh "$install_path"
  mkdir -p "$install_path/lib"
  copy_script run/lib/dependencies.txt "$install_path/lib"
  copy_script run/lib/startup_drives.sh "$install_path/lib"
  copy_script run/ota_finalize.sh "$install_path"
  copy_script run/waitforidle "$install_path"
  copy_script run/remountfs_rw "$install_path"
  copy_script run/os-update.sh "$install_path"
  log_progress "Installing archive module scripts"
  copy_script "$archive_module"/verify-and-configure-archive.sh /tmp
  for backend in cifs nfs rsync rclone none
  do
    local backend_src="run/${backend}_archive"
    local backend_dst="$install_path/lib/backends/$backend"
    mkdir -p "$backend_dst"
    copy_script "$backend_src"/archive-clips.sh "$backend_dst"
    copy_script "$backend_src"/connect-archive.sh "$backend_dst"
    copy_script "$backend_src"/disconnect-archive.sh "$backend_dst"
    copy_script "$backend_src"/archive-is-reachable.sh "$backend_dst"
    if [ -f "$backend_src/copy-music.sh" ]
    then
      copy_script "$backend_src"/copy-music.sh "$backend_dst"
    fi
  done
  copy_script run/lib/backends/wrapper/archive-clips.sh "$install_path"
  copy_script run/lib/backends/wrapper/connect-archive.sh "$install_path"
  copy_script run/lib/backends/wrapper/disconnect-archive.sh "$install_path"
  copy_script run/lib/backends/wrapper/archive-is-reachable.sh "$install_path"
  if [ -n "${MUSIC_SHARE_NAME:+x}" ] && grep -E "cifs|nfs" <<< "$archive_module"
  then
    copy_script "$archive_module"/copy-music.sh "$install_path"
  fi
}

function check_telegram_configuration () {
  if [ "${TELEGRAM_ENABLED:-false}" = "true" ]
  then
    if [ -z "${TELEGRAM_BOT_TOKEN+x}"  ] || [ -z "${TELEGRAM_CHAT_ID:+x}" ]
    then
      log_progress "STOP: You're trying to setup Telegram but didn't provide your Bot Token or Chat id."
      echo "Define the variables in config file like this:"
      echo "export TELEGRAM_CHAT_ID=123456789"
      echo "export TELEGRAM_BOT_TOKEN=bot123456789:abcdefghijklmnopqrstuvqxyz987654321"
      exit 1
    elif [ "${TELEGRAM_BOT_TOKEN}" = "bot123456789:abcdefghijklmnopqrstuvqxyz987654321" ] || [ "${TELEGRAM_CHAT_ID}" = "123456789" ]
    then
      log_progress "STOP: You're trying to setup Telegram, but didn't replace the default values."
      exit 1
    fi
  fi
}

function configure_telegram () {
  if [ "${TELEGRAM_ENABLED:-false}" = "true" ]
  then
    log_progress "Telegram enabled."
  else
    log_progress "Telegram not enabled."
  fi
}

function check_and_configure_telegram () {
  check_telegram_configuration

  configure_telegram
}

function install_push_message_scripts() {
  local install_path="$1"
  mkdir -p "$install_path/lib"
  copy_script run/send-push-message "$install_path"
  copy_script run/teslausb_telegram_bot.py "$install_path"
  copy_script run/lib/paths.sh "$install_path/lib"
}

function install_ota_scripts() {
  local install_path="$1"
  copy_script run/ota_install.sh "$install_path"
  copy_script run/ota_rollback.sh "$install_path"
  copy_script run/ota_transaction.sh "$install_path"
}

function install_telegram_bot_service() {
  log_progress "Installing Telegram bot service"
  install -m 644 "$SOURCE_DIR/setup/pi/teslausb-telegram-bot.service" \
    /lib/systemd/system/teslausb-telegram-bot.service
  systemctl daemon-reload
  if [ "${TELEGRAM_ENABLED:-false}" = "true" ]; then
    systemctl enable teslausb-telegram-bot.service
    systemctl start teslausb-telegram-bot.service || true
  fi
}

if [[ $EUID -ne 0 ]]
then
    log_progress "STOP: Run sudo -i."
    exit 1
fi

mkdir -p /root/bin

check_and_install_temperature_monitor /root/bin
check_and_configure_telegram
install_push_message_scripts /root/bin
install_ota_scripts /root/bin
install_telegram_bot_service

check_archive_configs

rm -f /root/teslausb.conf
rm -rf /mutable/TeslaCam/RecentClips/event.json

archive_module="$( get_archive_module )"
log_progress "Using archive module: $archive_module"

install_archive_scripts /root/bin "$archive_module"
/tmp/verify-and-configure-archive.sh
log_progress "Installing runtime dependencies"
if ! /root/bin/check-deps.sh --install
then
  log_progress "STOP: runtime dependencies are missing (listed above)"
  exit 1
fi

systemctl disable teslausb.service || true

cat << EOF > /lib/systemd/system/teslausb.service
[Unit]
Description=TeslaUSB archiveloop service
DefaultDependencies=no
After=mutable.mount backingfiles.mount

[Service]
Type=simple
ExecStart=/bin/bash /root/bin/archiveloop
Restart=always

[Install]
WantedBy=backingfiles.mount
EOF

systemctl enable teslausb.service
