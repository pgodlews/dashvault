"""Tests for teslausb_telegram_bot.py"""

from __future__ import annotations

import io
import json
import sys
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock, patch

import pytest

RUN_DIR = Path(__file__).resolve().parents[2] / "run"
if str(RUN_DIR) not in sys.path:
    sys.path.insert(0, str(RUN_DIR))

os_env_patch = {
    "TELEGRAM_BOT_TOKEN": "123:testtoken",
    "TELEGRAM_CHAT_ID": "111,222",
    "MUTABLE_DIR": "/tmp/teslausb-test/mutable",
    "BACKING_DIR": "/tmp/teslausb-test/backing",
}


@pytest.fixture(autouse=True)
def _patch_env(monkeypatch: pytest.MonkeyPatch) -> None:
    for k, v in os_env_patch.items():
        monkeypatch.setenv(k, v)


@pytest.fixture()
def bot():
    import importlib
    import teslausb_telegram_bot as m
    importlib.reload(m)
    return m


def test_allowed_ids_parsed(bot) -> None:
    assert bot.ALLOWED_IDS == {111, 222}


def test_send_text_calls_api(bot) -> None:
    with patch.object(bot, "_api") as mock_api:
        bot.send_text(111, "hello")
        mock_api.assert_called_once()
        _, kwargs = mock_api.call_args
        assert kwargs["json"]["text"] == "hello"
        assert kwargs["json"]["chat_id"] == 111


def test_unauthorized_chat_ignored(bot) -> None:
    with patch.object(bot, "send_text") as mock_send:
        with patch.object(bot, "get_updates", return_value=[
            {"update_id": 1, "message": {"chat": {"id": 999}, "text": "/status"}}
        ]):
            bot.ALLOWED_IDS = {111}
            updates = bot.get_updates(0)
            for u in updates:
                msg = u.get("message", {})
                chat_id = msg.get("chat", {}).get("id")
                if chat_id not in bot.ALLOWED_IDS:
                    continue
                bot.dispatch(chat_id, msg.get("text", ""))
        mock_send.assert_not_called()


def test_dispatch_help(bot) -> None:
    with patch.object(bot, "send_text") as mock_send:
        bot.dispatch(111, "/help")
        mock_send.assert_called_once()
        assert "/status" in mock_send.call_args[0][1]


def test_dispatch_unknown_shows_help(bot) -> None:
    with patch.object(bot, "send_text") as mock_send:
        bot.dispatch(111, "/unknowncommand")
        mock_send.assert_called_once()
        assert "/status" in mock_send.call_args[0][1]


def test_cmd_freespace_missing_mount(bot, tmp_path: Path) -> None:
    bot.CAM_DISK = tmp_path / "nonexistent.bin"
    bot.ARCHIVE_MOUNT = tmp_path / "no-archive"
    with patch.object(bot, "send_text") as mock_send:
        bot.cmd_freespace(111)
        text = mock_send.call_args[0][1]
        assert "not mounted" in text


def test_cmd_lastclip_no_clips(bot, tmp_path: Path) -> None:
    bot.TESLACAM_DIR = tmp_path
    (tmp_path / "SavedClips").mkdir()
    (tmp_path / "SentryClips").mkdir()
    with patch.object(bot, "send_text") as mock_send:
        bot.cmd_lastclip(111)
        assert "No clips" in mock_send.call_args[0][1]


def test_recent_invalid_arg(bot) -> None:
    with patch.object(bot, "send_text") as mock_send:
        bot.cmd_recent(111, "notanumber")
        assert "Usage" in mock_send.call_args[0][1]


# ── cmd_status ────────────────────────────────────────────────────────────────

def _make_mock_response(payload: dict) -> MagicMock:
    """Return a mock context manager that simulates urllib.request.urlopen."""
    mock_resp = MagicMock()
    mock_resp.read.return_value = json.dumps(payload).encode()
    mock_resp.__enter__ = lambda s: s
    mock_resp.__exit__ = MagicMock(return_value=False)
    return mock_resp


