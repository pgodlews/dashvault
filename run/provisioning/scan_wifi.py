#!/usr/bin/env python3
"""scan_wifi.py — print nearby WiFi APs as JSON using nmcli.

Outputs a JSON array to stdout:
  [{"ssid": "MyNet", "signal": 82, "security": "WPA2"}, ...]
sorted strongest-first.  Hidden networks (empty SSID) are omitted.

Used by both ble_service.py and portal_server.py.
"""

import json
import subprocess
import sys


def _split_terse(line: str, n: int) -> list[str]:
    """Split an nmcli -t colon-delimited line into at most *n* parts.

    nmcli escapes literal colons as \\: in terse output.
    """
    parts: list[str] = []
    cur: list[str] = []
    chars = list(line)
    i = 0
    while i < len(chars):
        if chars[i] == '\\' and i + 1 < len(chars) and chars[i + 1] == ':':
            cur.append(':')
            i += 2
        elif chars[i] == ':' and (n <= 0 or len(parts) < n - 1):
            parts.append(''.join(cur))
            cur = []
            i += 1
        else:
            cur.append(chars[i])
            i += 1
    parts.append(''.join(cur))
    return parts


def _norm_security(s: str) -> str:
    s = s.strip()
    if s in ('--', ''):
        return 'Open'
    if 'WPA3' in s:
        return 'WPA3'
    return 'WPA2'


def scan() -> list[dict]:
    try:
        out = subprocess.check_output(
            ['nmcli', '-t', '-f', 'SSID,SIGNAL,SECURITY',
             'dev', 'wifi', 'list', '--rescan', 'yes'],
            timeout=15,
            stderr=subprocess.DEVNULL,
        )
    except subprocess.TimeoutExpired:
        return []
    except subprocess.CalledProcessError:
        return []

    seen: dict[str, dict] = {}
    for line in out.decode(errors='replace').splitlines():
        if not line:
            continue
        parts = _split_terse(line, 3)
        if len(parts) < 3:
            continue
        ssid, sig_str, sec = parts[0], parts[1], parts[2]
        if not ssid:
            continue  # hidden network — not manageable via this UI
        try:
            sig = int(sig_str)
        except ValueError:
            sig = 0
        sec_norm = _norm_security(sec)
        if ssid not in seen or sig > seen[ssid]['signal']:
            seen[ssid] = {'ssid': ssid, 'signal': sig, 'security': sec_norm}

    return sorted(seen.values(), key=lambda x: -x['signal'])


if __name__ == '__main__':
    results = scan()
    json.dump(results, sys.stdout, ensure_ascii=False)
    sys.stdout.write('\n')
    sys.stdout.flush()
