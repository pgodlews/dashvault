#!/bin/bash -eu
# Install a static ffmpeg for the Telegram "near here" frames (experimental).
#
# Downloads John Van Sickle's static build for this CPU, checks it against the
# SHA-256 pinned below, and keeps only the ffmpeg binary (and its GPL text) in
# /backingfiles/tools, which is writable while the root filesystem stays
# read-only. Prints the binary's path. --check only reports whether it is there.
#
# The release URL always serves the newest build, so when it moves on the hash
# no longer matches and this refuses to install: update VERSION and the hashes.
set -o pipefail

VERSION=7.0.2
TOOLS_DIR="${TOOLS_DIR:-/backingfiles/tools}"
LINK="$TOOLS_DIR/ffmpeg"
BASE_URL="${FFMPEG_BASE_URL:-https://johnvansickle.com/ffmpeg/releases}"

declare -A SHA256=(
  [arm64]=f4149bb2b0784e30e99bdda85471c9b5930d3402014e934a5098b41d0f7201b1
  [armhf]=7d41f558cb1f3395b313f8ceabed78b3731c79a0962abf405ebb5cd393e93991
  [armel]=d221ee22b0c1ad98abffd449e5394217709caf97c7a251cde2de6a9759061186
)

function build_arch () {
  case "${1:-$(uname -m)}" in
    aarch64|arm64) echo arm64 ;;
    armv7l|armv8l) echo armhf ;;
    armv6l) echo armel ;;  # Pi Zero W / Pi 1: ARMv6
    *) return 1 ;;
  esac
}

if [ "${1:-}" = "--check" ]
then
  if [ -x "$LINK/ffmpeg" ]
  then
    echo "$LINK/ffmpeg"
    exit 0
  fi
  echo "not installed"
  exit 1
fi

if ! arch=$(build_arch "${FFMPEG_ARCH:-}")
then
  echo "no static ffmpeg build for $(uname -m)" >&2
  exit 1
fi
target="$TOOLS_DIR/ffmpeg-$VERSION-$arch"
if [ -x "$target/ffmpeg" ] && [ -L "$LINK" ] && [ "$(readlink "$LINK")" = "$target" ]
then
  echo "$LINK/ffmpeg"
  exit 0
fi

mkdir -p "$TOOLS_DIR"
work=$(mktemp -d "$TOOLS_DIR/.ffmpeg-download.XXXXXX")
trap 'rm -rf "$work"' EXIT

archive="$work/ffmpeg.tar.xz"
curl -fsSL --retry 2 --max-time 600 -o "$archive" "$BASE_URL/ffmpeg-release-$arch-static.tar.xz"
actual=$(sha256sum "$archive" | cut -d' ' -f1)
if [ "$actual" != "${SHA256[$arch]}" ]
then
  echo "checksum mismatch for the $arch build (got $actual)." >&2
  echo "The release has probably moved past $VERSION; update VERSION and the hashes in $0." >&2
  exit 1
fi

tar -xJf "$archive" -C "$work" --strip-components=1 \
  "ffmpeg-$VERSION-$arch-static/ffmpeg" "ffmpeg-$VERSION-$arch-static/GPLv3.txt"
"$work/ffmpeg" -hide_banner -version > /dev/null  # runs on this CPU
rm -rf "$target"
mkdir -p "$target"
mv "$work/ffmpeg" "$work/GPLv3.txt" "$target/"
chmod 755 "$target/ffmpeg"
ln -sfn "$target" "$LINK.new"
mv -T "$LINK.new" "$LINK"
echo "$LINK/ffmpeg"
