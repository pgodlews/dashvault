#!/bin/bash -eu
set -o pipefail
#
# Dashvault runs headless. Vendor desktop images (Radxa's KDE image, Armbian
# desktop builds, ...) start a display manager and desktop services at every
# boot. They use memory and CPU, PackageKit takes the dpkg lock during setup,
# and power-profiles-daemon would fight Dashvault's CPU governor setting.
#
# Switch the board to a text boot and turn those services off. Packages stay
# installed. Only units that exist are touched, and running ones are left
# running (stopping the display manager would end the desktop session the
# installer may have been started from), so the change takes effect at the
# next reboot. Safe to run more than once.
#
# To get the desktop back:
#   systemctl unmask <display manager> && systemctl enable <display manager>
#   systemctl set-default graphical.target

# Display managers and services that only matter with a desktop.
MASK_UNITS=(
  gdm3.service gdm.service sddm.service lightdm.service lxdm.service
  slim.service xdm.service nodm.service
  packagekit.service
)
DISABLE_UNITS=(
  cups.service cups.socket cups.path cups-browsed.service
  accounts-daemon.service rtkit-daemon.service upower.service udisks2.service
  colord.service switcheroo-control.service power-profiles-daemon.service
  fwupd-refresh.timer man-db.timer local-apt-repository.path
  # Rockchip vendor images: Chromium's X helper, the camera ISP and the NPU
  rockchip-chromium-x11-utils.service rkaiq_3A.service rknpu2.service
)

function unit_exists {
  [ -n "$(systemctl list-unit-files --no-legend "$1" 2> /dev/null)" ]
}

if [ "$(systemctl get-default 2> /dev/null)" = graphical.target ]
then
  echo "Switching to text boot (multi-user.target)"
  systemctl set-default multi-user.target &> /dev/null
fi

for unit in "${MASK_UNITS[@]}"
do
  if unit_exists "$unit" && [ "$(systemctl is-enabled "$unit" 2> /dev/null)" != masked ]
  then
    echo "Masking $unit"
    systemctl disable "$unit" &> /dev/null || true
    systemctl mask "$unit" &> /dev/null
  fi
done

for unit in "${DISABLE_UNITS[@]}"
do
  if unit_exists "$unit" && systemctl is-enabled "$unit" &> /dev/null
  then
    echo "Disabling $unit"
    systemctl disable "$unit" &> /dev/null || true
  fi
done

# Disabling the display manager's unit normally removes this alias. Remove it
# anyway so a leftover link cannot start a masked unit's replacement.
if [ -L /etc/systemd/system/display-manager.service ]
then
  rm -f /etc/systemd/system/display-manager.service
fi
