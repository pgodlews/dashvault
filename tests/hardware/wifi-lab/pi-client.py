#!/usr/bin/env python3
"""Board side of the Wi-Fi lab. Copy to a test card's boot partition as run_once.

Before installing, replace @LAB_PSK@ with the lab's throwaway password. It runs
once at first boot, before provisioning: it tries every "dvlab-*" network with
the same nmcli command first boot uses, reports what happened, and then exits so
that provisioning starts normally. Results are also kept on the boot partition
in dvlab-results.jsonl.
"""
# ruff: noqa: S310, S603

from __future__ import annotations

import json
import pathlib
import re
import subprocess
import time
import urllib.request

PSK = "@LAB_PSK@"
GATEWAY = "10.78.0.1"
RESULTS = pathlib.Path("/teslausb/dvlab-results.jsonl")
DEADLINE = time.monotonic() + 45 * 60


def sh(*args: str, timeout: int = 60) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    except subprocess.TimeoutExpired:
        return subprocess.CompletedProcess(args, 124, "", "timed out")


def read(path: str) -> str:
    try:
        return pathlib.Path(path).read_text(errors="replace").replace("\0", "").strip()
    except OSError:
        return ""


def identity() -> dict[str, str]:
    dmesg = sh("dmesg").stdout
    firmware = re.search(r"Firmware: (.+)", dmesg)
    return {
        "mac": read("/sys/class/net/wlan0/address"),
        "model": read("/proc/device-tree/model"),
        "kernel": sh("uname", "-rm").stdout.strip(),
        "wifi_firmware": firmware.group(1).strip()[:90] if firmware else "",
        "supplicant": sh("dpkg-query", "-W", "-f=${Version}", "wpasupplicant").stdout,
        "network_manager": sh("dpkg-query", "-W", "-f=${Version}", "network-manager").stdout,
    }


def forget(ssid: str) -> None:
    for line in sh("nmcli", "-g", "UUID,TYPE", "connection", "show").stdout.splitlines():
        if line.endswith(":802-11-wireless"):
            uuid = line.split(":", 1)[0]
            name = sh(
                "nmcli", "--escape", "no", "-g", "802-11-wireless.ssid", "connection", "show", uuid
            )
            if name.stdout.strip() == ssid:
                sh("nmcli", "connection", "delete", "uuid", uuid)


def visible() -> set[str]:
    sh("nmcli", "device", "wifi", "rescan")
    time.sleep(4)
    out = sh("nmcli", "-t", "--escape", "no", "-f", "SSID", "device", "wifi", "list").stdout
    return {line.strip() for line in out.splitlines() if line.startswith("dvlab-")}


def post(path: str, body: dict) -> bool:
    request = urllib.request.Request(
        f"http://{GATEWAY}:8088{path}",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    for _ in range(4):
        try:
            with urllib.request.urlopen(request, timeout=8) as response:
                return response.status == 200
        except OSError:
            time.sleep(2)
    return False


def attempt(ssid: str, me: dict[str, str]) -> dict:
    mode = ssid.removeprefix("dvlab-")
    since = time.strftime("%Y-%m-%d %H:%M:%S")
    forget(ssid)
    started = time.monotonic()
    result = sh(
        "nmcli", "--wait", "35", "device", "wifi", "connect", ssid, "password", PSK, timeout=50
    )
    entry: dict = {
        "mac": me["mac"],
        "mode": mode,
        "ok": result.returncode == 0,
        "seconds": round(time.monotonic() - started, 1),
    }
    log = sh("journalctl", "-u", "NetworkManager", "--since", since, "--no-pager").stdout
    key_mgmt = re.findall(r"added 'key_mgmt' value '([^']+)'", log)
    entry["offered_key_mgmt"] = key_mgmt[-1] if key_mgmt else ""
    if result.returncode == 0:
        reachable = sh("ping", "-c", "3", "-W", "2", GATEWAY).returncode == 0
        entry["reached_gateway"] = reachable
        if mode != "report":
            entry["reported"] = post("/report", entry)
    else:
        lines = (result.stderr + result.stdout).replace(PSK, "<hidden>").strip().splitlines()
        entry["error"] = lines[-1][:160] if lines else ""
    return entry


def main() -> None:
    sh("rfkill", "unblock", "wifi")
    sh("nmcli", "radio", "wifi", "on")
    for _ in range(40):
        if re.search(
            r"^wlan0:(disconnected|connected)",
            sh("nmcli", "-t", "-f", "DEVICE,STATE", "device").stdout,
            re.M,
        ):
            break
        time.sleep(1)
    me = identity()
    results: list[dict] = []
    tried: set[str] = set()
    give_up_without_lab = time.monotonic() + 15 * 60
    while time.monotonic() < DEADLINE:
        if not tried and time.monotonic() > give_up_without_lab:
            return  # No lab running: let provisioning start normally.
        for ssid in sorted(visible() - tried):
            if ssid == "dvlab-report":
                entry = attempt(ssid, me)
                if entry["ok"] and post("/final", {**me, "results": results}):
                    sh("nmcli", "device", "disconnect", "wlan0")
                    forget(ssid)
                    return
                forget(ssid)
                continue
            tried.add(ssid)
            entry = attempt(ssid, me)
            results.append(entry)
            with RESULTS.open("a") as output:
                output.write(json.dumps({**entry, "model": me["model"]}) + "\n")
            sh("nmcli", "device", "disconnect", "wlan0")
            forget(ssid)
        time.sleep(2)


if __name__ == "__main__":
    main()
