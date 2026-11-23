#!/bin/bash -eu
set -o pipefail
#
# build-release.sh — build everything a release ships.
#
# Usage: tools/build-release.sh VERSION [OUTPUT_DIR]
#
# Writes to OUTPUT_DIR (default: dist/release):
#   teslausb-update.tar.gz   the app update that installed boards take from
#                            Settings (layout read by run/ota_install.sh)
#   teslausb-release.tar.gz  everything setup needs, prebuilt: the SD image
#                            carries it, so first boot installs exactly this
#                            release without fetching scripts from GitHub
#   SHA256SUMS
#
# Environment:
#   SKIP_PNPM_INSTALL=1     use the node_modules already in teslausb-www/new
#   PREBUILT_BACKENDS=DIR   take teslausb-backend.{arm64,armv7,armv6} from DIR
#   PREBUILT_UI=DIR         take the built web app from DIR
#
# Only files tracked by git go into the archives, so nothing local (a .env,
# editor leftovers) can leak into a release.

if [ $# -lt 1 ] || [ -z "$1" ]
then
  sed -n '4,21p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

VERSION="$1"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${2:-$ROOT/dist/release}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$ROOT"

# macOS tar would add AppleDouble files; both tars need different flags to
# record root as the owner.
export COPYFILE_DISABLE=1
if tar --version 2> /dev/null | grep -q GNU
then
  TAR_OWNER=(--owner=0 --group=0 --numeric-owner)
else
  TAR_OWNER=(--uid 0 --gid 0)
fi

function checksum () {
  if command -v sha256sum > /dev/null
  then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

# Copy tracked files under the given paths into a directory, keeping the layout.
function copy_tracked () {
  local destination="$1"
  shift
  local file
  while IFS= read -r -d '' file
  do
    mkdir -p "$destination/$(dirname "$file")"
    cp -p "$file" "$destination/$file"
  done < <(git ls-files -z -- "$@")
}

echo "== Go backend $VERSION"
BACKENDS="$WORK/backends"
mkdir -p "$BACKENDS"

# Static binaries: which one a board runs depends on its kernel, not its userland.
function build_backend () {
  local name="$1" goarch="$2" goarm="${3:-}"
  (
    cd teslausb-www/go-backend
    GOOS=linux GOARCH="$goarch" GOARM="$goarm" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$BACKENDS/$name" .
  )
  echo "   $name"
}
if [ -n "${PREBUILT_BACKENDS:-}" ]
then
  cp "$PREBUILT_BACKENDS"/teslausb-backend.arm64 "$PREBUILT_BACKENDS"/teslausb-backend.armv7 \
    "$PREBUILT_BACKENDS"/teslausb-backend.armv6 "$BACKENDS/"
else
  build_backend teslausb-backend.arm64 arm64
  build_backend teslausb-backend.armv7 arm 7
  build_backend teslausb-backend.armv6 arm 6
fi

echo "== Web app"
if [ -n "${PREBUILT_UI:-}" ]
then
  UI_DIST="$(cd "$PREBUILT_UI" && pwd)"
else
  (
    cd teslausb-www/new
    if [ "${SKIP_PNPM_INSTALL:-0}" != 1 ]
    then
      pnpm install --frozen-lockfile
    fi
    pnpm build
  )
  UI_DIST="$ROOT/teslausb-www/new/dist"
fi
[ -f "$UI_DIST/index.html" ] || { echo "web app build produced no index.html" >&2; exit 1; }

echo "== teslausb-update.tar.gz"
STAGING="$WORK/update"
mkdir -p "$STAGING/bin/lib" "$STAGING/www" "$STAGING/units" "$STAGING/setup"
echo "$VERSION" > "$STAGING/VERSION"

# Scripts installed to /root/bin. A script that setup installs but is missing
# here stays at its old version on boards that update from Settings.
for script in \
  reconfigure-archive.sh archiveloop send-push-message waitforidle remountfs_rw \
  os-update.sh temperature_monitor make_snapshot.sh manage_free_space.sh \
  enable_gadget.sh disable_gadget.sh release_snapshot.sh ota_finalize.sh \
  ota_install.sh ota_rollback.sh ota_transaction.sh benchmark.sh sdcard_info.sh \
  telemetry_index.py teslausb_telegram_bot.py tesla_api.py check-deps.sh
do
  install -m 755 "run/$script" "$STAGING/bin/"
done
install -m 644 setup/pi/teslausb-telegram-bot.service "$STAGING/bin/"
install -m 644 setup/pi/envsetup.sh "$STAGING/bin/"
install -m 755 run/lib/paths.sh "$STAGING/bin/lib/"
install -m 755 run/lib/startup_drives.sh "$STAGING/bin/lib/"
install -m 644 run/lib/dependencies.txt "$STAGING/bin/lib/"
for script in archive-clips.sh connect-archive.sh disconnect-archive.sh archive-is-reachable.sh
do
  install -m 755 "run/lib/backends/wrapper/$script" "$STAGING/bin/"
done
for backend in cifs nfs rsync rclone none
do
  mkdir -p "$STAGING/bin/lib/backends/$backend"
  for script in archive-clips.sh connect-archive.sh disconnect-archive.sh archive-is-reachable.sh copy-music.sh
  do
    if [ -f "run/${backend}_archive/$script" ]
    then
      install -m 755 "run/${backend}_archive/$script" "$STAGING/bin/lib/backends/$backend/"
    fi
  done
done

# Provisioning service scripts, and what configure-provisioning.sh needs.
mkdir -p "$STAGING/bin/provisioning"
for script in start.sh scan_wifi.py portal_server.py ble_service.py provisioning_config.py
do
  install -m 755 "run/provisioning/$script" "$STAGING/bin/provisioning/"
done
install -m 644 run/provisioning/portal.html "$STAGING/bin/provisioning/"
install -m 755 setup/pi/configure-provisioning.sh "$STAGING/setup/"
install -m 644 setup/pi/teslausb-provisioning.service "$STAGING/setup/"

# Service units must accompany script fixes on installed systems.
install -m 644 setup/pi/teslausb-telegram-bot.service "$STAGING/units/"

install -m 755 "$BACKENDS"/teslausb-backend.* "$STAGING/bin/"
cp -R "$UI_DIST/." "$STAGING/www/"
tar -czf "$OUT/teslausb-update.tar.gz" "${TAR_OWNER[@]}" -C "$STAGING" .

echo "== teslausb-release.tar.gz"
BUNDLE="$WORK/release"
mkdir -p "$BUNDLE/dist" "$BUNDLE/teslausb-www/new"
echo "$VERSION" > "$BUNDLE/VERSION"
copy_tracked "$BUNDLE" \
  LICENSE \
  setup \
  run \
  tools/debian-resizefs.sh \
  pi-gen-sources/00-teslausb-tweaks/files \
  teslausb-www/teslausb.nginx \
  teslausb-www/teslausb-backend.service \
  teslausb-www/teslausb-backend.socket \
  teslausb-www/cttseraser.cpp \
  teslausb-www/html
install -m 755 "$BACKENDS"/teslausb-backend.* "$BUNDLE/dist/"
mkdir -p "$BUNDLE/teslausb-www/new/dist"
cp -R "$UI_DIST/." "$BUNDLE/teslausb-www/new/dist/"
tar -czf "$OUT/teslausb-release.tar.gz" "${TAR_OWNER[@]}" -C "$BUNDLE" .

(
  cd "$OUT"
  checksum teslausb-update.tar.gz teslausb-release.tar.gz > SHA256SUMS
  echo "== $OUT"
  ls -lh teslausb-update.tar.gz teslausb-release.tar.gz | awk '{ print "   " $5, $NF }'
  cat SHA256SUMS
)
