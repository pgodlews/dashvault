#!/usr/bin/env python3

from __future__ import annotations

import json
import os
import re
import shlex
import tempfile
from pathlib import Path

ARCHIVE_TYPES = {"cifs", "nfs", "rsync", "rclone", "none"}
ERROR_FILE_NAME = "provisioning_error"
MAX_COMMAND_BYTES = 16 * 1024
# A GATT attribute value can be at most 512 bytes.
MAX_SCAN_BYTES = 500


def shell_quote(value: str) -> str:
    return "'" + value.replace("'", "'\\''") + "'"


def read_configuration(path: Path) -> dict[str, str]:
    settings: dict[str, str] = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if line.startswith("export "):
            line = line[7:]
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values = shlex.split(value)
        settings[key] = values[0] if values else ""
    return settings


def write_configuration(
    path: Path,
    archive_type: str,
    archive_host: str,
    share_name: str,
    username: str,
    password: str,
    *,
    wifi_ssid: str = "",
    wifi_password: str = "",
    webui_username: str = "",
    webui_password: str = "",
) -> None:
    if archive_type not in ARCHIVE_TYPES:
        raise ValueError(f"Unsupported archive system: {archive_type}")
    if archive_type != "none" and (not archive_host or not share_name):
        raise ValueError("archive_host and share_name are required")
    # The file is sourced by root's shell and read line by line, and the host
    # and user end up on ssh/rsync/rclone command lines, where a leading "-"
    # would be taken as an option.
    for value in (
        archive_host,
        share_name,
        username,
        password,
        wifi_ssid,
        wifi_password,
        webui_username,
        webui_password,
    ):
        if any(
            ord(char) < 32 or 127 <= ord(char) < 160 or char in "\u2028\u2029" for char in value
        ):
            raise ValueError("Settings must be single-line text without control characters")
    if archive_host.startswith("-") or any(char in archive_host for char in " \\/#"):
        raise ValueError("Invalid archive host")
    if archive_type == "rsync" and not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", username):
        raise ValueError("Invalid rsync user: use letters, digits, '.', '_' and '-' only")

    settings = {"ARCHIVE_SYSTEM": archive_type}
    required = ["ARCHIVE_SYSTEM"]
    if archive_type in {"cifs", "nfs"}:
        settings.update(ARCHIVE_SERVER=archive_host, SHARE_NAME=share_name)
        required += ["ARCHIVE_SERVER", "SHARE_NAME"]
    if archive_type == "nfs":
        settings.update(NFS_SERVER=archive_host, NFS_PATH=share_name)
        required += ["NFS_SERVER", "NFS_PATH"]
    if archive_type == "cifs":
        settings.update(SHARE_USER=username, SHARE_PASSWORD=password)
        required += ["SHARE_USER", "SHARE_PASSWORD"]
    elif archive_type == "rsync":
        settings.update(RSYNC_SERVER=archive_host, RSYNC_PATH=share_name, RSYNC_USER=username)
        required += ["RSYNC_SERVER", "RSYNC_PATH", "RSYNC_USER"]
    elif archive_type == "rclone":
        settings.update(RCLONE_DRIVE=archive_host, RCLONE_PATH=share_name)
        required += ["RCLONE_DRIVE", "RCLONE_PATH"]

    if wifi_ssid:
        settings.update(SSID=wifi_ssid, WIFIPASS=wifi_password)
        required += ["SSID", "WIFIPASS"]
    if webui_username:
        # The one password chosen during provisioning also replaces the image's
        # well-known default for the OS user, which has SSH enabled.
        settings.update(
            WEBUI_USERNAME=webui_username,
            WEBUI_PASSWORD=webui_password,
            PI_PASSWORD=webui_password,
        )
        required += ["WEBUI_USERNAME", "WEBUI_PASSWORD", "PI_PASSWORD"]

    lines = ["# Dashvault configuration — written by first-run provisioning"]
    lines += [f"export {key}={shell_quote(value)}" for key, value in settings.items()]
    content = "\n".join(lines) + "\n"

    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w") as output:
            output.write(content)
        temporary.chmod(0o600)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)

    saved = read_configuration(path)
    if any(saved.get(key) != settings[key] for key in required):
        raise OSError("Written provisioning configuration failed read-back validation")


