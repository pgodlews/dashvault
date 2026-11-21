#!/usr/bin/env python3
"""Wi-Fi security test lab: plays one router configuration after another.

Runs in a container that owns a spare USB Wi-Fi adapter (see run-lab.sh), so the
host's own network and firewall are not involved. Boards under test run
pi-client.py, which tries every "dvlab-*" network it sees and reports back.

The order is: dvlab-hello (plain WPA2, finds the boards), one network per
security mode, then dvlab-report (plain WPA2, collects the full results).
"""
# ruff: noqa: S603, S607, S108

from __future__ import annotations

import http.server
import json
import os
import re
import subprocess
import sys
import threading
import time
from pathlib import Path

IFACE = os.environ["LAB_IF"]
PSK = os.environ["LAB_PSK"]
EXPECT = int(os.environ.get("LAB_EXPECT", "1"))
OUT = Path(os.environ.get("LAB_OUT", "/out"))
HELLO_TIMEOUT = int(os.environ.get("LAB_HELLO_TIMEOUT", "480"))
MODE_TIMEOUT = int(os.environ.get("LAB_MODE_TIMEOUT", "130"))
REPORT_TIMEOUT = int(os.environ.get("LAB_REPORT_TIMEOUT", "240"))
GATEWAY = "10.78.0.1"
# With LAB_EXPECT=0: offer only this mode, for up to LAB_HOLD seconds or until a
# client joins. Used to prove the adapter works with a known-good device.
ONLY = os.environ.get("LAB_ONLY", "")
HOLD = int(os.environ.get("LAB_HOLD", "3"))
SSID_OVERRIDE = os.environ.get("LAB_SSID", "")
COUNTRY = os.environ.get("LAB_COUNTRY", "")  # Two-letter regulatory domain, optional.

# name: (hostapd key_mgmt, ieee80211w, sae_pwe). What real routers commonly offer
# comes first; the last two are the strict settings.
MODES: dict[str, tuple[str, int, int | None]] = {
    "wpa2-psk": ("WPA-PSK", 0, None),
    "wpa2-psk-pmf-optional": ("WPA-PSK", 1, None),
    "wpa2-transition": ("WPA-PSK SAE", 1, 0),
    "wpa2-transition-h2e": ("WPA-PSK SAE", 1, 2),
    "psk-and-sha256": ("WPA-PSK WPA-PSK-SHA256", 0, None),
    "psk-and-sha256-pmf": ("WPA-PSK WPA-PSK-SHA256", 1, None),
    "psk-sha256-only": ("WPA-PSK-SHA256", 2, None),
    "wpa3-sae": ("SAE", 2, 0),
    "wpa3-sae-h2e": ("SAE", 2, 1),
}

reports: dict[str, dict[str, dict]] = {}
full_results: dict[str, list] = {}
lock = threading.Lock()


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        if not 0 < length < 200_000:
            self.send_error(400)
            return
        try:
            body = json.loads(self.rfile.read(length))
            mac = str(body["mac"])
            with lock:
                if self.path == "/report":
                    reports.setdefault(str(body["mode"]), {})[mac] = body
                elif self.path == "/final":
                    full_results[mac] = body
                else:
                    self.send_error(404)
                    return
        except (ValueError, KeyError, TypeError):
            self.send_error(400)
            return
        self.send_response(200)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"ok")


def run(*args: str) -> None:
    subprocess.run(args, check=True, capture_output=True)


def hostapd_conf(ssid: str, key_mgmt: str, pmf: int, sae_pwe: int | None) -> str:
    lines = [
        f"interface={IFACE}",
        "driver=nl80211",
        "ctrl_interface=/run/hostapd",
        f"ssid={ssid}",
        "hw_mode=g",
        "channel=1",
        "ieee80211n=1",
        "wmm_enabled=1",
        "auth_algs=1",
        "wpa=2",
        "rsn_pairwise=CCMP",
        f"wpa_key_mgmt={key_mgmt}",
        f"ieee80211w={pmf}",
    ]
    if COUNTRY:
        lines.append(f"country_code={COUNTRY}")
    if "SAE" in key_mgmt:
        lines.append(f"sae_password={PSK}")
        if sae_pwe is not None:
            lines.append(f"sae_pwe={sae_pwe}")
    if "WPA-PSK" in key_mgmt:
        lines.append(f"wpa_passphrase={PSK}")
    return "\n".join(lines) + "\n"


def stations() -> dict[str, dict[str, str]]:
    """What each connected client actually negotiated, as hostapd sees it."""
    result = subprocess.run(
        ["hostapd_cli", "-p", "/run/hostapd", "-i", IFACE, "all_sta"],
        capture_output=True,
        text=True,
        check=False,
    )
    found: dict[str, dict[str, str]] = {}
    current = None
    for line in result.stdout.splitlines():
        if re.fullmatch(r"([0-9a-f]{2}:){5}[0-9a-f]{2}", line.strip()):
            current = line.strip()
            found[current] = {}
        elif current and "=" in line:
            key, value = line.split("=", 1)
            found[current][key] = value
    return found


