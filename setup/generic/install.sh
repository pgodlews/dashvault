#!/bin/bash -eu
set -o pipefail
#
# Pre-install script to make things look sufficiently like what
# the main Raspberry Pi centric install scripts expect.
#
# Installs the latest Dashvault release on a Debian-based image that you have
# downloaded from your board's vendor. Nothing from that image is redistributed.
# Setting REPO or BRANCH installs from that branch instead (for development),
# and SOURCE_DIR installs from a local copy.
#

BRANCH_INSTALL=false
if [ -n "${REPO:-}" ] || [ -n "${BRANCH:-}" ]
then
  BRANCH_INSTALL=true
fi
REPO=${REPO:-pgodlews}
BRANCH=${BRANCH:-main}
RAW_BASE_URL=${RAW_BASE_URL:-https://raw.githubusercontent.com/${REPO}/dashvault/${BRANCH}}
RELEASE_BASE_URL=${RELEASE_BASE_URL:-https://github.com/${REPO}/dashvault/releases/latest/download}
RELEASE_DIR=${RELEASE_DIR:-/usr/local/share/teslausb-release}

if [[ $EUID -ne 0 ]]
then
  echo "STOP: Run sudo -i."
  exit 1
fi

if [ ! -L /teslausb ]
then
  rm -rf /teslausb
  if [ -d /boot/firmware ] && findmnt --fstab /boot/firmware &> /dev/null
  then
    ln -s /boot/firmware /teslausb
  else
    ln -s /boot /teslausb
  fi
fi

function error_exit {
  echo "STOP: $*"
  exit 1
}

function fetch_source {
  local source_path="$1"
  local destination="$2"
  if [ -n "${SOURCE_DIR:-}" ]
  then
    cp "$SOURCE_DIR/$source_path" "$destination"
    return
  fi
  while ! curl -s --fail -o "$destination" "$RAW_BASE_URL/$source_path"
  do
    sleep 1
  done
}

# Download and verify the release, which has the compiled backend and web app.
# There is no fallback: a failed download must not install something else.
function use_release {
  if [ -n "${SOURCE_DIR:-}" ] || [ "$BRANCH_INSTALL" = true ]
  then
    return 0
  fi
  if [ ! -f "$RELEASE_DIR/VERSION" ]
  then
    local work
    mkdir -p "$(dirname "$RELEASE_DIR")"
    work=$(mktemp -d -p "$(dirname "$RELEASE_DIR")")
    if ! curl -fsSL --retry 5 -o "$work/teslausb-release.tar.gz" "$RELEASE_BASE_URL/teslausb-release.tar.gz" \
      || ! curl -fsSL --retry 5 -o "$work/SHA256SUMS" "$RELEASE_BASE_URL/SHA256SUMS"
    then
      rm -rf "$work"
      error_exit "Could not download the release from $RELEASE_BASE_URL"
    fi
    if ! (cd "$work" && grep ' teslausb-release.tar.gz$' SHA256SUMS | sha256sum -c > /dev/null 2>&1)
    then
      rm -rf "$work"
      error_exit "The downloaded release failed its checksum."
    fi
    mkdir "$work/release"
    tar -xzf "$work/teslausb-release.tar.gz" -C "$work/release"
    if [ ! -f "$work/release/VERSION" ]
    then
      rm -rf "$work"
      error_exit "The downloaded release is incomplete."
    fi
    mv "$work/release" "$RELEASE_DIR"
    rm -rf "$work"
  fi
  export SOURCE_DIR="$RELEASE_DIR"
}
use_release

function flash_rapidly {
  for led in /sys/class/leds/*
  do 
    if [ -e "$led/trigger" ]
    then
      if ! grep -q timer "$led/trigger"
      then
        modprobe ledtrig-timer || echo "timer LED trigger unavailable"
      fi
      echo timer > "$led/trigger" || true
      if [ -e "$led/delay_off" ]
      then
        echo 150 > "$led/delay_off" || true
        echo 50 > "$led/delay_on" || true
      fi
    fi
  done
}

rootpart=$(findmnt -n -o SOURCE /)
rootname=$(lsblk -no pkname "${rootpart}")
rootdev="/dev/${rootname}"
marker="/root/RESIZE_ATTEMPTED"

# Check that the root partition is the last one.
lastpart=$(sfdisk -q -l "$rootdev" | tail +2 | sort -n -k 2 | tail -1 | awk '{print $1}')

# Check if there is sufficient unpartitioned space after the last
# partition to create the backingfiles and mutable partitions.
unpart=$(sfdisk -F "$rootdev" | grep -o '[0-9]* bytes' | head -1 | awk '{print $1}')
if [ "${1:-}" != "norootshrink" ] && [ "$unpart" -lt  $(( (1<<30) * 32)) ]
then
  # This script will only shrink the root partition, and if there's another
  # partition following the root partition, we won't be able to grow the
  # unpartitioned space at the end of the disk by shrinking the root partition.
  if [ "$rootpart" != "$lastpart" ]
  then
    error_exit "Insufficient unpartioned space, and root partition is not the last partition."
  fi

  used_bytes=$(df -B1 --output=used "$rootpart" | tail -1 | tr -d ' ')
  fetch_source setup/generic/rootfs-size.sh /tmp/teslausb-rootfs-size
  rootfs_size=$(bash /tmp/teslausb-rootfs-size "$used_bytes")
  rootfs_bytes=$(( ${rootfs_size%G} * (1 << 30) ))
  rootpart_bytes=$(blockdev --getsize64 "$rootpart")
  if ((rootpart_bytes <= rootfs_bytes))
  then
    error_exit "Root filesystem needs ${rootfs_size}, but its partition is only ${rootpart_bytes} bytes."
  fi
  if ((unpart + rootpart_bytes - rootfs_bytes < (1 << 30) * 32))
  then
    error_exit "Shrinking root to ${rootfs_size} would leave less than the required 32 GiB for TeslaUSB."
  fi

  # There is insufficient unpartitioned space.
  # Check if we've already shrunk the root filesystem, and shrink the root
  # partition to match if it hasn't been already

  devsectorsize=$(cat "/sys/block/${rootname}/queue/hw_sector_size")
  read -r fsblockcount fsblocksize < <(tune2fs -l "${rootpart}" | grep "Block count:\|Block size:" | awk ' {print $2}' FS=: | tr -d ' ' | tr '\n' ' ' | (cat; echo))
  fsnumsectors=$((fsblockcount * fsblocksize / devsectorsize))
  if ((fsnumsectors * devsectorsize > rootfs_bytes))
  then
    if [ -f "$marker" ]
    then
      error_exit "Previous resize attempt failed. Delete $marker before retrying."
    fi
    touch "$marker"

    echo "insufficient unpartitioned space, attempting to shrink root file system to ${rootfs_size}"

    if [ -n "${SOURCE_DIR:-}" ]
    then
      printf '#!/bin/bash\nexport SOURCE_DIR=%q\nbash %q\n' "$SOURCE_DIR" "$SOURCE_DIR/setup/generic/install.sh" > /etc/rc.local
    else
      cat <<- EOF > /etc/rc.local
			#!/bin/bash
			{
			  while ! curl -s "$RAW_BASE_URL"/setup/generic/install.sh
			  do
			    sleep 1
			  done
			} | bash
			EOF
    fi
    chmod a+x /etc/rc.local

    if [ ! -e "/boot/initrd.img-$(uname -r)" ]
    then
      # This device did not boot using an initramfs. If we're running
      # Raspberry Pi OS, we can switch it over to using initramfs first,
      # then revert back after.
      if [ -f /etc/os-release ] && grep -q Raspbian /etc/os-release && [ -e /teslausb/config.txt ]
      then
        echo "Temporarily switching Rasspberry Pi OS to use initramfs"
        update-initramfs -c -k "$(uname -r)"
        echo "initramfs initrd.img-$(uname -r) followkernel # TESLAUSB-REMOVE" >> /teslausb/config.txt
      else
        error_exit "can't automatically shrink root partition for this OS, please shrink it manually before proceeding"
      fi
    fi

    fetch_source tools/debian-resizefs.sh /tmp/teslausb-debian-resizefs
    bash /tmp/teslausb-debian-resizefs "$rootfs_size"
    exit 0
  fi
  rm -f "$marker"
  # shrink root partition to match root file system size
  echo "shrinking root partition to match root fs, $fsnumsectors sectors"
  sleep 3
  rootpartstartsector=$(sfdisk -q -l -o Start "${rootdev}" | tail +2 | sort -n | tail -1)
  partnum=${rootpart:0-1}

  echo "${rootpartstartsector},${fsnumsectors}" | sfdisk --force "${rootdev}" -N "${partnum}"

  if [ -e /teslausb/config.txt ] && grep -q TESLAUSB-REMOVE /teslausb/config.txt
  then
    # switch Raspberry Pi OS back to not using initramfs
    sed -i '/TESLAUSB-REMOVE/d' /teslausb/config.txt
    rm -rf "/boot/initrd.img-$(uname -r)"
  else
    # restore initramfs without the resize code that debian-resizefs.sh added
    update-initramfs -u
  fi

  reboot
  exit 0
fi

# Copy the sample config file from github
if [ ! -e /teslausb/teslausb_setup_variables.conf ] && [ ! -e /root/teslausb_setup_variables.conf ]
then
  fetch_source pi-gen-sources/00-teslausb-tweaks/files/teslausb_setup_variables.conf.sample /teslausb/teslausb_setup_variables.conf
fi

# and the wifi config template
if [ ! -e /teslausb/wpa_supplicant.conf.sample ]
then
  fetch_source pi-gen-sources/00-teslausb-tweaks/files/wpa_supplicant.conf.sample /teslausb/wpa_supplicant.conf.sample
fi

# The user should have configured networking manually, so disable wifi setup
touch /teslausb/WIFI_ENABLED

# Copy our rc.local from github, which will allow setup to
# continue using the regular "one step setup" process used
# for setting up a Raspberry Pi with the prebuilt image
rm -f /etc/rc.local
fetch_source pi-gen-sources/00-teslausb-tweaks/files/rc.local /etc/rc.local
chmod a+x /etc/rc.local

# Vendor desktop images run PackageKit, which takes the dpkg lock to refresh
# packages soon after boot. Make every apt call here and in the later setup
# wait for the lock instead of failing.
echo 'DPkg::Lock::Timeout "600";' > /etc/apt/apt.conf.d/90teslausb-lock-timeout

# A freshly flashed vendor image may have no package lists, so refresh them
# once before the first install. Fail loudly: the shebang's -e is lost when
# the script is run as "bash install.sh".
APT_UPDATED=false
function apt_install {
  if [ "$APT_UPDATED" = false ]
  then
    apt-get update || apt-get update --allow-releaseinfo-change || error_exit "apt-get update failed"
    APT_UPDATED=true
  fi
  DEBIAN_FRONTEND=noninteractive apt-get install -y "$@"
}

if [ ! -x "$(command -v dos2unix)" ]
then
  apt_install dos2unix || error_exit "Could not install dos2unix"
fi

if [ ! -x "$(command -v sntp)" ] && [ ! -x "$(command -v ntpdig)" ]
then
  apt_install sntp || apt_install ntpsec-ntpdig || error_exit "Could not install sntp or ntpsec-ntpdig"
fi

if [ ! -x "$(command -v parted)" ]
then
  apt_install parted || error_exit "Could not install parted"
fi

if [ ! -x "$(command -v fdisk)" ]
then
  apt_install fdisk || error_exit "Could not install fdisk"
fi

if [ ! -x "$(command -v sudo)" ]
then
  apt_install sudo || error_exit "Could not install sudo"
fi


# indicate we're waiting for the user to log in and finish setup
flash_rapidly

# If there is a user with id 1000, assume it is the default user
# the user will be logging in as.
DEFUSER=$(grep ":1000:1000:" /etc/passwd | awk -F : '{print $1}')
if [ -n "$DEFUSER" ]
then
  if [ ! -e "/home/$DEFUSER/.bashrc" ] || ! grep -q "SETUP_FINISHED" "/home/$DEFUSER/.bashrc"
  then
    cat <<- EOF >> "/home/$DEFUSER/.bashrc"
		if [ ! -e /teslausb/TESLAUSB_SETUP_FINISHED ]
		then
		  echo "+-------------------------------------------+"
		  echo "| To continue teslausb setup, run 'sudo -i' |"
		  echo "+-------------------------------------------+"
		fi
	EOF
    chown "$DEFUSER:$DEFUSER" "/home/$DEFUSER/.bashrc"
  fi
fi

if ! grep -q "SETUP_FINISHED" /root/.bashrc
then
  cat <<- EOF >> /root/.bashrc
	if [ ! -e /teslausb/TESLAUSB_SETUP_FINISHED ]
	then
	  echo "+------------------------------------------------------------------------+"
	  echo "| To continue teslausb setup, edit the file                              |"
	  echo "| /teslausb/teslausb_setup_variables.conf with your favorite             |"
	  echo "| editor, e.g. 'nano /teslausb/teslausb_setup_variables.conf' and fill   |"
	  echo "| in the required variables. Instructions are in the file, and at        |"
	  echo "| https://github.com/pgodlews/dashvault/blob/main/doc/OneStepSetup.md    |"
	  echo "| (though ignore the Raspberry Pi specific bits about flashing and       |"
	  echo "| mounting the sd card on a PC)                                          |"
	  echo "|                                                                        |"
	  echo "| When done, save changes and run /etc/rc.local                          |"
	  echo "+------------------------------------------------------------------------+"
	fi
	EOF
fi

# hack to print the above message without duplicating it here
grep -A 12 SETUP_FINISHED /root/.bashrc | grep echo | while read -r line; do eval "$line"; done
