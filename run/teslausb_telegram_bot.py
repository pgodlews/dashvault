#!/usr/bin/env python3
"""
TeslaUSB Telegram bot — long-polling inbound command handler.

Config (from /mutable/teslausb_telegram.env, with fallback to /root/teslausb_setup_variables.conf):
  TELEGRAM_BOT_TOKEN          — bot token from @BotFather
  TELEGRAM_CHAT_ID            — comma-separated allowed chat IDs (ints)
  TELEGRAM_SILENT_NOTIFY      — "true"/"false", default false

Commands:
  /status     — live status from backend API (archive state, disk, temp, wifi, uptime)
  /freespace  — free space on cam disk and archive mount
  /sdcard     — SD card health: card info, write stats, benchmark history, TBW threshold
  /sync       — trigger an archive run now
  /lastclip   — send the most recent SavedClip or SentryClip
  /recent <n> — send the n most recent clips (max 5)
  /restart    — restart the teslausb service
  /near       — drives near a point (experimental, TELEGRAM_NEAR_ENABLED=true):
                share a location pin, or /near <lat>,<lon> [radius m]
  /help       — list commands

Run as a systemd service (see setup/pi/teslausb-telegram-bot.service).
"""

from __future__ import annotations

import base64
import json
import json as json_module  # _api takes a `json=` argument that shadows the module
import logging
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import BinaryIO

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
    stream=sys.stdout,
)
log = logging.getLogger("teslausb-bot")

# ── Config ────────────────────────────────────────────────────────────────────