AKM_NAMES = {"00-0f-ac-2": "PSK", "00-0f-ac-6": "PSK-SHA256", "00-0f-ac-8": "SAE"}


class AccessPoint:
    def __init__(self, ssid: str, key_mgmt: str, pmf: int, sae_pwe: int | None):
        Path("/tmp/hostapd.conf").write_text(hostapd_conf(ssid, key_mgmt, pmf, sae_pwe))
        self.events: list[str] = []
        self.negotiated: dict[str, dict[str, str]] = {}
        self.enabled = threading.Event()
        self.process = subprocess.Popen(
            ["hostapd", "-t", "/tmp/hostapd.conf"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        for line in self.process.stdout:
            line = line.strip()
            if "AP-ENABLED" in line:
                self.enabled.set()
            if "AP-STA-" in line or "PSK-MISMATCH" in line or "could not" in line.lower():
                self.events.append(line.split(": ", 1)[-1])
            if "AP-STA-CONNECTED" in line:
                time.sleep(0.3)
                for mac, info in stations().items():
                    self.negotiated[mac] = {
                        "akm": AKM_NAMES.get(
                            info.get("AKMSuiteSelector", ""), info.get("AKMSuiteSelector", "?")
                        ),
                        "pmf": "yes" if "[MFP]" in info.get("flags", "") else "no",
                    }

    def stop(self):
        self.process.terminate()
        try:
            self.process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            self.process.kill()
        time.sleep(1)


def wait_for(condition, timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return True
        time.sleep(1)
    return condition()


def count(mode: str) -> int:
    with lock:
        return len(reports.get(mode, {}))


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    print("waiting for the adapter", flush=True)
    if not wait_for(lambda: Path(f"/sys/class/net/{IFACE}").exists(), 30):
        print("adapter never appeared")
        return 2
    run("ip", "link", "set", "lo", "up")
    run("ip", "link", "set", IFACE, "down")
    run("ip", "addr", "flush", "dev", IFACE)
    run("ip", "addr", "add", f"{GATEWAY}/24", "dev", IFACE)
    Path("/run/hostapd").mkdir(exist_ok=True)
    dnsmasq = subprocess.Popen(
        [
            "dnsmasq",
            "--no-daemon",
            f"--interface={IFACE}",
            "--bind-dynamic",
            "--port=0",
            "--dhcp-range=10.78.0.10,10.78.0.100,1h",
            "--dhcp-leasefile=/tmp/leases",
            "--dhcp-option=6,1.1.1.1",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    server = http.server.ThreadingHTTPServer((GATEWAY, 8088), Handler) if EXPECT else None
    if server:
        threading.Thread(target=server.serve_forever, daemon=True).start()

    observed: dict[str, dict] = {}
    configs_ok = True
    try:
        if EXPECT:
            ap = AccessPoint("dvlab-hello", "WPA-PSK", 0, None)
            if not ap.enabled.wait(25):
                print("hello network failed to start")
                return 2
            print(
                f"{time.strftime('%T')} hello network up, waiting for {EXPECT} board(s)", flush=True
            )
            wait_for(lambda: count("hello") >= EXPECT, HELLO_TIMEOUT)
            print(f"{time.strftime('%T')} boards seen: {count('hello')}", flush=True)
            ap.stop()

        for mode, (key_mgmt, pmf, sae_pwe) in MODES.items():
            if ONLY and mode != ONLY:
                continue
            ap = AccessPoint(SSID_OVERRIDE or f"dvlab-{mode}", key_mgmt, pmf, sae_pwe)
            if not ap.enabled.wait(25):
                print(f"{time.strftime('%T')} {mode}: ACCESS POINT FAILED TO START", flush=True)
                configs_ok = False
                ap.stop()
                continue
            if EXPECT:
                wait_for(lambda current=mode: count(current) >= EXPECT, MODE_TIMEOUT)
            else:
                if ONLY:
                    print(f"{time.strftime('%T')} {mode}: on air for {HOLD}s", flush=True)
                wait_for(lambda current=ap: bool(ONLY and current.negotiated), HOLD)
                time.sleep(1)
                for info in ap.negotiated.values():
                    print(
                        f"{time.strftime('%T')} client joined: akm={info['akm']} pmf={info['pmf']}",
                        flush=True,
                    )
            observed[mode] = {"negotiated": ap.negotiated, "events": ap.events[-12:]}
            print(f"{time.strftime('%T')} {mode}: started, {count(mode)} joined", flush=True)
            ap.stop()

        if EXPECT:
            ap = AccessPoint("dvlab-report", "WPA-PSK", 0, None)
            ap.enabled.wait(25)
            wait_for(lambda: len(full_results) >= count("hello") > 0, REPORT_TIMEOUT)
            ap.stop()
    finally:
        dnsmasq.terminate()

    with lock:
        output = {
            "modes": list(MODES),
            "joined": reports,
            "observed": observed,
            "clients": full_results,
        }
    (OUT / "results.json").write_text(json.dumps(output, indent=1))
    print(f"{time.strftime('%T')} results written, {len(full_results)} full report(s)", flush=True)
    return 0 if configs_ok else 1


if __name__ == "__main__":
    sys.exit(main())
