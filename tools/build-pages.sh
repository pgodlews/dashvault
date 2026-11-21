#!/bin/bash -eu
set -o pipefail
#
# build-pages.sh OUTPUT_DIR
#
# Builds the GitHub Pages site: a hosted copy of the setup page, because browsers
# only allow Web Bluetooth on HTTPS pages. The policy added here stops the hosted
# copy from making any network request, so details typed into it can only go to
# the Pi over Bluetooth.

if [ $# -ne 1 ]
then
  sed -n '4,9p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

OUT=$1
ROOT=$(cd "$(dirname "$0")/.." && pwd)
POLICY="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'"

mkdir -p "$OUT/setup"
awk -v policy="$POLICY" '
  { print }
  !added && /<meta charset="utf-8">/ {
    print "<meta http-equiv=\"Content-Security-Policy\" content=\"" policy "\">"
    print "<meta name=\"referrer\" content=\"no-referrer\">"
    added = 1
  }
' "$ROOT/run/provisioning/portal.html" > "$OUT/setup/index.html"

if ! grep -q 'http-equiv="Content-Security-Policy"' "$OUT/setup/index.html"
then
  echo "could not add the security policy to the setup page" >&2
  exit 1
fi

cat > "$OUT/index.html" <<'EOF'
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="0; url=setup/">
<title>Dashvault Setup</title>
</head>
<body><a href="setup/">Open Dashvault Bluetooth setup</a></body>
</html>
EOF
