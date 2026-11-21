#!/usr/bin/env bash
# Smoke test for the local integration harness.
# Brings up samba + fake-pi, runs a trivial archive into the samba share,
# verifies the file arrives, and tears everything down.
set -euo pipefail

cd "$(dirname "$0")"

cleanup() {
    docker compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> Building harness..."
docker compose build --quiet

echo "==> Starting samba + fake-pi..."
docker compose up -d

echo "==> Waiting for samba..."
for _ in $(seq 1 30); do
    if docker compose exec -T fake-pi smbclient -L //samba -U tesla%tesla -N >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

echo "==> Writing test file to samba share from fake-pi..."
docker compose exec -T fake-pi bash -c '
    set -euo pipefail
    echo "hello-teslausb-$(date -u +%s)" > /tmp/probe.txt
    smbclient //samba/dashcam -U tesla%tesla -c "put /tmp/probe.txt probe.txt"
'

echo "==> Verifying file arrived..."
docker compose exec -T fake-pi bash -c '
    smbclient //samba/dashcam -U tesla%tesla -c "get probe.txt /tmp/probe-readback.txt"
    grep -q "^hello-teslausb-" /tmp/probe-readback.txt
'

echo "==> Shell scripts on fake-pi can be invoked:"
docker compose exec -T fake-pi bash -c '
    test -x /opt/teslausb/run/archiveloop
    test -x /opt/teslausb/run/make_snapshot.sh
    bash -n /opt/teslausb/run/archiveloop
'

echo "==> Smoke test PASSED"
