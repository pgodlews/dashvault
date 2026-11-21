#!/bin/bash -eu

SRC=$(dirname $(readlink -f $0))
DEST=$(readlink -f .)

if [[ "$DEST" != */pi-gen ]]
then
  echo "$0 should be called from the RPi-Distro pi-gen folder"
  exit 1
fi

cp "$SRC/pi-gen-config" config
rm -rf stage2/EXPORT_NOOBS stage2/EXPORT_IMAGE export-image/01-user-rename/00-packages

# ENABLE_CLOUD_INIT=0 only skips writing cloud-init's seed files; the packages
# in this stage are installed anyway. First boot is driven by rc.local, and
# cloud-init would add boot time and its own ideas about resizing the root
# partition, so leave it out of the image.
rm -rf stage2/04-cloud-init

# pi-gen sizes the root partition to its contents plus 20% and 200 MB, which
# leaves about 230 MB free: not enough for the packages setup installs, let
# alone later operating system updates. The root partition is not grown on
# first boot (setup needs the rest of the card), so the headroom has to be in
# the image. It is empty space and compresses to almost nothing.
ROOT_HEADROOM_MB=2560
if grep -q "ROOT_SIZE \* 0.2 + ${ROOT_HEADROOM_MB} \* 1024 \* 1024" export-image/prerun.sh
then
  : # already patched by an earlier run
elif grep -q 'ROOT_SIZE \* 0.2 + 200 \* 1024 \* 1024' export-image/prerun.sh
then
  sed -i "s/ROOT_SIZE \* 0.2 + 200 \* 1024 \* 1024/ROOT_SIZE * 0.2 + ${ROOT_HEADROOM_MB} * 1024 * 1024/" export-image/prerun.sh
else
  echo "export-image/prerun.sh no longer has the root margin this script patches"
  exit 1
fi
mkdir -p stage_teslausb
touch stage_teslausb/EXPORT_IMAGE
cp stage2/prerun.sh stage_teslausb/prerun.sh
cp -r "$SRC/00-teslausb-tweaks" stage_teslausb

# Optional argument: teslausb-release.tar.gz from tools/build-release.sh. The
# image then carries that release. Without it the image is "thin" and first
# boot downloads the scripts from GitHub.
RELEASE_BUNDLE="${1:-}"
rm -rf stage_teslausb/00-teslausb-tweaks/files/release
if [ -n "$RELEASE_BUNDLE" ]
then
  mkdir -p stage_teslausb/00-teslausb-tweaks/files/release
  tar -xzf "$RELEASE_BUNDLE" -C stage_teslausb/00-teslausb-tweaks/files/release
  echo "Image will carry release $(cat stage_teslausb/00-teslausb-tweaks/files/release/VERSION)"
else
  echo "No release bundle given: building a thin image that downloads its scripts at first boot"
fi

echo 'Build config set. Now use "./build.sh" or "./build-docker.sh" to build the TeslaUSB image.'