def test_cmd_status_uses_backend_api(bot) -> None:
    """cmd_status should call /api/status and format archive state."""
    payload = {
        "archive": {"state": "idle", "last_run": "2026-04-25T12:00:00Z"},
        "disks": [{"mount": "/mutable", "used_pct": 42.4}],
        "cpu_temp_c": 55.0,
        "wifi": {"ssid": "HomeNet"},
    }
    mock_resp = _make_mock_response(payload)
    with patch("urllib.request.urlopen", return_value=mock_resp):
        with patch.object(bot, "send_text") as mock_send:
            with patch.object(bot, "_run", return_value="up 2 hours"):
                bot.cmd_status(111)
    text = mock_send.call_args[0][1]
    assert "idle" in text
    assert "42%" in text
    assert "55" in text
    assert "HomeNet" in text
    assert "up 2 hours" in text


def test_cmd_restart_does_not_invoke_sudo(bot: object) -> None:
    """cmd_restart runs as root under NoNewPrivileges=yes and must not call sudo."""
    with patch.object(bot, "send_text"), patch.object(bot, "_run", return_value="") as mock_run:
        bot.cmd_restart(111)  # type: ignore[attr-defined]
    mock_run.assert_called_once_with(["systemctl", "restart", "teslausb.service"])


def test_telegram_service_does_not_enable_private_tmp() -> None:
    unit = (
        Path(__file__).resolve().parents[2] / "setup/pi/teslausb-telegram-bot.service"
    ).read_text()
    assert "PrivateTmp=yes" not in unit
    assert "ReadWritePaths=/mutable /backingfiles /mnt /tmp /run/teslausb" in unit


def test_cmd_status_fallback_when_api_unavailable(bot) -> None:
    """cmd_status should fall back gracefully when the backend is unreachable."""
    with patch("urllib.request.urlopen", side_effect=OSError("connection refused")):
        with patch.object(bot, "send_text") as mock_send:
            with patch.object(bot, "_run", return_value="up 1 hour"):
                bot.cmd_status(111)
    text = mock_send.call_args[0][1]
    assert "up 1 hour" in text
    assert "unavailable" in text.lower()


# ── cmd_sync ─────────────────────────────────────────────────────────────────

