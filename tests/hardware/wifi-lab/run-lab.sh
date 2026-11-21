#!/bin/bash -eu
set -o pipefail
#
# run-lab.sh ADAPTER OUTPUT_DIR [BOARDS]
#
# Runs the Wi-Fi security lab on a spare USB Wi-Fi adapter. The adapter is moved
# into a container for the duration, so the host's firewall and other network
# interfaces are not touched. It returns to the host when the lab ends.
# BOARDS=0 only checks that every router configuration starts.
# The lab password is read from LAB_PSK.

if [ $# -lt 2 ]
then
  sed -n '4,10p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

ADAPTER=$1
OUTPUT=$2
BOARDS=${3:-1}
: "${LAB_PSK:?set LAB_PSK}"
HERE=$(cd "$(dirname "$0")" && pwd)
NAME=dashvault-wifi-lab

if ip -4 route show default | grep -q "dev $ADAPTER "
then
  echo "STOP: $ADAPTER carries the default route; use a spare adapter." >&2
  exit 1
fi
PHY=phy$(iw dev "$ADAPTER" info | awk '/wiphy/ { print $2 }')

cleanup () {
  docker rm -f "$NAME" > /dev/null 2>&1 || true
  # The adapter goes back to the host when the container's namespace ends.
  for _ in 1 2 3 4 5 6 7 8 9 10
  do
    [ -e "/sys/class/net/$ADAPTER" ] && break
    sleep 1
  done
  sudo nmcli device set "$ADAPTER" managed yes > /dev/null 2>&1 || true
}
trap cleanup EXIT

docker build -q -t "$NAME" "$HERE" > /dev/null
mkdir -p "$OUTPUT"
docker rm -f "$NAME" > /dev/null 2>&1 || true
sudo nmcli device set "$ADAPTER" managed no
docker run -d --name "$NAME" --privileged \
  -e "LAB_IF=$ADAPTER" -e LAB_PSK -e "LAB_EXPECT=$BOARDS" \
  ${LAB_HELLO_TIMEOUT:+-e "LAB_HELLO_TIMEOUT=$LAB_HELLO_TIMEOUT"} \
  ${LAB_MODE_TIMEOUT:+-e "LAB_MODE_TIMEOUT=$LAB_MODE_TIMEOUT"} \
  ${LAB_ONLY:+-e "LAB_ONLY=$LAB_ONLY"} ${LAB_HOLD:+-e "LAB_HOLD=$LAB_HOLD"} \
  ${LAB_SSID:+-e "LAB_SSID=$LAB_SSID"} ${LAB_COUNTRY:+-e "LAB_COUNTRY=$LAB_COUNTRY"} \
  -v "$OUTPUT:/out" "$NAME" > /dev/null
sudo iw phy "$PHY" set netns "$(docker inspect -f '{{.State.Pid}}' "$NAME")"
docker logs -f "$NAME" 2>&1 || true
exit "$(docker wait "$NAME")"
