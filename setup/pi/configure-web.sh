#!/bin/bash -eu
set -o pipefail

setup_progress "configuring nginx"

# delete existing nginx fstab entries
sed -i "/.*\/nginx tmpfs.*/d" /etc/fstab
# and recreate them
echo "tmpfs /var/log/nginx tmpfs nodev,nosuid 0 0" >> /etc/fstab
echo "tmpfs /var/lib/nginx tmpfs nodev,nosuid 0 0" >> /etc/fstab
# only needed for initial setup, since systemd will create these automatically after that
mkdir -p /var/log/nginx
mkdir -p /var/lib/nginx
mount /var/log/nginx
mount /var/lib/nginx

packages=(nginx apache2-utils fuse libfuse-dev g++ net-tools wireless-tools ethtool)
if [ ! -f "$SOURCE_DIR/dist/teslausb-backend" ]
then
  packages+=(golang-go)
fi
apt-get -y --force-yes install "${packages[@]}"

# install data files and config files
# Stopping nginx drops the web-setup request. Always bring it back, including
# when a later command fails, so the UI is not left down until a power cycle.
nginx_was_stopped=false
restore_nginx() {
  if [ "$nginx_was_stopped" = true ]
  then
    systemctl start nginx.service >/dev/null 2>&1 || true
  fi
}
trap restore_nginx EXIT
systemctl stop nginx.service &> /dev/null || true
nginx_was_stopped=true
mkdir -p /var/www
umount /var/www/html/TeslaCam &> /dev/null || true
umount /var/www/html/fs/Music &> /dev/null || true
umount /var/www/html/fs/LightShow &> /dev/null || true
umount /var/www/html/fs/Boombox &> /dev/null || true
find /var/www/html -mount \( -type f -o -type l \) -print0 | xargs -0 rm
cp -r "$SOURCE_DIR/teslausb-www/html" /var/www/
# The backend's source is only needed when there is no prebuilt binary.
if [ -d "$SOURCE_DIR/teslausb-www/go-backend" ]
then
  cp -r "$SOURCE_DIR/teslausb-www/go-backend" /var/www/html/go-backend
else
  mkdir -p /var/www/html/go-backend
fi
ln -sf /teslausb/teslausb-headless-setup.log /var/www/html/
ln -sf /mutable/archiveloop.log /var/www/html/
ln -sf /tmp/diagnostics.txt /var/www/html/
mkdir -p /var/www/html/TeslaCam
cp -rf "$SOURCE_DIR/teslausb-www/teslausb.nginx" /etc/nginx/sites-available
# The login lockout needs nginx's auth_request module. Without it nginx would
# refuse to start, so fall back to password checking by nginx alone.
if ! nginx -V 2>&1 | grep -q -- '--with-http_auth_request_module'
then
  setup_progress "WARNING: nginx has no auth_request module; wrong web passwords will not be rate limited"
  sed -i -e '/# BEGIN auth gate/,/# END auth gate/d' -e 's/^\( *\)#fallback# /\1/' /etc/nginx/sites-available/teslausb.nginx
fi
ln -sf /etc/nginx/sites-available/teslausb.nginx /etc/nginx/sites-enabled/default

# Web UI auth configuration is now handled by configure-webui-auth.sh

# install the fuse layer needed to work around an incompatibility
# between Chrome and Tesla's recordings
g++ -o /root/cttseraser -D_FILE_OFFSET_BITS=64 "$SOURCE_DIR/teslausb-www/cttseraser.cpp" -lstdc++ -lfuse

# install new UI (compiled js/css files)
if [ -d "$SOURCE_DIR/teslausb-www/new/dist" ]
then
  mkdir -p /var/www/html/new
  cp -a "$SOURCE_DIR/teslausb-www/new/dist/." /var/www/html/new/
else
  # A source checkout has no compiled web app: take it from the latest release.
  setup_progress "Downloading the web app from the latest release"
  release_base="${RELEASE_BASE_URL:-https://github.com/${REPO:-pgodlews}/dashvault/releases/latest/download}"
  release_tmp=$(mktemp -d)
  curlwrapper -L -o "$release_tmp/teslausb-release.tar.gz" "$release_base/teslausb-release.tar.gz"
  curlwrapper -L -o "$release_tmp/SHA256SUMS" "$release_base/SHA256SUMS"
  (cd "$release_tmp" && grep ' teslausb-release.tar.gz$' SHA256SUMS | sha256sum -c > /dev/null 2>&1)
  tar -xzf "$release_tmp/teslausb-release.tar.gz" -C "$release_tmp" ./teslausb-www/new/dist
  mkdir -p /var/www/html/new
  cp -a "$release_tmp/teslausb-www/new/dist/." /var/www/html/new/
  rm -rf "$release_tmp"
