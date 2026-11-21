#!/bin/bash -e
touch "${ROOTFS_DIR}/boot/ssh"
install -m 755 files/rc.local                             "${ROOTFS_DIR}/etc/"
# A sample only: with no teslausb_setup_variables.conf, first boot starts the
# setup hotspot and Bluetooth service. Copy it to that name to configure by hand.
install -m 666 files/teslausb_setup_variables.conf.sample "${ROOTFS_DIR}/boot/firmware/teslausb_setup_variables.conf.sample"
install -m 666 files/wpa_supplicant.conf.sample           "${ROOTFS_DIR}/boot/firmware"
install -m 666 files/run_once                             "${ROOTFS_DIR}/boot/firmware"
install -d "${ROOTFS_DIR}/root/bin"

# Shown at http://dashvault.local until setup replaces it with the web app.
install -m 644 files/installing.html "${ROOTFS_DIR}/var/www/html/index.html"

# The release this image is built from (see prepare.sh). With it in the image,
# first boot installs exactly that release and fetches no scripts from GitHub.
if [ -d files/release ]
then
  install -d "${ROOTFS_DIR}/usr/local/share/teslausb-release"
  cp -R files/release/. "${ROOTFS_DIR}/usr/local/share/teslausb-release/"
  chown -R 0:0 "${ROOTFS_DIR}/usr/local/share/teslausb-release"

  # First-run provisioning. rc.local starts the service only when there is no
  # configuration file, so it is installed but not enabled.
  install -d "${ROOTFS_DIR}/root/provisioning"
  for script in start.sh scan_wifi.py portal_server.py ble_service.py
  do
    install -m 755 "files/release/run/provisioning/$script" "${ROOTFS_DIR}/root/provisioning/"
  done
  install -m 644 files/release/run/provisioning/portal.html "${ROOTFS_DIR}/root/provisioning/"
  install -m 644 files/release/run/provisioning/provisioning_config.py "${ROOTFS_DIR}/root/provisioning/"
  install -m 644 files/release/setup/pi/teslausb-provisioning.service "${ROOTFS_DIR}/etc/systemd/system/"
  install -d "${ROOTFS_DIR}/etc/NetworkManager/dnsmasq-shared.d"
  echo 'address=/#/192.168.4.1' > "${ROOTFS_DIR}/etc/NetworkManager/dnsmasq-shared.d/teslausb-captive.conf"
fi

# ensure dwc2 module is loaded
echo "dtoverlay=dwc2" >> "${ROOTFS_DIR}/boot/firmware/config.txt"

# Setup creates the backingfiles and mutable partitions in the space after the
# root partition, so the root partition must not grow to fill the card on
# first boot. Trixie does that with the "resize" kernel argument and the
# rpi-resize service; Bookworm used resize2fs_once and an initramfs script.
sed -i -e 's/ resize\( \|$\)/\1/' "${ROOTFS_DIR}/boot/firmware/cmdline.txt"

# Trixie's rpi-swap defaults to compressed RAM swap backed by a file on the
# card. Keep the RAM part, which helps a 512 MB board, and drop the file: the
# root filesystem ends up read-only and has no room to spare for it.
if [ -e "${ROOTFS_DIR}/etc/rpi/swap.conf" ]
then
  install -d "${ROOTFS_DIR}/etc/rpi/swap.conf.d"
  printf '[Main]\nMechanism=zram\n' > "${ROOTFS_DIR}/etc/rpi/swap.conf.d/90-teslausb.conf"
fi

# remove unwanted packages, disable unwanted services, and disable swap
on_chroot << EOF
for package in triggerhappy userconf-pi dphys-swapfile firmware-libertas firmware-realtek firmware-atheros mkvtoolnix
do
  if dpkg -s "\$package" > /dev/null 2>&1
  then
    apt-get remove -y --purge "\$package"
  fi
done
apt-get -y autoremove
for unit in keyboard-setup resize2fs_once rpi-resize dpkg-db-backup
do
  systemctl disable "\$unit" || true
done
update-rc.d resize2fs_once remove || true
rm -f /etc/init.d/resize2fs_once
rm -f /usr/share/initramfs-tools/scripts/local-premount/firstboot
update-initramfs -u
EOF