def _text(request: dict[str, object], key: str, default: str = "") -> str:
    value = request.get(key, default)
    if not isinstance(value, str):
        raise ValueError(f"{key} must be text")
    return value


def validate_login(username: str, password: str) -> None:
    if not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9._-]{0,31}", username):
        raise ValueError("Username: use up to 32 letters, digits, '.', '_' or '-'")
    if not 8 <= len(password) <= 128:
        raise ValueError("Password must be 8 to 128 characters long")


def validate_wifi(ssid: str, password: str) -> None:
    if not ssid:
        return
    if len(ssid.encode()) > 32:
        raise ValueError("Wi-Fi network name is longer than 32 bytes")
    is_hex_key = re.fullmatch(r"[0-9A-Fa-f]{64}", password) is not None
    if password and not is_hex_key and not 8 <= len(password.encode()) <= 63:
        raise ValueError("Wi-Fi password must be 8 to 63 characters long")


def apply_request(request: object, path: Path, sentinel: Path) -> None:
    """Validate a hotspot or Bluetooth setup request and save it.

    Only writes the configuration file. The next boot runs the normal setup
    from it, the same way as a file placed on the boot partition by hand.
    ValueError means the request is wrong and nothing was written.
    """
    if not isinstance(request, dict):
        raise ValueError("Request must be a JSON object")
    wifi_ssid = _text(request, "wifi_ssid").strip()
    wifi_password = _text(request, "wifi_password")
    webui_username = _text(request, "webui_username").strip()
    webui_password = _text(request, "webui_password")
    validate_wifi(wifi_ssid, wifi_password)
    validate_login(webui_username, webui_password)
    write_configuration(
        path,
        _text(request, "archive_type", "none"),
        _text(request, "archive_host").strip(),
        _text(request, "share_name").strip(),
        _text(request, "username"),
        _text(request, "password"),
        wifi_ssid=wifi_ssid,
        wifi_password=wifi_password,
        webui_username=webui_username,
        webui_password=webui_password,
    )
    (sentinel.parent / ERROR_FILE_NAME).unlink(missing_ok=True)
    sentinel.touch()


def last_error(sentinel: Path) -> str:
    """Why the previous attempt failed (for example a wrong Wi-Fi password)."""
    try:
        return (sentinel.parent / ERROR_FILE_NAME).read_text(errors="replace").strip()[:200]
    except OSError:
        return ""


def fit_scan_json(raw: bytes, limit: int = MAX_SCAN_BYTES) -> bytes:
    """Keep the strongest networks that fit in one GATT attribute."""
    try:
        networks = json.loads(raw)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return b"[]"
    if not isinstance(networks, list):
        return b"[]"
    while True:
        value = json.dumps(networks, ensure_ascii=False, separators=(",", ":")).encode()
        if len(value) <= limit:
            return value
        networks.pop()


class ChunkAssembler:
    """Reassembles writes of [chunk index, chunk count, payload...]."""

    def __init__(self) -> None:
        self._chunks: dict[int, bytes] = {}
        self._total = 0

    def add(self, data: bytes) -> bytes | None:
        """Return the whole message once its last chunk has arrived."""
        if len(data) < 2:
            raise ValueError("chunk too short")
        index, total = data[0], data[1]
        if total == 0 or index >= total:
            raise ValueError("bad chunk header")
        if total != self._total or index == 0:
            self._chunks = {}
            self._total = total
        self._chunks[index] = data[2:]
        if sum(len(chunk) for chunk in self._chunks.values()) > MAX_COMMAND_BYTES:
            self._chunks = {}
            raise ValueError("command too long")
        if len(self._chunks) < total:
            return None
        message = b"".join(self._chunks[i] for i in range(total))
        self._chunks = {}
        return message
