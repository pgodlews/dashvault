#!/bin/bash -eu
# run/sdcard_info.sh — read microSD card identity from sysfs.
#
# Outputs a single JSON object:
#   {"name":"SD64G","manufacturer":"SanDisk","manfid":"0x000003",
#    "serial":"0xdeadbeef","date":"04/2023","capacity_gb":59.5}
#
# Falls back gracefully when fields are unavailable.

set -o pipefail

# Manufacturer ID → brand name mapping (most common OEMs)
# Source: https://www.cameramemoryspeed.com/sd-memory-card-faq/who-makes-sd-cards/
declare -A MFR_MAP=(
    ["0x000001"]="Panasonic"
    ["0x000002"]="Toshiba"
    ["0x000003"]="SanDisk"
    ["0x00001b"]="Samsung"
    ["0x00001d"]="AData"
    ["0x000027"]="Phison"
    ["0x000028"]="Lexar"
    ["0x000031"]="Silicon Power"
    ["0x000041"]="Kingston"
    ["0x000074"]="Transcend"
    ["0x000076"]="Patriot"
    ["0x000082"]="Sony"
    ["0x000090"]="SK Hynix"
    ["0x00009f"]="Kingston"
    ["0x000045"]="Team Group"
)

# The card is the disk holding the root filesystem: mmcblk0 on a Raspberry Pi,
# mmcblk1 on a Radxa Zero 3. Found from the root mount's device number.
boot_disk() {
    local majmin dev
    majmin=$(awk '$5 == "/" { m = $3 } END { print m }' /proc/self/mountinfo 2>/dev/null || true)
    if [ -n "$majmin" ] && dev=$(readlink -f "/sys/dev/block/$majmin" 2>/dev/null) && [ -d "$dev" ]; then
        if [ -f "$dev/partition" ]; then
            dev=$(dirname "$dev")
        fi
        basename "$dev"
        return
    fi
    for dev in /sys/block/mmcblk*; do
        if [ -e "$dev" ]; then
            basename "$dev"
            return
        fi
    done
    echo mmcblk0
}

DISK=$(boot_disk)
MMC_PATH=""
if [ -f "/sys/block/$DISK/device/name" ] || [ -f "/sys/block/$DISK/device/manfid" ]; then
    MMC_PATH="/sys/block/$DISK/device"
fi

read_field() {
    local file="$MMC_PATH/$1"
    if [ -f "$file" ]; then
        cat "$file" | tr -d '\n'
    else
        echo ""
    fi
}

if [ -z "$MMC_PATH" ]; then
    python3 -c "import json; print(json.dumps({'available': False, 'error': 'no MMC device found for the boot disk'}))"
    exit 0
fi

NAME=$(read_field "name")
MANFID=$(read_field "manfid")
OEMID=$(read_field "oemid")
SERIAL=$(read_field "serial")
DATE=$(read_field "date")
HWREV=$(read_field "hwrev")
FWREV=$(read_field "fwrev")

# Capacity from /sys/block/<disk>/size (512-byte sectors)
CAPACITY_BYTES=0
if [ -f "/sys/block/$DISK/size" ]; then
    SECTORS=$(cat "/sys/block/$DISK/size")
    CAPACITY_BYTES=$(( SECTORS * 512 ))
fi

python3 - <<EOF
import json

manfid = "$MANFID"
mfr_map = {
    "0x000001": "Panasonic",
    "0x000002": "Toshiba",
    "0x000003": "SanDisk",
    "0x00001b": "Samsung",
    "0x00001d": "AData",
    "0x000027": "Phison",
    "0x000028": "Lexar",
    "0x000031": "Silicon Power",
    "0x000041": "Kingston",
    "0x000074": "Transcend",
    "0x000076": "Patriot",
    "0x000082": "Sony",
    "0x000090": "SK Hynix",
    "0x00009f": "Kingston",
    "0x000045": "Team Group",
}
manufacturer = mfr_map.get(manfid, "Unknown")

capacity_bytes = $CAPACITY_BYTES
capacity_gb = round(capacity_bytes / 1e9, 1) if capacity_bytes > 0 else None

result = {
    "available": True,
    "name": "$NAME" or None,
    "manufacturer": manufacturer,
    "manfid": manfid or None,
    "oemid": "$OEMID" or None,
    "serial": "$SERIAL" or None,
    "date": "$DATE" or None,
    "hwrev": "$HWREV" or None,
    "fwrev": "$FWREV" or None,
    "capacity_gb": capacity_gb,
}
print(json.dumps({k: v for k, v in result.items() if v is not None or k == "available"}))
EOF
