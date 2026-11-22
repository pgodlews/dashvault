#!/bin/bash -eu
set -o pipefail
#
# sign-release.sh — attach the SD image to a draft release and sign it.
#
# Usage: tools/sign-release.sh TAG IMAGE_ZIP
#
# Run this on the machine that holds the offline signing key, after the tag's
# Release workflow has created the draft. It checks the draft's archives, adds
# the image to SHA256SUMS, signs that file (minisign asks for the passphrase)
# and uploads the image, SHA256SUMS and SHA256SUMS.minisig. It does not publish.
#
# Environment:
#   DASHVAULT_SIGNING_KEY   secret key (default: ~/.minisign/dashvault.key)
#   DASHVAULT_PUBLIC_KEY    public key to check the signature against
#                           (default: the one built into the backend)

if [ $# -ne 2 ]
then
  sed -n '4,16p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

TAG="$1"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
IMAGE_NAME="dashvault-$TAG-raspberry-pi.zip"
SIGNING_KEY="${DASHVAULT_SIGNING_KEY:-$HOME/.minisign/dashvault.key}"
PUBLIC_KEY="${DASHVAULT_PUBLIC_KEY:-$ROOT/teslausb-www/go-backend/release-signing.pub}"
COMMENT="dashvault-release $TAG"
# The script works in a temp folder, so name the repository explicitly.
export GH_REPO="${GH_REPO:-pgodlews/dashvault}"

function die () {
  echo "sign-release: $*" >&2
  exit 1
}

function checksum () {
  if command -v sha256sum > /dev/null
  then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

command -v minisign > /dev/null || die "minisign is not installed"
command -v gh > /dev/null || die "gh is not installed"
[ -f "$IMAGE" ] || die "no image at $IMAGE"
[ -f "$SIGNING_KEY" ] || die "no signing key at $SIGNING_KEY"
[ -f "$PUBLIC_KEY" ] || die "no public key at $PUBLIC_KEY"

IS_DRAFT="$(gh release view "$TAG" --json isDraft --jq .isDraft)" || die "no release $TAG"
[ "$IS_DRAFT" = true ] || die "$TAG is already published; its files must not change"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

echo "== Checking the draft's archives"
gh release download "$TAG" --dir . --clobber \
  --pattern teslausb-update.tar.gz --pattern teslausb-release.tar.gz --pattern SHA256SUMS

# Keep only the archives' lines, so that signing again replaces the image line.
grep -E '^[0-9a-f]{64}  teslausb-(update|release)\.tar\.gz$' SHA256SUMS > sums.new \
  || die "SHA256SUMS does not list the archives"
[ "$(wc -l < sums.new)" -eq 2 ] || die "SHA256SUMS does not list exactly the two archives"
mv sums.new SHA256SUMS
checksum -c SHA256SUMS > /dev/null || die "the draft's archives do not match SHA256SUMS"
for archive in teslausb-update.tar.gz teslausb-release.tar.gz
do
  version="$(tar -xzOf "$archive" ./VERSION)"
  [ "$version" = "$TAG" ] || die "$archive contains version $version, not $TAG"
done

echo "== Adding $IMAGE_NAME"
ln -s "$IMAGE" "$IMAGE_NAME"
image_hash="$(checksum "$IMAGE_NAME" | cut -d' ' -f1)"
printf '%s  %s\n' "$image_hash" "$IMAGE_NAME" >> SHA256SUMS

echo "== Signing SHA256SUMS"
minisign -S -s "$SIGNING_KEY" -m SHA256SUMS -t "$COMMENT"
verified="$(minisign -V -p "$PUBLIC_KEY" -m SHA256SUMS)" \
  || die "the signature does not match the public key"
case "$verified" in
  *"Trusted comment: $COMMENT"*) ;;
  *) die "the signature has the wrong comment" ;;
esac

echo "== Uploading"
gh release upload "$TAG" --clobber "$IMAGE_NAME" SHA256SUMS SHA256SUMS.minisig

mkdir uploaded
gh release download "$TAG" --dir uploaded --pattern SHA256SUMS --pattern SHA256SUMS.minisig
cmp SHA256SUMS uploaded/SHA256SUMS || die "uploaded SHA256SUMS differs"
cmp SHA256SUMS.minisig uploaded/SHA256SUMS.minisig || die "uploaded signature differs"

echo "Signed. Publish it with:"
echo "  gh release edit $TAG --draft=false --latest"