fi
if [ -d /var/www/html/new ] && ! [ -e /var/www/html/new/favicon.ico ]
then
  ln -s /var/www/html/favicon.ico /var/www/html/new/favicon.ico
fi


cat > /sbin/mount.ctts << EOF
#!/bin/bash -eu
/root/cttseraser "\$@" -o allow_other
EOF
chmod +x /sbin/mount.ctts

sed -i '/mount.ctts/d' /etc/fstab
echo "mount.ctts#/mutable/TeslaCam /var/www/html/TeslaCam fuse defaults,nofail,x-systemd.requires=/mutable 0 0" >> /etc/fstab
mkdir -p /mutable/TeslaCam

sed -i 's/#user_allow_other/user_allow_other/' /etc/fuse.conf

# to get diagnostics and perform other teslausb functionality,
# nginx needs to be able to sudo
echo 'www-data ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/010_www-data-nopasswd
chmod 440 /etc/sudoers.d/010_www-data-nopasswd

# Install the unified Go backend (file ops + status API). A release carries
# static binaries per kernel architecture; a development tree may have a single
# dist/teslausb-backend; otherwise build it here.
prebuilt_backend=""
case "$(uname -m)" in
  aarch64|arm64) backend_arch=arm64 ;;
  armv7l|armhf)  backend_arch=armv7 ;;
  armv6l)        backend_arch=armv6 ;;
  *)             backend_arch="" ;;
esac
if [ -n "$backend_arch" ] && [ -f "$SOURCE_DIR/dist/teslausb-backend.$backend_arch" ]
then
  prebuilt_backend="$SOURCE_DIR/dist/teslausb-backend.$backend_arch"
elif [ -f "$SOURCE_DIR/dist/teslausb-backend" ]
then
  prebuilt_backend="$SOURCE_DIR/dist/teslausb-backend"
fi
if [ -n "$prebuilt_backend" ]
then
  setup_progress "Installing prebuilt Go backend ($(basename "$prebuilt_backend"))..."
  install -m 755 "$prebuilt_backend" /var/www/html/go-backend/teslausb-backend
else
  setup_progress "Building Go backend..."
  (
    cd /var/www/html/go-backend
    go build -o teslausb-backend
  )
fi
# Record what was installed, for the web app and for later runs of setup: a
# release has a VERSION file, a checkout of the repository does not. Setup only
# reinstalls from the release in the SD image while this still names it.
if [ -f "$SOURCE_DIR/VERSION" ]
then
  cp "$SOURCE_DIR/VERSION" /root/teslausb_version
else
  echo "source-$(date +%Y%m%d)" > /root/teslausb_version
fi
copy_script run/benchmark.sh /root/bin
copy_script run/sdcard_info.sh /root/bin
copy_script run/telemetry_index.py /root/bin
cp "$SOURCE_DIR/teslausb-www/teslausb-backend.service" /etc/systemd/system/
cp "$SOURCE_DIR/teslausb-www/teslausb-backend.socket" /etc/systemd/system/
systemctl daemon-reload
systemctl enable teslausb-backend.socket teslausb-backend.service
systemctl start teslausb-backend.socket

if [ -e /backingfiles/music_disk.bin ] || [ -e /backingfiles/lightshow_disk.bin ] || [ -e /backingfiles/boombox_disk.bin ]
then
  mkdir -p /var/www/html/fs
  copy_script run/auto.www /root/bin
  echo "/var/www/html/fs  /root/bin/auto.www" > /etc/auto.master.d/www.autofs
  apt-get -y --force-yes install zip
fi

# install teslausb-config backup/restore tool
cp "$SOURCE_DIR/run/teslausb-config" /usr/local/sbin/teslausb-config
chmod 755 /usr/local/sbin/teslausb-config

systemctl restart nginx.service
setup_progress "done configuring nginx"
