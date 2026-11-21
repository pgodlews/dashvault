import importlib
import json
import struct
import sys
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo

import pytest

RUN_DIR = Path(__file__).resolve().parents[2] / "run"
if str(RUN_DIR) not in sys.path:
    sys.path.insert(0, str(RUN_DIR))


@pytest.fixture()
def telemetry(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Any:
    monkeypatch.setenv("TELEMETRY_STORE_DIR", str(tmp_path / "store"))
    import telemetry_index

    importlib.reload(telemetry_index)
    return telemetry_index


def varint(value: int) -> bytes:
    out = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        out.append(byte | (0x80 if value else 0))
        if not value:
            return bytes(out)


def message(latitude: float, sequence: int) -> bytes:
    return b"".join(
        [
            varint(3 << 3) + varint(sequence),
            varint(4 << 3 | 5) + struct.pack("<f", 8.0),
            varint(11 << 3 | 1) + struct.pack("<d", latitude),
            varint(12 << 3 | 1) + struct.pack("<d", 0.0 if latitude == 0.0 else -9.5),
        ]
    )


def nal(payload: bytes) -> bytes:
    body = b"\x42\x69" + payload
    return b"\x06\x05" + bytes([len(body)]) + body + b"\x80"


def write_clip(path: Path, latitudes: list[float]) -> list[int]:
    units = []
    offsets = []
    position = 24 + 8
    for sequence, latitude in enumerate(latitudes):
        unit = nal(message(latitude, sequence))
        offsets.append(position)
        data = struct.pack(">I", len(unit)) + unit
        units.append(data)
        position += len(data)
    mdat = b"".join(units)
    path.write_bytes(
        struct.pack(">I4s", 24, b"ftyp")
        + b"isom\x00\x00\x00\x00"
        + b"isom"
        + b"isom"
        + struct.pack(">I4s", 8 + len(mdat), b"mdat")
        + mdat
    )
    return offsets


def test_index_clip_downsamples_and_marks_archived(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    event = tmp_path / "TeslaCam/SavedClips/2026-11-07_10-00-00"
    event.mkdir(parents=True)
    clip = event / "2026-11-07_10-00-00-front.mp4"
    offsets = write_clip(clip, [53.1, 53.1001, 53.1002])
    monkeypatch.setattr(
        telemetry,
        "probe_and_packet_times",
        lambda path: (
            offsets,
            [0.0, 0.5, 1.1],
        ),
    )

    telemetry.index_clip(event)
    clips = telemetry.load_index()
    assert len(clips) == 1
    assert clips[0]["id"] == f"SavedClips/2026-11-07_10-00-00/{clip.name}"
    assert clips[0]["source"] == clips[0]["id"]
    assert clips[0]["recorded_at"] == "2026-11-07T10:00:00"
    assert clips[0]["samples"] == 2
    assert clips[0]["state"] == "available"
    assert clips[0]["point_words"] == 15
    assert telemetry.track_path(clips[0]["id"]).stat().st_size == 120

    file_list = tmp_path / "files.txt"
    file_list.write_text(f"SavedClips/{event.name}/{clip.name}\n")
    clip.unlink()
    telemetry.mark_archived(tmp_path / "TeslaCam", file_list)
    assert telemetry.load_index()[0]["state"] == "archived"


def test_saved_event_indexes_each_minute_independently(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    event = tmp_path / "TeslaCam/SavedClips/2026-11-07_10-00-00"
    event.mkdir(parents=True)
    first = event / "2026-11-07_10-00-00-front.mp4"
    second = event / "2026-11-07_10-01-00-front.mp4"
    for clip in (first, second):
        write_clip(clip, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))

    telemetry.index_clip(event)

    clips = {clip["id"]: clip for clip in telemetry.load_index()}
    assert set(clips) == {
        f"SavedClips/{event.name}/{first.name}",
        f"SavedClips/{event.name}/{second.name}",
    }
    file_list = tmp_path / "files.txt"
    file_list.write_text(f"SavedClips/{event.name}/{first.name}\n")
    first.unlink()
    telemetry.mark_archived(tmp_path / "TeslaCam", file_list)
    clips = {clip["id"]: clip for clip in telemetry.load_index()}
    assert clips[f"SavedClips/{event.name}/{first.name}"]["state"] == "archived"
    assert clips[f"SavedClips/{event.name}/{second.name}"]["state"] == "available"


def test_index_clip_rejects_missing_gps(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    event = tmp_path / "TeslaCam/SavedClips/2026-11-07_10-00-00"
    event.mkdir(parents=True)
    clip = event / "2026-11-07_10-00-00-front.mp4"
    offsets = write_clip(clip, [0.0])
    monkeypatch.setattr(
        telemetry,
        "probe_and_packet_times",
        lambda path: (offsets, [0.0]),
    )

    telemetry.index_clip(event)
    stored = telemetry.load_index()
    assert stored[0]["state"] == "no-gps"
    calls = {"count": 0}

    def fail_again(path: Path) -> tuple[list[int], list[float]]:
        calls["count"] += 1
        return offsets, [0.0]

    monkeypatch.setattr(telemetry, "probe_and_packet_times", fail_again)
    telemetry.index_clip(event)
    assert calls["count"] == 0


def test_mp4_sample_table_decodes_offsets_and_presentation_times(telemetry: Any) -> None:
    from io import BytesIO

    def atom(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I4s", len(data) + 8, kind) + data

    stts = atom(b"stts", b"\0" * 4 + struct.pack(">I", 2) + struct.pack(">IIII", 2, 1000, 1, 500))
    stsc = atom(b"stsc", b"\0" * 4 + struct.pack(">I", 1) + struct.pack(">III", 1, 3, 1))
    stsz = atom(b"stsz", b"\0" * 4 + struct.pack(">II", 0, 3) + struct.pack(">III", 10, 20, 30))
    stco = atom(b"stco", b"\0" * 4 + struct.pack(">I", 1) + struct.pack(">I", 100))
    table = stts + stsc + stsz + stco
    positions, times = telemetry._sample_table(BytesIO(table), 0, len(table), 1000)

    assert positions == [100, 110, 130]
    assert times == [0.0, 1.0, 2.0]


def test_index_recent_clip_and_mark_archived(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    clip = recent / "2026-11-07_10-00-00-front.mp4"
    offsets = write_clip(clip, [53.1])
    monkeypatch.setattr(
        telemetry,
        "probe_and_packet_times",
        lambda path: (offsets, [0.0]),
    )

    telemetry.index_clip(clip)
    stored = telemetry.load_index()[0]
    assert stored["id"] == "RecentClips/2026-11-07/2026-11-07_10-00-00-front.mp4"
    assert stored["source"] == stored["id"]

    file_list = tmp_path / "recent-files.txt"
    file_list.write_text(stored["id"] + "\n")
    clip.unlink()
    telemetry.mark_archived(tmp_path / "TeslaCam", file_list)
    assert telemetry.load_index()[0]["state"] == "archived"


def test_mark_archived_counts_any_camera(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """With only back and left archived, the minute's track still shows archived."""
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    front = recent / "2026-11-07_10-00-00-front.mp4"
    offsets = write_clip(front, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: (offsets, [0.0]))
    telemetry.index_clip(front)

    file_list = tmp_path / "recent-files.txt"
    file_list.write_text(
        "RecentClips/2026-11-07/2026-11-07_10-00-00-back.mp4\n"
        "RecentClips/2026-11-07/2026-11-07_10-00-00-left_repeater.mp4\n"
    )
    telemetry.mark_archived(tmp_path / "TeslaCam", file_list)
    assert telemetry.load_index()[0]["state"] == "archived"


def test_mark_listed_trusts_the_archived_list(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Clips indexed after the cycle that copied them are still marked archived."""
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    front = recent / "2026-11-07_10-00-00-front.mp4"
    offsets = write_clip(front, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: (offsets, [0.0]))
    telemetry.index_clip(front)

    file_list = tmp_path / "archived.txt"
    file_list.write_text("RecentClips/2026-11-07/2026-11-07_10-00-00-back.mp4\n")
    telemetry.mark_archived(None, file_list)
    assert front.exists()
    assert telemetry.load_index()[0]["state"] == "archived"


def test_saved_event_skips_minute_without_gps_and_indexes_valid_minutes(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    event = tmp_path / "TeslaCam/SavedClips/2026-11-07_10-00-00"
    event.mkdir(parents=True)
    missing_gps = event / "2026-11-07_10-00-00-front.mp4"
    valid_gps = event / "2026-11-07_10-01-00-front.mp4"
    write_clip(missing_gps, [0.0])
    write_clip(valid_gps, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))

    telemetry.index_clip(event)

    stored = {clip["id"]: clip["state"] for clip in telemetry.load_index()}
    assert stored[f"SavedClips/{event.name}/{missing_gps.name}"] == "no-gps"
    assert stored[f"SavedClips/{event.name}/{valid_gps.name}"] == "available"


def test_recent_clip_symlink_to_sentry_is_rejected(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    sentry_file = (
        tmp_path
        / "snapshots/snap-000001/TeslaCam/SentryClips/2026-11-07_10-00-00/2026-11-07_10-00-00-front.mp4"
    )
    sentry_file.parent.mkdir(parents=True)
    write_clip(sentry_file, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))

    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    symlink = recent / "2026-11-07_10-00-00-front.mp4"
    symlink.symlink_to(sentry_file)

    with pytest.raises(ValueError, match="SentryClips or SavedClips"):
        telemetry.index_clip(symlink)
    assert telemetry.load_index() == []


def test_non_finite_speed_is_not_indexed(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    clip = tmp_path / "2026-11-07_10-00-00-front.mp4"
    clip.write_bytes(b"")
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([0, 1], [0.0, 1.0]))

    def samples(path: Path) -> list[tuple[int, dict[str, float]]]:
        return [
            (0, {"latitude_deg": 53.1, "longitude_deg": -9.5, "vehicle_speed_mps": float("nan")}),
            (1, {"latitude_deg": 53.2, "longitude_deg": -9.5, "vehicle_speed_mps": 4.0}),
        ]

    monkeypatch.setattr(telemetry, "iter_sei", samples)
    track = telemetry.extract_track(clip)
    assert len(track) == 1
    assert track[0][1] == 53.2


def test_corrupt_index_is_not_replaced(telemetry: Any, tmp_path: Path) -> None:
    telemetry.INDEX_FILE.parent.mkdir(parents=True, exist_ok=True)
    telemetry.INDEX_FILE.write_text("{")
    track = telemetry.TRACK_DIR / "orphan.f32"
    telemetry.TRACK_DIR.mkdir(parents=True)
    track.write_bytes(b"keep")
    with pytest.raises(telemetry.CorruptIndexError):
        telemetry.load_index()
    assert telemetry.INDEX_FILE.read_text() == "{"
    assert track.read_bytes() == b"keep"


def test_prune_compares_naive_filename_stamps_as_local_time(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    zone = ZoneInfo("Pacific/Auckland")
    monkeypatch.setattr(telemetry, "local_timezone", lambda: zone)

    class Clock:
        @staticmethod
        def now(tz: Any = None) -> datetime:
            moment = datetime(2026, 11, 8, 12, 0, tzinfo=UTC)
            if tz is None:
                return moment.astimezone(zone).replace(tzinfo=None)
            return moment.astimezone(tz)

        fromisoformat = staticmethod(datetime.fromisoformat)

    monkeypatch.setattr(telemetry, "datetime", Clock)
    monkeypatch.setattr(telemetry, "retention_days", lambda: 1)
    # 10:00 in Auckland is 21:00 the previous day UTC, before the one-day cutoff.
    clips = [{"id": "RecentClips/2026-11-07/clip.mp4", "recorded_at": "2026-11-07T10:00:00"}]
    assert telemetry.prune(clips) == []


def test_index_batch_extracts_each_clip_once_and_writes_index_once(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: Any
) -> None:
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    clips = [recent / f"2026-11-07_10-0{minute}-00-front.mp4" for minute in range(3)]
    for clip in clips:
        write_clip(clip, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    extracted: list[str] = []
    real_extract = telemetry.extract_track

    def counting_extract(path: Path) -> Any:
        extracted.append(path.name)
        return real_extract(path)

    monkeypatch.setattr(telemetry, "extract_track", counting_extract)
    saves: list[int] = []
    real_save = telemetry.save_index

    def counting_save(clips: list[dict[str, Any]], days: set[str] | None = None) -> None:
        saves.append(len(clips))
        real_save(clips, days)

    monkeypatch.setattr(telemetry, "save_index", counting_save)
    lines = [f"RecentClips/2026-11-07/{clip.name}\t{clip}\n" for clip in clips]

    assert telemetry.index_batch(lines, []) == 0
    assert extracted == [clip.name for clip in clips]
    assert saves == [3]
    assert {c["state"] for c in telemetry.load_index()} == {"available"}

    # Second cycle: nothing new, so no video is read and clips.json is not rewritten.
    extracted.clear()
    saves.clear()
    assert telemetry.index_batch(lines, []) == 0
    assert extracted == []
    assert saves == []
    assert "3 clips checked, 0 new, 0 failed" in capsys.readouterr().out


def test_index_batch_reports_failures_and_keeps_going(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: Any
) -> None:
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    good = recent / "2026-11-07_10-00-00-front.mp4"
    write_clip(good, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    empty_event = tmp_path / "TeslaCam/SavedClips/2026-11-07_09-00-00"
    empty_event.mkdir(parents=True)
    lines = [
        f"SavedClips/{empty_event.name}\t{empty_event}\n",
        f"RecentClips/2026-11-07/{good.name}\t{good}\n",
    ]

    assert telemetry.index_batch(lines, []) == 0
    out = capsys.readouterr().out
    assert f"SavedClips/{empty_event.name}: SavedClips event has no front-camera MP4" in out
    assert "2 clips checked, 1 new, 1 failed" in out
    assert [c["state"] for c in telemetry.load_index()] == ["available"]


def test_index_batch_stops_when_archive_becomes_unreachable(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: Any
) -> None:
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    clips = [recent / f"2026-11-07_10-0{minute}-00-front.mp4" for minute in range(2)]
    for clip in clips:
        write_clip(clip, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    lines = [f"RecentClips/2026-11-07/{clip.name}\t{clip}\n" for clip in clips]

    # check_interval 0: the check runs before every clip. "false" always fails.
    status = telemetry.index_batch(lines, ["false"], check_interval=0)
    assert status == telemetry.UNREACHABLE_EXIT
    assert "archive unreachable, deferring 2 clips" in capsys.readouterr().out
    assert telemetry.load_index() == []

    assert telemetry.index_batch(lines, ["true"], check_interval=0) == 0
    assert len(telemetry.load_index()) == 2


def test_index_batch_budget_leaves_clips_for_the_next_run(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: Any
) -> None:
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    clips = [recent / f"2026-11-07_10-0{minute}-00-front.mp4" for minute in range(3)]
    for clip in clips:
        write_clip(clip, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    lines = [f"RecentClips/2026-11-07/{clip.name}\t{clip}\n" for clip in clips]

    assert telemetry.index_batch(lines, [], budget=0) == 0
    assert "time budget of 0s used, 3 clips left" in capsys.readouterr().out
    assert telemetry.load_index() == []

    assert telemetry.index_batch(lines, [], budget=600) == 0
    assert len(telemetry.load_index()) == 3


def test_index_batch_reads_oldest_clips_first(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """The oldest clips are the first to go when snapshots are released."""
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    clips = [recent / f"2026-11-07_10-0{minute}-00-front.mp4" for minute in range(3)]
    for clip in clips:
        write_clip(clip, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    order: list[str] = []
    real_extract = telemetry.extract_track

    def recording_extract(path: Path) -> Any:
        order.append(path.name)
        return real_extract(path)

    monkeypatch.setattr(telemetry, "extract_track", recording_extract)
    lines = [f"RecentClips/2026-11-07/{clip.name}\t{clip}\n" for clip in reversed(clips)]

    assert telemetry.index_batch(lines, []) == 0
    assert order == [clip.name for clip in clips]


def test_second_indexer_skips_while_the_store_is_locked(telemetry: Any, tmp_path: Path) -> None:
    import fcntl
    import os
    import subprocess

    env = {**os.environ, "TELEMETRY_STORE_DIR": str(telemetry.STORE_DIR)}
    command = [sys.executable, str(RUN_DIR / "telemetry_index.py"), "index-batch", "--no-wait"]
    with telemetry.store_lock() as locked:
        assert locked
        busy = subprocess.run(  # noqa: S603 — repository script only
            command, input="", text=True, capture_output=True, env=env, check=False
        )
    assert busy.returncode == telemetry.BUSY_EXIT
    assert "another indexer is running; skipped" in busy.stdout

    free = subprocess.run(  # noqa: S603 — repository script only
        command, input="", text=True, capture_output=True, env=env, check=False
    )
    assert free.returncode == 0
    assert "0 clips checked" in free.stdout

    # Without --no-wait a second indexer waits for the lock instead of skipping.
    with open(telemetry.LOCK_FILE, "a") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        waiting = subprocess.Popen(  # noqa: S603 — repository script only
            command[:-1], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, env=env
        )
        with pytest.raises(subprocess.TimeoutExpired):
            waiting.communicate(input="", timeout=1)
        fcntl.flock(handle, fcntl.LOCK_UN)
    out, _ = waiting.communicate(timeout=10)
    assert waiting.returncode == 0
    assert "0 clips checked" in out


def test_reindexing_keeps_archived_state(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    """Indexing after every snapshot must not undo "archived" while the clip is on the Pi."""
    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    front = recent / "2026-11-07_10-00-00-front.mp4"
    offsets = write_clip(front, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: (offsets, [0.0]))
    clip_id = f"RecentClips/2026-11-07/{front.name}"
    lines = [f"{clip_id}\t{front}\n"]
    assert telemetry.index_batch(lines, []) == 0

    file_list = tmp_path / "archived.txt"
    file_list.write_text(clip_id + "\n")
    telemetry.mark_archived(None, file_list)
    saves: list[int] = []
    real_save = telemetry.save_index
    monkeypatch.setattr(
        telemetry, "save_index", lambda clips, days=None: (saves.append(1), real_save(clips, days))
    )

    assert telemetry.index_batch(lines, []) == 0
    assert telemetry.load_index()[0]["state"] == "archived"
    assert saves == []  # nothing changed, so clips.json is not rewritten


def test_scan_tree_finds_saved_events_and_recent_fronts(telemetry: Any, tmp_path: Path) -> None:
    """Sentry is excluded and telemetry does not depend on which categories are archived."""
    root = tmp_path / "TeslaCam"
    saved = root / "SavedClips/2026-11-07_12-00-00"
    saved.mkdir(parents=True)
    (saved / "2026-11-07_11-59-00-front.mp4").touch()
    (saved / "2026-11-07_11-59-00-back.mp4").touch()
    (saved / "2026-11-07_11-58-00-front.mp4").touch()
    (root / "SavedClips/2026-11-07_13-00-00").mkdir()  # no front camera: skipped
    sentry = root / "SentryClips/2026-11-07_12-30-00"
    sentry.mkdir(parents=True)
    sentry_front = sentry / "2026-11-07_12-29-00-front.mp4"
    sentry_front.touch()
    recent = root / "RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    real = recent / "2026-11-07_10-00-00-front.mp4"
    real.touch()
    (recent / "2026-11-07_10-00-00-back.mp4").touch()
    (recent / "2026-11-07_12-29-00-front.mp4").symlink_to(sentry_front)  # Sentry alias
    (recent / "2026-11-07_09-00-00-front.mp4").symlink_to(tmp_path / "gone.mp4")  # released
    # A released link is listed (checking it would mount its snapshot) and skipped
    # quietly when indexed.
    index = telemetry.ClipIndex()
    telemetry.index_clip(recent / "2026-11-07_09-00-00-front.mp4", index)
    assert index.clips == {}

    released = recent / "2026-11-07_09-00-00-front.mp4"
    assert telemetry.scan_tree(root) == [
        ("SavedClips/2026-11-07_12-00-00", saved),
        ("RecentClips/2026-11-07/2026-11-07_09-00-00-front.mp4", released),
        ("RecentClips/2026-11-07/2026-11-07_10-00-00-front.mp4", real),
    ]
    assert telemetry.scan_tree(tmp_path / "missing") == []


def test_clips_older_than_retention_are_not_read(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: Any
) -> None:
    """A Pi holding months-old clips must not extract them on every run only to prune them."""
    monkeypatch.setenv("TELEMETRY_RETENTION_DAYS", "30")
    old_day = (datetime.now() - timedelta(days=40)).strftime("%Y-%m-%d")
    new_day = (datetime.now() - timedelta(days=1)).strftime("%Y-%m-%d")
    recent = tmp_path / "TeslaCam/RecentClips"
    old = recent / old_day / f"{old_day}_10-00-00-front.mp4"
    new = recent / new_day / f"{new_day}_10-00-00-front.mp4"
    saved = tmp_path / f"TeslaCam/SavedClips/{old_day}_11-00-00"
    for clip in (old, new, saved / f"{old_day}_10-59-00-front.mp4"):
        clip.parent.mkdir(parents=True, exist_ok=True)
        write_clip(clip, [53.1])
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    extracted: list[str] = []
    real_extract = telemetry.extract_track

    def recording_extract(path: Path) -> Any:
        extracted.append(path.name)
        return real_extract(path)

    monkeypatch.setattr(telemetry, "extract_track", recording_extract)
    lines = [f"{clip_id}\t{path}\n" for clip_id, path in telemetry.scan_tree(tmp_path / "TeslaCam")]

    assert telemetry.index_batch(lines, []) == 0
    assert extracted == [new.name]
    out = capsys.readouterr().out
    assert "3 clips checked, 1 new, 0 failed" in out  # the old Saved event is not a failure
    assert "(2 older than 30 days skipped)" in out


def test_index_batch_scan_option(telemetry: Any, tmp_path: Path) -> None:
    import os
    import subprocess

    recent = tmp_path / "TeslaCam/RecentClips/2026-11-07"
    recent.mkdir(parents=True)
    (recent / "2026-11-07_10-00-00-front.mp4").write_bytes(b"not a video")
    env = {
        **os.environ,
        "TELEMETRY_STORE_DIR": str(telemetry.STORE_DIR),
        "TELEMETRY_RETENTION_DAYS": "3650",
    }
    result = subprocess.run(  # noqa: S603 — repository script only
        [
            sys.executable,
            str(RUN_DIR / "telemetry_index.py"),
            "index-batch",
            "--scan",
            str(tmp_path / "TeslaCam"),
        ],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        env=env,
        check=False,
    )
    assert result.returncode == 0, result.stderr
    assert "1 clips checked" in result.stdout


def _recent_clip(tmp_path: Path, day: str, minute: int, lats: list[float]) -> tuple[str, Path]:
    folder = tmp_path / "TeslaCam/RecentClips" / day
    folder.mkdir(parents=True, exist_ok=True)
    clip = folder / f"{day}_10-{minute:02d}-00-front.mp4"
    write_clip(clip, lats)
    return f"RecentClips/{day}/{clip.name}", clip


def _days_ago(days: int) -> str:
    return (datetime.now() - timedelta(days=days)).strftime("%Y-%m-%d")


def test_clips_are_stored_one_file_per_day(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    first, second = _days_ago(2), _days_ago(1)
    lines = [
        f"{clip_id}\t{path}\n"
        for clip_id, path in (
            _recent_clip(tmp_path, first, 0, [53.1]),
            _recent_clip(tmp_path, first, 1, [53.1]),
            _recent_clip(tmp_path, second, 0, [53.1]),
        )
    ]
    assert telemetry.index_batch(lines, []) == 0
    assert sorted(path.name for path in telemetry.DAYS_DIR.glob("*.json")) == [
        f"{first}.json",
        f"{second}.json",
    ]
    assert len(json.loads((telemetry.DAYS_DIR / f"{first}.json").read_text())["clips"]) == 2
    assert not telemetry.INDEX_FILE.exists()


def test_legacy_clips_json_is_split_into_days(telemetry: Any) -> None:
    clips = [
        {"id": "RecentClips/a/2026-11-10_10-00-00-front.mp4", "recorded_at": "2026-11-10T10:00:00"},
        {"id": "RecentClips/a/2026-11-11_10-00-00-front.mp4", "recorded_at": "2026-11-11T10:00:00"},
        {"id": "SavedClips/2026-11-11_12-00-00", "recorded_at": "2026-11-11T11:59:00"},
    ]
    telemetry.STORE_DIR.mkdir(parents=True)
    telemetry.INDEX_FILE.write_text(json.dumps({"version": 1, "clips": clips}))
    loaded = telemetry.load_index()
    assert sorted(c["id"] for c in loaded) == sorted(c["id"] for c in clips)
    assert not telemetry.INDEX_FILE.exists()
    assert {p.name for p in telemetry.DAYS_DIR.glob("*.json")} == {
        "2026-11-10.json",
        "2026-11-11.json",
    }
    # A run interrupted after the day files were written just writes them again.
    telemetry.INDEX_FILE.write_text(json.dumps({"version": 1, "clips": clips[:1]}))
    assert len(telemetry.load_index()) == 3


def test_retention_removes_whole_days(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    monkeypatch.setenv("TELEMETRY_RETENTION_DAYS", "10")
    old_day, new_day = _days_ago(5), _days_ago(1)
    lines = [
        f"{clip_id}\t{path}\n"
        for clip_id, path in (
            _recent_clip(tmp_path, old_day, 0, [53.1]),
            _recent_clip(tmp_path, new_day, 0, [53.1]),
        )
    ]
    assert telemetry.index_batch(lines, []) == 0
    assert telemetry.compact() == [old_day, new_day]
    monkeypatch.setenv("TELEMETRY_RETENTION_DAYS", "3")
    telemetry.save_index(telemetry.prune(telemetry.load_index()))
    assert [c["recorded_at"][:10] for c in telemetry.load_index()] == [new_day]
    assert not list(telemetry.DAYS_DIR.glob(f"{old_day}*"))


def test_compaction_packs_finished_days_and_keeps_tracks_identical(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    yesterday, today = _days_ago(1), _days_ago(0)
    clips = [
        _recent_clip(tmp_path, yesterday, 0, [53.1]),
        _recent_clip(tmp_path, yesterday, 1, [53.2]),
        _recent_clip(tmp_path, today, 0, [53.3]),
    ]
    lines = [f"{clip_id}\t{path}\n" for clip_id, path in clips]
    assert telemetry.index_batch(lines, []) == 0
    before = {c["id"]: telemetry.read_track_bytes(c) for c in telemetry.load_index()}

    # Today's clip was just recorded (the clip's own time is 10:00 today): only
    # yesterday is finished, unless an hour has passed since the last clip.
    now = datetime.fromisoformat(f"{today}T10:30:00")
    assert telemetry.compact(now) == [yesterday]
    stored = {c["id"]: c for c in telemetry.load_index()}
    assert all(stored[clip_id].get("pack") for clip_id, _ in clips[:2])
    assert "pack" not in stored[clips[2][0]]
    assert {c["id"]: telemetry.read_track_bytes(c) for c in stored.values()} == before
    assert len(list(telemetry.TRACK_DIR.glob("*.f32"))) == 1  # only today's raw track
    assert len(list(telemetry.DAYS_DIR.glob("*.f32"))) == 1
    line = (telemetry.DAYS_DIR / f"{stored[clips[0][0]]['pack']['name']}.line").read_bytes()
    assert len(line) == 8 * sum(stored[c]["pack"]["line_count"] for c, _ in clips[:2])

    # Nothing new: a second run changes nothing.
    assert telemetry.compact(now) == []
    later = datetime.fromisoformat(f"{today}T11:30:00")
    assert telemetry.compact(later) == [today]
    assert not list(telemetry.TRACK_DIR.glob("*.f32"))

    # Re-indexing packed clips reads no video.
    monkeypatch.setattr(
        telemetry, "extract_track", lambda path: pytest.fail("re-read " + path.name)
    )
    assert telemetry.index_batch(lines, []) == 0


def test_interrupted_compaction_leaves_every_clip_readable(
    telemetry: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setattr(telemetry, "probe_and_packet_times", lambda path: ([32], [0.0]))
    day = _days_ago(1)
    clip_id, path = _recent_clip(tmp_path, day, 0, [53.1])
    assert telemetry.index_batch([f"{clip_id}\t{path}\n"], []) == 0
    raw = telemetry.read_track_bytes(telemetry.load_index()[0])

    # Power lost after the new pack was written but before the day file switched.
    real_save = telemetry.save_index

    def lost_power(clips: list[dict[str, Any]], days: set[str] | None = None) -> None:
        raise KeyboardInterrupt

    monkeypatch.setattr(telemetry, "save_index", lost_power)
    with pytest.raises(KeyboardInterrupt):
        telemetry.compact()
    monkeypatch.setattr(telemetry, "save_index", real_save)
    entry = telemetry.load_index()[0]
    assert "pack" not in entry and telemetry.read_track_bytes(entry) == raw

    assert telemetry.compact() == [day]  # the next run finishes and removes the orphan
    entry = telemetry.load_index()[0]
    assert telemetry.read_track_bytes(entry) == raw
    assert len(list(telemetry.DAYS_DIR.glob("*.f32"))) == 1


def test_simplify_line_keeps_shape_and_regular_points(telemetry: Any) -> None:
    straight = [(float(t), 53.0 + t * 0.0001, 18.0, 0.0) + (0.0,) * 11 for t in range(120)]
    line = telemetry.simplify_line(straight)
    assert line[0] == (straight[0][1], straight[0][2])
    assert line[-1] == (straight[-1][1], straight[-1][2])
    assert 4 <= len(line) <= 6  # ends plus one point every 30 s
    corner = [
        (float(t), 53.0 + min(t, 30) * 0.0001, 18.0 + max(t - 30, 0) * 0.0001) + (0.0,) * 12
        for t in range(60)
    ]
    assert (corner[30][1], corner[30][2]) in telemetry.simplify_line(corner)