def _load_conf_defaults(conf_path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    try:
        for raw_line in conf_path.read_text(encoding="utf-8", errors="replace").splitlines():
            line = raw_line.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("export "):
                line = line[len("export ") :].strip()
            if "=" not in line:
                continue
            k, v = line.split("=", 1)
            k = k.strip()
            v = v.strip()
            if len(v) >= 2 and v[0] in ("'", '"') and v[-1] == v[0]:
                v = v[1:-1].replace("'\\''", "'")
            values[k] = v
    except OSError:
        pass
    return values


_CONF_DEFAULTS = _load_conf_defaults(
    Path(os.environ.get("TESLAUSB_CONF_FILE", "/root/teslausb_setup_variables.conf"))
)

TOKEN = os.environ.get("TELEGRAM_BOT_TOKEN") or _CONF_DEFAULTS.get("TELEGRAM_BOT_TOKEN", "")
_RAW_CHAT_IDS = os.environ.get("TELEGRAM_CHAT_ID") or _CONF_DEFAULTS.get("TELEGRAM_CHAT_ID", "")
ALLOWED_IDS: set[int] = {
    int(c.strip())
    for c in _RAW_CHAT_IDS.split(",")
    if c.strip() and c.strip().lstrip("-").isdigit()
}
WEBUI_USER = (
    os.environ.get("WEBUI_USERNAME")
    or os.environ.get("WEB_USERNAME")
    or _CONF_DEFAULTS.get("WEBUI_USERNAME")
    or _CONF_DEFAULTS.get("WEB_USERNAME", "")
)
WEBUI_PASS = (
    os.environ.get("WEBUI_PASSWORD")
    or os.environ.get("WEB_PASSWORD")
    or _CONF_DEFAULTS.get("WEBUI_PASSWORD")
    or _CONF_DEFAULTS.get("WEB_PASSWORD", "")
)
MUTABLE_DIR = Path(os.environ.get("MUTABLE_DIR", "/mutable"))
BACKING_DIR = Path(os.environ.get("BACKING_DIR", "/backingfiles"))
CAM_DISK = Path(os.environ.get("CAM_DISK", str(BACKING_DIR / "cam_disk.bin")))
TESLACAM_DIR = Path(os.environ.get("TESLACAM_DIR", str(MUTABLE_DIR / "TeslaCam")))
ARCHIVE_MOUNT = Path(os.environ.get("ARCHIVE_MOUNT", "/mnt/archive"))
BACKEND_URL = os.environ.get("BACKEND_URL", "http://127.0.0.1:8080")

# Experimental, off by default: "drives near here" from a location pin or
# /near, with camera frames from clips still on the Pi.
NEAR_ENABLED = (
    os.environ.get("TELEGRAM_NEAR_ENABLED") or _CONF_DEFAULTS.get("TELEGRAM_NEAR_ENABLED", "")
).lower() == "true"
NEAR_DEFAULT_RADIUS_M = 100
INSTALL_FFMPEG = Path(os.environ.get("INSTALL_FFMPEG", "/root/bin/install-ffmpeg.sh"))
FFMPEG = Path(os.environ.get("TELEMETRY_FFMPEG", "/backingfiles/tools/ffmpeg/ffmpeg"))

API = f"https://api.telegram.org/bot{TOKEN}"
MAX_CLIP_BYTES = 50 * 1024 * 1024  # Telegram sendVideo limit
MAX_RECENT = 5

# ── Telegram helpers ──────────────────────────────────────────────────────────


def _multipart(
    fields: dict[str, object], files: dict[str, BinaryIO]
) -> tuple[Iterator[bytes], dict[str, str]]:
    """Stream a multipart/form-data body so a 50 MB clip is never held in RAM."""
    boundary = uuid.uuid4().hex
    prefix = b"".join(
        f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode()
        for name, value in fields.items()
    )
    heads: list[tuple[bytes, BinaryIO]] = []
    size = len(prefix)
    for name, fh in files.items():
        filename = Path(getattr(fh, "name", name)).name.replace('"', "")
        head = (
            f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"; filename="{filename}"\r\n'
            "Content-Type: application/octet-stream\r\n\r\n"
        ).encode()
        heads.append((head, fh))
        size += len(head) + os.fstat(fh.fileno()).st_size + 2
    tail = f"--{boundary}--\r\n".encode()
    size += len(tail)

    def body() -> Iterator[bytes]:
        yield prefix
        for head, fh in heads:
            yield head
            while chunk := fh.read(64 * 1024):
                yield chunk
            yield b"\r\n"
        yield tail

    headers = {
        "Content-Type": f"multipart/form-data; boundary={boundary}",
        "Content-Length": str(size),
    }
    return body(), headers


def _api(
    method: str,
    json: dict[str, object] | None = None,
    data: dict[str, object] | None = None,
    files: dict[str, BinaryIO] | None = None,
) -> dict:  # type: ignore[type-arg]
    """POST to the Bot API with the standard library only (no requests)."""
    try:
        body: bytes | Iterator[bytes]
        if files:
            body, headers = _multipart(data or {}, files)
        elif json is not None:
            body = json_module.dumps(json).encode()
            headers = {"Content-Type": "application/json"}
        else:
            body = urllib.parse.urlencode(data or {}).encode()
            headers = {"Content-Type": "application/x-www-form-urlencoded"}
        request = urllib.request.Request(  # noqa: S310 - API is a fixed https:// URL
            f"{API}/{method}", data=body, headers=headers, method="POST"
        )
        # getUpdates long-polls for 30 s, so allow a little longer than that.
        with urllib.request.urlopen(request, timeout=45) as response:  # noqa: S310
            return json_module.loads(response.read())  # type: ignore[no-any-return]
    except Exception as exc:
        log.error("API %s failed: %s", method, exc)
        return {}


def send_text(chat_id: int, text: str, silent: bool = False) -> None:
    _api(
        "sendMessage",
        json={"chat_id": chat_id, "text": text, "disable_notification": silent},
    )


def send_video(chat_id: int, path: Path, caption: str = "") -> bool:
    if path.stat().st_size > MAX_CLIP_BYTES:
        send_text(chat_id, f"⚠️ Clip too large to send ({path.stat().st_size // 1024 // 1024} MB)")
        return False
    with path.open("rb") as fh:
        result = _api(
            "sendVideo", data={"chat_id": chat_id, "caption": caption}, files={"video": fh}
        )
    return bool(result.get("ok"))


def send_photo(
    chat_id: int, path: Path, caption: str = "", buttons: list[list[dict[str, str]]] | None = None
) -> bool:
    fields: dict[str, object] = {"chat_id": chat_id, "caption": caption}
    if buttons:
        fields["reply_markup"] = json_module.dumps({"inline_keyboard": buttons})
    with path.open("rb") as fh:
        result = _api("sendPhoto", data=fields, files={"photo": fh})
    return bool(result.get("ok"))


def get_updates(offset: int) -> list[dict[str, object]] | None:
    """Long-poll for updates. None means the request failed (no network, bad
    token, API error), as opposed to an empty list when nothing arrived."""
    data = _api(
        "getUpdates",
        json={"offset": offset, "timeout": 30, "allowed_updates": ["message", "callback_query"]},
    )
    if not data.get("ok"):
        return None
    result = data.get("result", [])
    return result if isinstance(result, list) else []


# Wait between failed getUpdates calls. A failed call returns at once (the car
# has no network for most of every drive), so without a pause the loop spins.
POLL_RETRY_MIN_SECONDS = 5.0
POLL_RETRY_MAX_SECONDS = 60.0


def next_retry_delay(previous: float) -> float:
    """Double the wait after each failure, from 5 s up to a minute."""
    if previous <= 0:
        return POLL_RETRY_MIN_SECONDS
    return min(previous * 2, POLL_RETRY_MAX_SECONDS)


# ── Command handlers ──────────────────────────────────────────────────────────


def _run(cmd: list[str]) -> str:
    try:
        return subprocess.check_output(cmd, stderr=subprocess.STDOUT, timeout=15).decode().strip()
    except subprocess.CalledProcessError as exc:
        out: bytes = exc.output or b""
        return out.decode().strip()
    except Exception as exc:
        return str(exc)


def cmd_status(chat_id: int) -> None:
    uptime = _run(["uptime", "-p"])
    try:
        url = f"{BACKEND_URL}/api/status"
        target: str | urllib.request.Request = url
        if WEBUI_USER and WEBUI_PASS:
            creds = base64.b64encode(f"{WEBUI_USER}:{WEBUI_PASS}".encode()).decode()
            target = urllib.request.Request(  # noqa: S310
                url, headers={"Authorization": f"Basic {creds}"}
            )
        with urllib.request.urlopen(target, timeout=5) as resp:  # noqa: S310
            data = json.loads(resp.read())
        archive = data.get("archive", {})
        disk = data.get("disks", data.get("disk", []))
        wifi = data.get("wifi", {})
        temp = data.get("cpu_temp_c")

        lines = ["🚗 Dashvault status"]
        lines.append(f"Archive: {archive.get('state', 'unknown')}")
        if archive.get("last_run"):
            lines.append(f"Last run: {archive['last_run']}")
        for d in disk:
            pct = d.get("used_pct", d.get("use_pct", 0))
            pct_str = f"{pct:.0f}" if isinstance(pct, float) else str(pct)
            lines.append(f"Disk {d.get('mount', '?')}: {pct_str}% used")
        if temp is not None:
            lines.append(f"CPU temp: {temp}°C")
        ssid = wifi.get("ssid")
        if ssid:
            lines.append(f"WiFi: {ssid}")
        lines.append(f"Host uptime: {uptime}")
        send_text(chat_id, "\n".join(lines))
    except Exception as exc:
        log.warning("Status API unavailable (%s); falling back to uptime", exc)
        send_text(chat_id, f"🚗 Dashvault\nHost uptime: {uptime}\n(Status API unavailable)")


def cmd_freespace(chat_id: int) -> None:
    lines = ["💾 Free space"]
    for label, path in [("Cam disk", CAM_DISK), ("Archive", ARCHIVE_MOUNT)]:
        if path.exists():
            out = _run(["df", "-h", str(path)])
            lines.append(f"\n{label}:\n{out}")
        else:
            lines.append(f"\n{label}: not mounted")
    send_text(chat_id, "\n".join(lines))


def cmd_sync(chat_id: int) -> None:
    # Signal archiveloop to treat the archive as temporarily unreachable so it
    # falls back to its "run a fresh cycle" path on the next iteration.
    sentinel = Path("/tmp/archive_is_unreachable")
    try:
        sentinel.touch()
        send_text(chat_id, "🔄 Archive sync triggered. Cycle will start shortly.")
    except OSError as exc:
        send_text(chat_id, f"❌ Could not trigger sync: {exc}")


def _latest_clips(n: int = 1) -> list[Path]:
    clips: list[Path] = []
    for subdir in ("SavedClips", "SentryClips"):
        d = TESLACAM_DIR / subdir
        if d.exists():
            clips.extend(d.rglob("*.mp4"))
    clips.sort(key=lambda p: p.stat().st_mtime, reverse=True)
    return clips[:n]


def cmd_lastclip(chat_id: int) -> None:
    clips = _latest_clips(1)
    if not clips:
        send_text(chat_id, "📭 No clips found.")
        return
    clip = clips[0]
    send_text(chat_id, f"📹 Sending: {clip.name}")
    send_video(chat_id, clip, caption=clip.name)


def cmd_recent(chat_id: int, args: str) -> None:
    try:
        n = min(int(args.strip()), MAX_RECENT)
    except ValueError:
        send_text(chat_id, f"Usage: /recent <1-{MAX_RECENT}>")
        return
    clips = _latest_clips(n)
    if not clips:
        send_text(chat_id, "📭 No clips found.")
        return
    send_text(chat_id, f"📹 Sending {len(clips)} clip(s)…")
    for clip in clips:
        send_video(chat_id, clip, caption=clip.name)


def _boot_disk() -> str:
    """Disk holding the root filesystem: mmcblk0 on a Pi, mmcblk1 on a Radxa Zero 3."""
    import glob as _glob

    try:
        majmin = ""
        with open("/proc/self/mountinfo") as mountinfo:
            for line in mountinfo:
                fields = line.split()
                if len(fields) >= 5 and fields[4] == "/":
                    majmin = fields[2]
        dev = os.path.realpath("/sys/dev/block/" + majmin)
        if majmin and os.path.isdir(dev):
            if os.path.exists(os.path.join(dev, "partition")):
                dev = os.path.dirname(dev)
            return os.path.basename(dev)
    except OSError:
        pass
    disks = sorted(_glob.glob("/sys/block/mmcblk*"))
    return os.path.basename(disks[0]) if disks else "mmcblk0"


def cmd_sdcard(chat_id: int) -> None:
    """Full SD card health report: card identity, write stats, benchmark history."""
    import json as _json

    lines = ["💾 SD card health"]

    # ── card identity from sysfs ──────────────────────────────────────────
    try:
        disk = _boot_disk()
        base = f"/sys/block/{disk}/device"
        if os.path.exists(f"{base}/name") or os.path.exists(f"{base}/manfid"):

            def _sysfs(f: str) -> str:
                try:
                    return open(f"{base}/{f}").read().strip()
                except OSError:
                    return ""

            name = _sysfs("name")
            manfid = _sysfs("manfid")
            date = _sysfs("date")
            manf_map = {
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
                "0x000082": "Sony",
                "0x000090": "SK Hynix",
            }
            manufacturer = manf_map.get(manfid, "Unknown")
            card_label = " ".join(filter(None, [manufacturer, name])) or "Unknown"
            try:
                sectors = int(open(f"/sys/block/{disk}/size").read().strip())
                cap_gb = sectors * 512 / 1e9
                card_label += f" ({cap_gb:.0f} GB)"
            except OSError:
                cap_gb = 0.0
            lines.append(f"Card: {card_label}")
            if date:
                lines.append(f"Manufactured: {date}")
        else:
            cap_gb = 0.0
    except Exception:
        cap_gb = 0.0

    # ── write stats ───────────────────────────────────────────────────────
    try:
        stats = _json.load(open(MUTABLE_DIR / "sdcard_write_stats.json"))
        total_tb = stats.get("total_bytes_written", 0) / 1e12
        since = stats.get("tracking_since", "")[:10]
        if total_tb >= 0.001:
            lines.append(f"Written: {total_tb:.3f} TB (tracked since {since})")
        elif total_tb > 0:
            lines.append(f"Written: {total_tb * 1e6:.0f} MB (tracked since {since})")

        # TBW threshold
        threshold_tb: float | None = None
        try:
            threshold_tb = float(open(MUTABLE_DIR / "sdcard_tbw_threshold").read().strip())
        except OSError:
            pass
        if threshold_tb is None and cap_gb > 0:
            threshold_tb = max(1.0, round(500 * cap_gb / 1000, 1))
        if threshold_tb is None:
            threshold_tb = 32.0
        lines.append(f"TBW threshold: {threshold_tb:.1f} TB")

        if total_tb >= threshold_tb:
            lines.append("⚠️ Write endurance threshold reached — consider replacing")
        else:
            pct = total_tb / threshold_tb * 100
            lines.append(f"Endurance used: {pct:.1f}%")
    except OSError:
        lines.append("Write stats: not available yet")

    # ── benchmark history ─────────────────────────────────────────────────
    try:
        bench = _json.load(open(MUTABLE_DIR / "sdcard_benchmark.json"))
        results = bench.get("results") or []
        if results:
            lines.append("")
            lines.append("Benchmark history (newest first):")
            for r in results[:3]:
                mbps = r.get("write_mbps")
                rating = r.get("rating", "?")
                ts = r.get("timestamp", "")[:16].replace("T", " ")
                if mbps is not None:
                    lines.append(f"  {ts}: {mbps:.1f} MB/s — {rating}")
                elif r.get("timed_out"):
                    lines.append(f"  {ts}: timed out — too slow")
                else:
                    lines.append(f"  {ts}: {rating}")
        else:
            lines.append("No benchmark results yet.")
    except OSError:
        lines.append("No benchmark results yet.")

    send_text(chat_id, "\n".join(lines))


def cmd_restart(chat_id: int) -> None:
    send_text(chat_id, "♻️ Restarting teslausb service…")
    result = _run(["systemctl", "restart", "teslausb.service"])
    send_text(chat_id, f"Done.\n{result}" if result else "Service restarted.")


def cmd_help(chat_id: int) -> None:
    near = (
        "/near <lat>,<lon> [m] — drives near a point (or share a location)\n"
        if NEAR_ENABLED
        else ""
    )
    send_text(
        chat_id,
        "🤖 Dashvault bot commands:\n"
        "/status — service status + host uptime\n"
        "/freespace — cam disk and archive free space\n"
        "/sdcard — SD card health, write stats & benchmark history\n"
        "/sync — trigger archive run now\n"
        "/lastclip — send most recent clip\n"
        f"/recent <n> — send up to {MAX_RECENT} recent clips\n"
        "/restart — restart teslausb service\n"
        f"{near}"
        "/help — this message",
    )


# ── Drives near a point (experimental) ───────────────────────────────────────

CAMERA_LABELS = {
    "front": "Front",
    "back": "Back",
    "left_repeater": "Left repeater",
    "right_repeater": "Right repeater",
    "left_pillar": "Left pillar",
    "right_pillar": "Right pillar",
}
# Recent searches, so buttons carry a short token instead of clip ids
# (callback_data is limited to 64 bytes). Oldest dropped first.
_NEAR_RESULTS: dict[str, list[dict[str, object]]] = {}
_NEAR_KEEP = 20


def parse_near_args(args: str) -> tuple[float, float, float] | None:
    """'53.80,-9.55 200', '53.80 -9.55' or '53.80, -9.55, 50' -> (lat, lon, radius)."""
    parts = args.replace(",", " ").split()
    try:
        values = [float(part) for part in parts]
    except ValueError:
        return None
    if len(values) not in (2, 3):
        return None
    lat, lon = values[0], values[1]
    radius = values[2] if len(values) == 3 else float(NEAR_DEFAULT_RADIUS_M)
    if not (-90 <= lat <= 90 and -180 <= lon <= 180 and 1 <= radius <= 50_000):
        return None
    return lat, lon, radius


def _format_time(value: object) -> str:
    try:
        moment = time.strptime(str(value)[:19], "%Y-%m-%dT%H:%M:%S")
    except ValueError:
        return str(value)
    return time.strftime("%a %d %b %H:%M:%S", moment)


def _backend_get(path: str, params: dict[str, object], timeout: float = 30) -> tuple[int, bytes]:
    """GET from the local Go backend with the web UI login. (status, body); 0 on failure."""
    url = f"{BACKEND_URL}{path}?{urllib.parse.urlencode(params)}"
    headers = {}
    if WEBUI_USER and WEBUI_PASS:
        creds = base64.b64encode(f"{WEBUI_USER}:{WEBUI_PASS}".encode()).decode()
        headers["Authorization"] = f"Basic {creds}"
    request = urllib.request.Request(url, headers=headers)  # noqa: S310 - local backend
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:  # noqa: S310
            return response.status, response.read()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()
    except OSError as exc:
        log.error("backend %s failed: %s", path, exc)
        return 0, b""


def _near_search(lat: float, lon: float, radius: float) -> list[dict[str, object]] | None:
    """Passes near a point from the backend (GET /api/street/near)."""
    status, body = _backend_get(
        "/api/street/near",
        {"lat": f"{lat:.6f}", "lon": f"{lon:.6f}", "radius": f"{radius:.0f}"},
        120,
    )
    if status != 200:
        log.error("near search failed: %s %s", status, body[:200])
        return None
    try:
        passes = json_module.loads(body).get("passes")
    except (ValueError, AttributeError):
        return None
    return passes if isinstance(passes, list) else None


def cmd_near(chat_id: int, lat: float, lon: float, radius: float) -> None:
    passes = _near_search(lat, lon, radius)
    if passes is None:
        send_text(chat_id, "❌ Could not search the GPS tracks.")
        return
    if not passes:
        send_text(
            chat_id, f"📍 No drives within {radius:.0f} m of that spot in the stored GPS tracks."
        )
        return
    token = uuid.uuid4().hex[:8]
    _NEAR_RESULTS[token] = passes
    while len(_NEAR_RESULTS) > _NEAR_KEEP:
        _NEAR_RESULTS.pop(next(iter(_NEAR_RESULTS)))
    lines = [f"📍 {len(passes)} pass(es) within {radius:.0f} m, newest first:"]
    buttons: list[list[dict[str, str]]] = []
    for number, item in enumerate(passes, 1):
        cameras = item.get("cameras") or []
        where = "Saved" if item.get("saved") else "Recent"
        video = "video on Pi" if cameras else "video no longer on Pi"
        lines.append(
            f"{number}. {_format_time(item.get('at'))} · {item.get('distance_m')} m · "
            f"{item.get('speed_kmh')} km/h · {where} · {video}"
        )
        if isinstance(cameras, list) and cameras:
            camera = "front" if "front" in cameras else str(cameras[0])
            buttons.append(
                [
                    {
                        "text": f"📷 {number} · {_format_time(item.get('at'))[-8:-3]}",
                        "callback_data": f"nf|{token}|{number - 1}|{camera}",
                    }
                ]
            )
    _api(
        "sendMessage",
        json={
            "chat_id": chat_id,
            "text": "\n".join(lines),
            **({"reply_markup": {"inline_keyboard": buttons}} if buttons else {}),
        },
    )


def _install_ffmpeg(chat_id: int) -> bool:
    """Run the pinned static ffmpeg installer once (the bot runs as root)."""
    send_text(chat_id, "⏳ Installing ffmpeg once (about 15-20 MB download)…")
    try:
        result = subprocess.run(  # noqa: S603 — fixed installer script
            ["/bin/bash", str(INSTALL_FFMPEG)],
            capture_output=True,
            text=True,
            timeout=900,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        send_text(chat_id, f"❌ Could not install ffmpeg: {exc}")
        return False
    if result.returncode != 0 or not FFMPEG.is_file():
        send_text(
            chat_id,
            f"❌ Could not install ffmpeg: {(result.stderr or result.stdout).strip()[:300]}",
        )
        return False
    return True


def _fetch_frame(item: dict[str, object], camera: str) -> tuple[int, bytes]:
    return _backend_get(
        "/api/street/frame", {"id": item.get("id"), "camera": camera, "t": item.get("t", 0)}, 180
    )


def cmd_near_frame(chat_id: int, token: str, index: int, camera: str) -> None:
    passes = _NEAR_RESULTS.get(token)
    if not passes or not 0 <= index < len(passes) or camera not in CAMERA_LABELS:
        send_text(chat_id, "⌛ That search has expired; send the location again.")
        return
    item = passes[index]
    listed = item.get("cameras")
    cameras = [str(c) for c in listed] if isinstance(listed, list) else []
    if camera not in cameras:
        send_text(chat_id, f"🎞 The {CAMERA_LABELS[camera].lower()} video is no longer on the Pi.")
        return
    status, body = _fetch_frame(item, camera)
    if status == 503 and _install_ffmpeg(chat_id):
        status, body = _fetch_frame(item, camera)
    if status != 200 or not body:
        try:
            reason = str(json_module.loads(body).get("error"))
        except (ValueError, AttributeError):
            reason = f"backend answered {status}"
        send_text(chat_id, f"❌ Could not read the frame: {reason}")
        return
    import tempfile as _tempfile

    with _tempfile.TemporaryDirectory(prefix="teslausb-near-") as tmp:
        output = Path(tmp) / f"{camera}.jpg"
        output.write_bytes(body)
        others = [
            {"text": CAMERA_LABELS[c], "callback_data": f"nf|{token}|{index}|{c}"}
            for c in cameras
            if c != camera and c in CAMERA_LABELS
        ]
        caption = (
            f"{CAMERA_LABELS[camera]} · {_format_time(item.get('at'))} · "
            f"{item.get('distance_m')} m from the spot · {item.get('speed_kmh')} km/h"
        )
        rows = [others[i : i + 3] for i in range(0, len(others), 3)]
        if not send_photo(chat_id, output, caption, rows or None):
            send_text(chat_id, "❌ Telegram did not accept the picture.")


def dispatch_callback(chat_id: int, data: str) -> None:
    parts = data.split("|")
    if len(parts) == 4 and parts[0] == "nf" and parts[2].isdigit() and NEAR_ENABLED:
        cmd_near_frame(chat_id, parts[1], int(parts[2]), parts[3])


# ── Dispatcher ────────────────────────────────────────────────────────────────


def dispatch(chat_id: int, text: str) -> None:
    text = text.strip()
    if text.startswith("/status"):
        cmd_status(chat_id)
    elif text.startswith("/freespace"):
        cmd_freespace(chat_id)
    elif text.startswith("/sdcard"):
        cmd_sdcard(chat_id)
    elif text.startswith("/sync"):
        cmd_sync(chat_id)
    elif text.startswith("/lastclip"):
        cmd_lastclip(chat_id)
    elif text.startswith("/recent"):
        cmd_recent(chat_id, text[len("/recent") :])
    elif text.startswith("/restart"):
        cmd_restart(chat_id)
    elif text.startswith("/near") and NEAR_ENABLED:
        parsed = parse_near_args(text[len("/near") :])
        if parsed is None:
            send_text(
                chat_id,
                "📍 Share a location (📎 → Location), or send /near <lat>,<lon> [radius in m], "
                f"e.g. /near 53.8008,-9.5491 {NEAR_DEFAULT_RADIUS_M}",
            )
        else:
            cmd_near(chat_id, *parsed)
    else:
        cmd_help(chat_id)


def handle_update(update: dict[str, object]) -> None:
    """One getUpdates item: a text command, a shared location, or a button press."""
    callback = update.get("callback_query")
    if isinstance(callback, dict):
        message = callback.get("message") or {}
        chat = message.get("chat") if isinstance(message, dict) else None
        chat_id = chat.get("id") if isinstance(chat, dict) else None
        _api("answerCallbackQuery", json={"callback_query_id": callback.get("id")})
        if not isinstance(chat_id, int) or chat_id not in ALLOWED_IDS:
            log.warning("Ignoring button press from unauthorized chat_id %s", chat_id)
            return
        dispatch_callback(chat_id, str(callback.get("data") or ""))
        return
    msg = update.get("message")
    if not isinstance(msg, dict):
        return
    chat_obj = msg.get("chat") or {}
    chat_id = chat_obj.get("id") if isinstance(chat_obj, dict) else None
    if not isinstance(chat_id, int):
        return
    location = msg.get("location")
    text = str(msg.get("text", "") or "")
    if not text and not isinstance(location, dict):
        return
    if chat_id not in ALLOWED_IDS:
        log.warning("Ignoring message from unauthorized chat_id %s", chat_id)
        return
    if isinstance(location, dict):
        if NEAR_ENABLED:
            cmd_near(
                chat_id,
                float(location.get("latitude", 0.0)),
                float(location.get("longitude", 0.0)),
                float(NEAR_DEFAULT_RADIUS_M),
            )
        return
    log.info("Command from %s: %s", chat_id, text)
    dispatch(chat_id, text)


# ── Main loop ─────────────────────────────────────────────────────────────────


def main() -> None:
    if not TOKEN:
        log.error("TELEGRAM_BOT_TOKEN not set — exiting.")
        sys.exit(1)
    if not ALLOWED_IDS:
        log.error("TELEGRAM_CHAT_ID not set — exiting.")
        sys.exit(1)

    log.info("TeslaUSB Telegram bot starting. Allowed IDs: %s", ALLOWED_IDS)
    offset = 0
    retry_delay = 0.0

    while True:
        try:
            updates = get_updates(offset)
        except Exception as exc:
            log.warning("getUpdates error: %s", exc)
            updates = None
        if updates is None:
            retry_delay = next_retry_delay(retry_delay)
            log.warning("getUpdates failed, retrying in %.0f s", retry_delay)
            time.sleep(retry_delay)
            continue
        retry_delay = 0.0

        for update in updates:
            update_id: object = update.get("update_id", 0)
            offset = (int(update_id) if isinstance(update_id, (int, float)) else 0) + 1
            try:
                handle_update(update)
            except Exception as exc:
                log.error("dispatch error: %s", exc)
                callback = update.get("callback_query")
                message = update.get("message") or (
                    callback.get("message") if isinstance(callback, dict) else None
                )
                chat = message.get("chat") if isinstance(message, dict) else None
                if isinstance(chat, dict) and chat.get("id") in ALLOWED_IDS:
                    send_text(int(chat["id"]), f"❌ Error: {exc}")


if __name__ == "__main__":
    main()