def test_cmd_sync_touches_sentinel(bot, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """cmd_sync should touch the sentinel file and report success."""
    sentinel = tmp_path / "archive_is_unreachable"

    # Redirect Path("/tmp/archive_is_unreachable") → our tmp sentinel.
    # We patch the Path constructor used inside the module via monkeypatch.
    real_path = bot.Path

    def fake_path(*args: object, **kwargs: object) -> Path:
        p = real_path(*args, **kwargs)
        if p.name == "archive_is_unreachable":
            return sentinel
        return p

    monkeypatch.setattr(bot, "Path", fake_path)

    with patch.object(bot, "send_text") as mock_send:
        bot.cmd_sync(111)

    assert sentinel.exists(), "sentinel file was not created"
    text = mock_send.call_args[0][1]
    assert "triggered" in text.lower()


def test_cmd_sync_oserror_reports_failure(bot, monkeypatch: pytest.MonkeyPatch) -> None:
    """cmd_sync should send an error message if touching the sentinel fails."""
    real_path = bot.Path

    class _FailPath:
        def __init__(self, *args: object, **kwargs: object) -> None:
            self._p = real_path(*args, **kwargs)

        @property
        def name(self) -> str:
            return self._p.name

        def touch(self) -> None:
            raise OSError("read-only filesystem")

        def __str__(self) -> str:
            return str(self._p)

    def fake_path(*args: object, **kwargs: object):  # type: ignore[return]
        p = real_path(*args, **kwargs)
        if p.name == "archive_is_unreachable":
            return _FailPath(*args, **kwargs)
        return p

    monkeypatch.setattr(bot, "Path", fake_path)

    with patch.object(bot, "send_text") as mock_send:
        bot.cmd_sync(111)

    text = mock_send.call_args[0][1]
    assert "❌" in text or "error" in text.lower() or "Could not" in text


def test_sdcard_uses_mutable_data_and_shows_small_write_total(bot, tmp_path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(bot, "MUTABLE_DIR", tmp_path)
    (tmp_path / "sdcard_write_stats.json").write_text(json.dumps({
        "total_bytes_written": 512000000,
        "tracking_since": "2026-11-07T12:00:00Z",
    }))
    (tmp_path / "sdcard_benchmark.json").write_text(json.dumps({
        "results": [{
            "timestamp": "2026-11-07T12:10:00Z",
            "write_mbps": 10.9,
            "rating": "slow",
        }],
    }))
    sent = []
    monkeypatch.setattr(bot, "send_text", lambda chat_id, text: sent.append(text))

    bot.cmd_sdcard(111)
    message = sent[0]
    assert "Written: 512 MB" in message
    assert "10.9 MB/s — slow" in message


def _capture_request(bot: Any, payload: dict[str, object]) -> tuple[list[Any], Any]:
    """Patch urlopen; return the list that receives each urllib Request sent."""
    sent: list[Any] = []
    resp = MagicMock()
    resp.read.return_value = json.dumps(payload).encode()
    resp.__enter__ = lambda s: s
    resp.__exit__ = MagicMock(return_value=False)

    def fake_urlopen(request: Any, timeout: float) -> Any:
        # Like the real urlopen, consume a streamed body during the call,
        # while the caller still has the file open.
        # (Assigning .data drops Content-Length, so keep the header first.)
        request.sent_length = request.get_header("Content-length")
        if not isinstance(request.data, bytes):
            request.data = b"".join(request.data)
        sent.append(request)
        return resp

    return sent, patch("urllib.request.urlopen", side_effect=fake_urlopen)


def test_bot_needs_only_the_standard_library() -> None:
    # A fresh Debian image has no python3-requests; the bot must still start.
    source = (RUN_DIR / "teslausb_telegram_bot.py").read_text()
    assert "import requests" not in source


def test_api_posts_json(bot: Any) -> None:
    sent, patcher = _capture_request(bot, {"ok": True, "result": []})
    with patcher:
        assert bot._api("getUpdates", json={"offset": 5}) == {"ok": True, "result": []}
    request = sent[0]
    assert request.full_url.endswith("/getUpdates")
    assert request.get_header("Content-type") == "application/json"
    assert json.loads(request.data) == {"offset": 5}


def test_send_video_streams_multipart_with_exact_length(bot: Any, tmp_path: Path) -> None:
    clip = tmp_path / "2026-11-11_14-00-00-front.mp4"
    clip.write_bytes(b"\x00\x01video-bytes" * 1000)
    sent, patcher = _capture_request(bot, {"ok": True})
    with patcher:
        assert bot.send_video(111, clip, caption="Front camera")
    request = sent[0]
    body = request.data
    assert int(request.sent_length) == len(body)
    boundary = request.get_header("Content-type").split("boundary=")[1]
    assert body.startswith(f"--{boundary}\r\n".encode())
    assert body.endswith(f"--{boundary}--\r\n".encode())
    assert b'name="caption"\r\n\r\nFront camera\r\n' in body
    assert f'filename="{clip.name}"'.encode() in body
    assert clip.read_bytes() in body


def test_api_failure_returns_empty_dict(bot: Any) -> None:
    with patch("urllib.request.urlopen", side_effect=OSError("network down")):
        assert bot._api("sendMessage", json={"chat_id": 1, "text": "x"}) == {}


# ── drives near a point (experimental) ───────────────────────────────────────

PASSES = [
    {"id": "RecentClips/2026-11-12/2026-11-12_10-01-00-front.mp4", "at": "2026-11-12T10:01:59", "t": 59,
     "distance_m": 0, "speed_kmh": 36, "saved": False, "cameras": ["front", "back", "left_repeater"]},
    {"id": "RecentClips/2026-11-11/2026-11-11_09-00-00-front.mp4", "at": "2026-11-11T09:00:20", "t": 20,
     "distance_m": 40, "speed_kmh": 50, "saved": True, "cameras": []},
]


@pytest.fixture()
def near_bot(bot, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setattr(bot, "NEAR_ENABLED", True)
    bot._NEAR_RESULTS.clear()
    return bot


@pytest.mark.parametrize(
    "args,expected",
    [
        (" 53.8008,-9.5491", (53.8008, -9.5491, 100.0)),
        (" 53.8008 -9.5491 250", (53.8008, -9.5491, 250.0)),
        (" 53.8, -9.5, 50", (53.8, -9.5, 50.0)),
        ("", None),
        (" here", None),
        (" 95,0", None),
        (" 53,-1,99999", None),
    ],
)
def test_parse_near_args(bot, args: str, expected: Any) -> None:
    assert bot.parse_near_args(args) == expected


def test_location_pin_searches_with_the_default_radius(near_bot) -> None:
    with patch.object(near_bot, "cmd_near") as cmd_near:
        near_bot.handle_update({"message": {"chat": {"id": 111}, "location": {"latitude": 53.8, "longitude": -9.55}}})
    cmd_near.assert_called_once_with(111, 53.8, -9.55, 100.0)


def test_location_pin_is_ignored_when_disabled_or_unauthorized(bot, monkeypatch: pytest.MonkeyPatch) -> None:
    with patch.object(bot, "cmd_near") as cmd_near:
        bot.handle_update({"message": {"chat": {"id": 111}, "location": {"latitude": 53.8, "longitude": -9.55}}})
        monkeypatch.setattr(bot, "NEAR_ENABLED", True)
        bot.handle_update({"message": {"chat": {"id": 999}, "location": {"latitude": 53.8, "longitude": -9.55}}})
    cmd_near.assert_not_called()


def test_near_lists_passes_with_a_button_per_pass_on_the_pi(near_bot) -> None:
    body = json.dumps({"passes": PASSES}).encode()
    with patch.object(near_bot, "_backend_get", return_value=(200, body)) as backend, \
            patch.object(near_bot, "_api") as api:
        near_bot.cmd_near(111, 53.8, -9.55, 100)
    path, params, _timeout = backend.call_args.args
    assert path == "/api/street/near" and params["radius"] == "100"
    message = api.call_args.kwargs["json"]
    assert "2 pass(es) within 100 m" in message["text"]
    assert "video no longer on Pi" in message["text"]
    buttons = message["reply_markup"]["inline_keyboard"]
    assert len(buttons) == 1  # the archived pass has no button
    token = next(iter(near_bot._NEAR_RESULTS))
    assert buttons[0][0]["callback_data"] == f"nf|{token}|0|front"


def test_near_with_no_passes_says_so(near_bot) -> None:
    with patch.object(near_bot, "_backend_get", return_value=(200, b'{"passes": []}')), \
            patch.object(near_bot, "send_text") as send_text:
        near_bot.cmd_near(111, 53.8, -9.55, 100)
    assert "No drives within 100 m" in send_text.call_args.args[1]


def test_button_sends_the_frame_with_buttons_for_other_cameras(near_bot) -> None:
    near_bot._NEAR_RESULTS["tok"] = PASSES
    with patch.object(near_bot, "_backend_get", return_value=(200, b"JPEG")) as backend, \
            patch.object(near_bot, "send_photo", return_value=True) as send_photo, \
            patch.object(near_bot, "_api") as api:
        near_bot.handle_update({"callback_query": {"id": "cb1", "data": "nf|tok|0|back",
                                                   "message": {"chat": {"id": 111}}}})
    api.assert_called_once_with("answerCallbackQuery", json={"callback_query_id": "cb1"})
    path, params, _timeout = backend.call_args.args
    assert path == "/api/street/frame" and params["camera"] == "back" and params["t"] == 59
    chat_id, _photo, caption, rows = send_photo.call_args.args
    assert chat_id == 111 and caption.startswith("Back · ")
    assert [b["callback_data"] for b in rows[0]] == ["nf|tok|0|front", "nf|tok|0|left_repeater"]


def test_missing_ffmpeg_is_installed_then_the_frame_is_retried(near_bot) -> None:
    near_bot._NEAR_RESULTS["tok"] = PASSES
    answers = iter([(503, b'{"install": true}'), (200, b"JPEG")])
    with patch.object(near_bot, "_backend_get", side_effect=lambda *a: next(answers)), \
            patch.object(near_bot, "_install_ffmpeg", return_value=True) as install, \
            patch.object(near_bot, "send_photo", return_value=True) as send_photo:
        near_bot.cmd_near_frame(111, "tok", 0, "front")
    install.assert_called_once_with(111)
    send_photo.assert_called_once()


def test_expired_search_or_unauthorized_button(near_bot) -> None:
    with patch.object(near_bot, "send_text") as send_text:
        near_bot.cmd_near_frame(111, "gone", 0, "front")
    assert "expired" in send_text.call_args.args[1]
    with patch.object(near_bot, "cmd_near_frame") as frame, patch.object(near_bot, "_api"):
        near_bot.handle_update({"callback_query": {"id": "x", "data": "nf|tok|0|front", "message": {"chat": {"id": 999}}}})
    frame.assert_not_called()


# ── polling backoff ───────────────────────────────────────────────────────────


class _StopLoop(BaseException):
    """Ends bot.main() from a test; not an Exception, so main() does not catch it."""


def test_get_updates_tells_failure_from_no_updates(bot) -> None:
    with patch.object(bot, "_api", return_value={}):
        assert bot.get_updates(0) is None
    with patch.object(bot, "_api", return_value={"ok": True, "result": []}):
        assert bot.get_updates(0) == []


def test_main_backs_off_while_the_network_is_down(bot) -> None:
    answers: list[Any] = [None, None, None, None, None, None, _StopLoop()]
    with patch.object(bot, "get_updates", side_effect=answers), \
            patch.object(bot.time, "sleep") as sleep, \
            pytest.raises(_StopLoop):
        bot.main()
    assert [call.args[0] for call in sleep.call_args_list] == [5, 10, 20, 40, 60, 60]


def test_main_backoff_restarts_after_a_successful_poll(bot) -> None:
    answers: list[Any] = [None, None, [], RuntimeError("boom"), _StopLoop()]
    with patch.object(bot, "get_updates", side_effect=answers), \
            patch.object(bot.time, "sleep") as sleep, \
            pytest.raises(_StopLoop):
        bot.main()
    assert [call.args[0] for call in sleep.call_args_list] == [5, 10, 5]


def test_remember_chat_records_without_authorizing(bot, tmp_path: Path) -> None:
    bot.RECENT_CHATS_FILE = tmp_path / "recent.json"
    update = {
        "update_id": 1,
        "message": {
            "date": 1700000000,
            "text": "hello",
            "chat": {"id": 333, "type": "private", "first_name": "Ann", "username": "ann"},
        },
    }
    with patch.object(bot, "dispatch") as dispatch:
        bot.handle_update(update)
    dispatch.assert_not_called()
    assert 333 not in bot.ALLOWED_IDS
    assert json.loads(bot.RECENT_CHATS_FILE.read_text()) == [
        {"id": 333, "type": "private", "name": "Ann", "username": "ann", "last_seen": 1700000000}
    ]
    assert bot.RECENT_CHATS_FILE.stat().st_mode & 0o777 == 0o600


def test_remember_chat_keeps_only_the_newest(bot, tmp_path: Path) -> None:
    bot.RECENT_CHATS_FILE = tmp_path / "recent.json"
    for chat_id in range(1, bot.MAX_RECENT_CHATS + 3):
        bot.remember_chat(
            {"message": {"date": 1000 + chat_id, "chat": {"id": chat_id, "type": "private"}}}
        )
    stored = json.loads(bot.RECENT_CHATS_FILE.read_text())
    assert len(stored) == bot.MAX_RECENT_CHATS
    assert stored[0]["id"] == bot.MAX_RECENT_CHATS + 2
    assert all(chat["id"] > 2 for chat in stored)


def test_remember_chat_failure_does_not_stop_commands(bot, tmp_path: Path) -> None:
    bot.RECENT_CHATS_FILE = tmp_path / "missing" / "recent.json"
    update = {"message": {"date": 1, "text": "/help", "chat": {"id": 111, "type": "private"}}}
    with patch.object(bot, "dispatch") as dispatch:
        bot.handle_update(update)
    dispatch.assert_called_once_with(111, "/help")
