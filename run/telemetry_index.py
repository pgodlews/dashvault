#!/usr/bin/env python3
"""Extract and retain lightweight Tesla SEI tracks before clips are archived."""

from __future__ import annotations

import argparse
import bisect
import contextlib
import fcntl
import hashlib
import json
import math
import os
import re
import struct
import subprocess
import sys
import tempfile
import time
from collections.abc import Iterable, Iterator
from datetime import UTC, datetime, timedelta, tzinfo
from pathlib import Path
from typing import Any, BinaryIO

STORE_DIR = Path(os.environ.get("TELEMETRY_STORE_DIR", "/mutable/telemetry"))
# Before 2026-11-12 every clip was in one clips.json; it is split into DAYS_DIR
# on first use (migrate_legacy_index).
INDEX_FILE = STORE_DIR / "clips.json"
# days/<YYYY-MM-DD>.json: the clips recorded that local day. Once a day is
# finished, compact() packs its tracks into days/<day>.<generation>.f32 (15
# floats per point, clips back to back) and a simplified map line into
# days/<day>.<generation>.line (lat, lon float32 pairs); each clip entry then
# has a "pack" reference and its raw tracks/<sha256>.f32 is deleted.
DAYS_DIR = STORE_DIR / "days"
TRACK_DIR = STORE_DIR / "tracks"
POINT_FLOATS = 15
POINT_BYTES = POINT_FLOATS * 4
DEFAULT_RETENTION_DAYS = 30

_FIELDS = {
    1: ("version", 0),
    2: ("gear_state", 0),
    3: ("frame_seq_no", 0),
    4: ("vehicle_speed_mps", 5),
    5: ("accelerator_pedal_position", 5),
    6: ("steering_wheel_angle", 5),
    7: ("blinker_on_left", 0),
    8: ("blinker_on_right", 0),
    9: ("brake_applied", 0),
    10: ("autopilot_state", 0),
    11: ("latitude_deg", 1),
    12: ("longitude_deg", 1),
    13: ("heading_deg", 1),
    14: ("linear_acceleration_mps2_x", 1),
    15: ("linear_acceleration_mps2_y", 1),
    16: ("linear_acceleration_mps2_z", 1),
}


def _varint(data: bytes, i: int) -> tuple[int, int]:
    value = shift = 0
    while True:
        byte = data[i]
        i += 1
        value |= (byte & 0x7F) << shift
        if not byte & 0x80:
            return value, i
        shift += 7
        if shift > 63:
            raise ValueError("Varint too long")


def decode_sei(data: bytes) -> dict[str, float | int]:
    out: dict[str, float | int] = {}
    i = 0
    try:
        while i < len(data):
            key, i = _varint(data, i)
            field, wire = key >> 3, key & 7
            definition = _FIELDS.get(field)
            if definition is not None and wire != definition[1]:
                raise ValueError(f"Unexpected wire type for {definition[0]}")
            if wire == 0:
                value, i = _varint(data, i)
            elif wire == 1:
                (value,) = struct.unpack_from("<d", data, i)
                i += 8
            elif wire == 5:
                (value,) = struct.unpack_from("<f", data, i)
                i += 4
            elif wire == 2:
                length, i = _varint(data, i)
                i += length
                continue
            else:
                raise ValueError(f"Unsupported protobuf wire type {wire}")
            if definition is not None:
                out[definition[0]] = value
    except (IndexError, struct.error) as error:
        raise ValueError("Truncated SEI metadata") from error
    return out


def _unescape(data: bytes) -> bytes:
    out = bytearray()
    zeros = 0
    for byte in data:
        if zeros >= 2 and byte == 0x03:
            zeros = 0
            continue
        out.append(byte)
        zeros = zeros + 1 if byte == 0 else 0
    return bytes(out)


def _sei_payloads(nal: bytes) -> Iterator[bytes]:
    if not nal or nal[0] & 0x1F != 6:
        return
    body = _unescape(nal[1:])
    i = 0
    while i < len(body):
        if body[i] == 0x80 and not any(body[i + 1 :]):
            return
        values = []
        for _ in range(2):
            value = 0
            while i < len(body) and body[i] == 0xFF:
                value += 255
                i += 1
            if i == len(body):
                return
            values.append(value + body[i])
            i += 1
        kind, size = values
        end = i + size
        if end >= len(body):
            return
        if kind == 5:
            start = i
            while i < end and body[i] == 0x42:
                i += 1
            if i > start and i + 1 < end and body[i] == 0x69:
                yield body[i + 1 : end]
        i = end


def _mdat_range(fp: BinaryIO) -> tuple[int, int]:
    fp.seek(0, 2)
    end = fp.tell()
    fp.seek(0)
    while fp.tell() + 8 <= end:
        start = fp.tell()
        size, kind = struct.unpack(">I4s", fp.read(8))
        header = 8
        if size == 1:
            size, header = struct.unpack(">Q", fp.read(8))[0], 16
        elif size == 0:
            size = end - start
        if size < header or size > end - start:
            raise ValueError("Invalid or truncated MP4 atom")
        if kind == b"mdat":
            return start + header, start + size
        fp.seek(start + size)
    raise ValueError("MP4 has no mdat atom")


def iter_sei(video_path: Path) -> Iterator[tuple[int, dict[str, float | int]]]:
    with video_path.open("rb") as fp:
        lo, hi = _mdat_range(fp)
        fp.seek(lo)
        while fp.tell() < hi:
            offset = fp.tell()
            if hi - offset < 4:
                raise ValueError("Truncated MP4 NAL length")
            (size,) = struct.unpack(">I", fp.read(4))
            if size < 1 or offset + 4 + size > hi:
                raise ValueError("Invalid or truncated MP4 NAL")
            head = fp.read(1)
            if head[0] & 0x1F == 6:
                for payload in _sei_payloads(head + fp.read(size - 1)):
                    with contextlib.suppress(ValueError):
                        yield offset, decode_sei(payload)
            fp.seek(offset + 4 + size)


def _atoms(fp: BinaryIO, start: int, end: int) -> Iterator[tuple[bytes, int, int]]:
    fp.seek(start)
    while start + 8 <= end:
        size, kind = struct.unpack(">I4s", fp.read(8))
        header = 8
        if size == 1:
            size, header = struct.unpack(">Q", fp.read(8))[0], 16
        elif size == 0:
            size = end - start
        if size < header or start + size > end:
            raise ValueError("Invalid or truncated MP4 atom")
        yield kind, start + header, start + size
        start += size
        fp.seek(start)


def _child(fp: BinaryIO, start: int, end: int, kind: bytes) -> tuple[int, int]:
    for child_kind, child_start, child_end in _atoms(fp, start, end):
        if child_kind == kind:
            return child_start, child_end
    raise ValueError(f"MP4 atom {kind.decode(errors='replace')} not found")


def _child_data(fp: BinaryIO, start: int, end: int, kind: bytes) -> bytes:
    child_start, child_end = _child(fp, start, end, kind)
    fp.seek(child_start)
    return fp.read(child_end - child_start)


def _sample_table(
    fp: BinaryIO, start: int, end: int, timescale: int
) -> tuple[list[int], list[float]]:
    stts = _child_data(fp, start, end, b"stts")
    stsc = _child_data(fp, start, end, b"stsc")
    stsz = _child_data(fp, start, end, b"stsz")
    chunk_atom = (
        _child_data(fp, start, end, b"stco")
        if _has_child(fp, start, end, b"stco")
        else _child_data(fp, start, end, b"co64")
    )
    constant_size, sample_count = struct.unpack_from(">II", stsz, 4)
    sample_sizes = (
        [constant_size] * sample_count
        if constant_size
        else list(struct.unpack_from(f">{sample_count}I", stsz, 12))
    )
    chunk_count = struct.unpack_from(">I", chunk_atom, 4)[0]
    chunk_width = 4 if len(chunk_atom) == 8 + chunk_count * 4 else 8
    chunk_offsets = [
        value[0] for value in struct.iter_unpack(">I" if chunk_width == 4 else ">Q", chunk_atom[8:])
    ]
    decoding_times = []
    for count, delta in struct.iter_unpack(">II", stts[8:]):
        decoding_times.extend([delta] * count)
    if len(decoding_times) != sample_count:
        raise ValueError("MP4 sample timing count mismatch")
    composition_offsets = [0] * sample_count
    if _has_child(fp, start, end, b"ctts"):
        ctts = _child_data(fp, start, end, b"ctts")
        version = ctts[0]
        unpacked = []
        for count, offset in struct.iter_unpack(">II", ctts[8:]):
            unpacked.extend([offset] * count)
        if len(unpacked) != sample_count:
            raise ValueError("MP4 composition timing count mismatch")
        if version == 1:
            unpacked = [offset - (1 << 32) if offset & (1 << 31) else offset for offset in unpacked]
        composition_offsets = unpacked
    positions = []
    sample_index = 0
    stsc_rows = list(struct.iter_unpack(">III", stsc[8:]))
    for chunk_number, chunk_offset in enumerate(chunk_offsets, 1):
        row = stsc_rows[0]
        for candidate in stsc_rows:
            if candidate[0] > chunk_number:
                break
            row = candidate
        within_chunk = 0
        for _ in range(row[1]):
            if sample_index >= sample_count:
                break
            positions.append(chunk_offset + within_chunk)
            within_chunk += sample_sizes[sample_index]
            sample_index += 1
    if sample_index != sample_count:
        raise ValueError("MP4 sample offset count mismatch")
    elapsed = 0
    rows = []
    for position, decoding_delta, composition_offset in zip(
        positions, decoding_times, composition_offsets, strict=True
    ):
        rows.append((position, (elapsed + composition_offset) / timescale))
        elapsed += decoding_delta
    rows.sort()
    return [position for position, _ in rows], [seconds for _, seconds in rows]


def _has_child(fp: BinaryIO, start: int, end: int, kind: bytes) -> bool:
    return any(child_kind == kind for child_kind, _, _ in _atoms(fp, start, end))


def probe_and_packet_times(video_path: Path) -> tuple[list[int], list[float]]:
    with video_path.open("rb") as fp:
        fp.seek(0, 2)
        file_end = fp.tell()
        moov_start, moov_end = _child(fp, 0, file_end, b"moov")
        for kind, trak_start, trak_end in _atoms(fp, moov_start, moov_end):
            if kind != b"trak":
                continue
            mdia_start, mdia_end = _child(fp, trak_start, trak_end, b"mdia")
            hdlr = _child_data(fp, mdia_start, mdia_end, b"hdlr")
            if hdlr[8:12] != b"vide":
                continue
            mdhd = _child_data(fp, mdia_start, mdia_end, b"mdhd")
            timescale = struct.unpack_from(">I", mdhd, 20 if mdhd[0] == 1 else 12)[0]
            minf_start, minf_end = _child(fp, mdia_start, mdia_end, b"minf")
            stbl_start, stbl_end = _child(fp, minf_start, minf_end, b"stbl")
            positions, times = _sample_table(fp, stbl_start, stbl_end, timescale)
            return positions, times
    raise ValueError("Tesla clip has no video sample table")


def extract_track(video_path: Path) -> list[tuple[float, ...]]:
    positions, times = probe_and_packet_times(video_path)
    samples: list[tuple[float, ...]] = []
    next_second = 0.0
    for offset, metadata in iter_sei(video_path):
        index = bisect.bisect_right(positions, offset) - 1
        if index < 0:
            continue
        latitude = float(metadata.get("latitude_deg", 0.0))
        longitude = float(metadata.get("longitude_deg", 0.0))
        if (
            not math.isfinite(latitude)
            or not math.isfinite(longitude)
            or not (-90 <= latitude <= 90 and -180 <= longitude <= 180)
            or (latitude == 0 and longitude == 0)
        ):
            continue
        seconds = times[index]
        if seconds + 1e-6 < next_second:
            continue
        speed = abs(float(metadata.get("vehicle_speed_mps", 0.0)))
        sample = (
            seconds,
            latitude,
            longitude,
            speed,
            float(metadata.get("heading_deg", 0.0)),
            float(metadata.get("gear_state", 0)),
            float(metadata.get("autopilot_state", 0)),
            float(metadata.get("linear_acceleration_mps2_x", 0.0)),
            float(metadata.get("linear_acceleration_mps2_y", 0.0)),
            float(metadata.get("linear_acceleration_mps2_z", 0.0)),
            float(metadata.get("accelerator_pedal_position", 0.0)),
            float(metadata.get("brake_applied", 0)),
            float(metadata.get("steering_wheel_angle", 0.0)),
            float(metadata.get("blinker_on_left", 0)),
            float(metadata.get("blinker_on_right", 0)),
        )
        if not all(math.isfinite(value) for value in sample):
            continue
        samples.append(sample)
        next_second = math.floor(seconds) + 1.0
    if not samples:
        raise ValueError("Tesla clip has no usable GPS telemetry")
    return samples


class CorruptIndexError(RuntimeError):
    """clips.json cannot be parsed, so it must not be replaced with an empty index."""


def day_of(entry: dict[str, Any]) -> str:
    """The local civil day a clip was recorded (from its filename stamp)."""
    return str(entry["recorded_at"])[:10]


def day_path(day: str) -> Path:
    return DAYS_DIR / f"{day}.json"


def _read_json_index(path: Path) -> list[dict[str, Any]]:
    try:
        data = json.loads(path.read_text())
    except json.JSONDecodeError as exc:
        raise CorruptIndexError(f"refusing to replace corrupt {path}: {exc}") from exc
    clips: list[dict[str, Any]] = data.get("clips", [])
    return clips


def migrate_legacy_index() -> None:
    """Split the old single clips.json into day files, then remove it.

    Idempotent: if power is lost halfway, the next run writes the same day
    files again. A corrupt clips.json is left alone (CorruptIndexError).
    """
    if not INDEX_FILE.exists():
        return
    legacy = _read_json_index(INDEX_FILE)
    days: dict[str, list[dict[str, Any]]] = {}
    for clip in legacy:
        days.setdefault(day_of(clip), []).append(clip)
    for day, clips in days.items():
        current = {
            c["id"]: c for c in (_read_json_index(day_path(day)) if day_path(day).exists() else [])
        }
        current.update({c["id"]: c for c in clips})
        _write_day(day, list(current.values()))
    INDEX_FILE.unlink()


def load_index() -> list[dict[str, Any]]:
    migrate_legacy_index()
    if not DAYS_DIR.is_dir():
        return []
    clips: list[dict[str, Any]] = []
    for path in sorted(DAYS_DIR.glob("*.json")):
        clips.extend(_read_json_index(path))
    return clips


def atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    except Exception:
        temporary.unlink(missing_ok=True)
        raise


def _write_day(day: str, clips: list[dict[str, Any]]) -> None:
    clips.sort(key=lambda clip: clip["recorded_at"], reverse=True)
    data = json.dumps({"version": 2, "day": day, "clips": clips}, separators=(",", ":")).encode()
    path = day_path(day)
    try:
        if path.read_bytes() == data:
            return  # unchanged: spare the SD card a write
    except FileNotFoundError:
        pass
    atomic_write(path, data)


def _delete_day(day: str) -> None:
    day_path(day).unlink(missing_ok=True)
    for pack in DAYS_DIR.glob(f"{day}.*.f32"):
        pack.unlink(missing_ok=True)
    for line in DAYS_DIR.glob(f"{day}.*.line"):
        line.unlink(missing_ok=True)


def save_index(clips: list[dict[str, Any]], days: set[str] | None = None) -> None:
    """Write the day files for these clips: only the given days, or every day.

    A day left without clips is deleted with its packed tracks.
    """
    by_day: dict[str, list[dict[str, Any]]] = {}
    for clip in clips:
        by_day.setdefault(day_of(clip), []).append(clip)
    if days is None:
        existing = {path.stem for path in DAYS_DIR.glob("*.json")} if DAYS_DIR.is_dir() else set()
        days = set(by_day) | existing
    for day in sorted(days):
        if by_day.get(day):
            _write_day(day, by_day[day])
        else:
            _delete_day(day)


def track_path(clip_id: str) -> Path:
    return TRACK_DIR / (hashlib.sha256(clip_id.encode()).hexdigest() + ".f32")


def retention_days() -> int:
    try:
        value = int(os.environ.get("TELEMETRY_RETENTION_DAYS", DEFAULT_RETENTION_DAYS))
    except ValueError:
        return DEFAULT_RETENTION_DAYS
    return value if 1 <= value <= 3650 else DEFAULT_RETENTION_DAYS


def filename_recorded_at(video_path: Path) -> datetime:
    match = re.fullmatch(r"(\d{4}-\d{2}-\d{2})_(\d{2}-\d{2}-\d{2})-front\.mp4", video_path.name)
    if not match:
        raise ValueError("Tesla clip filename has no recognizable timestamp")
    return datetime.strptime(f"{match[1]} {match[2].replace('-', ':')}", "%Y-%m-%d %H:%M:%S")


def local_timezone() -> tzinfo:
    zone = datetime.now().astimezone().tzinfo
    return UTC if zone is None else zone


def recorded_at_utc(value: str) -> datetime:
    """Filename stamps are local civil time. Compare retention in UTC."""
    recorded = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if recorded.tzinfo is None:
        recorded = recorded.replace(tzinfo=local_timezone())
    return recorded.astimezone(UTC)


def retention_cutoff_day() -> str:
    """The oldest local day kept. Retention removes whole days."""
    return (datetime.now() - timedelta(days=retention_days())).date().isoformat()


def prune(clips: list[dict[str, Any]]) -> list[dict[str, Any]]:
    cutoff = retention_cutoff_day()
    retained = []
    for clip in clips:
        if day_of(clip) >= cutoff:
            retained.append(clip)
        else:
            track_path(clip["id"]).unlink(missing_ok=True)
    return retained


def has_track(entry: dict[str, Any]) -> bool:
    pack = entry.get("pack")
    if pack:
        return (DAYS_DIR / f"{pack['name']}.f32").is_file()
    return track_path(entry["id"]).is_file()


def read_track_bytes(entry: dict[str, Any]) -> bytes:
    """A clip's packed points (15 little-endian floats each), raw or compacted."""
    pack = entry.get("pack")
    if not pack:
        return track_path(entry["id"]).read_bytes()
    with open(DAYS_DIR / f"{pack['name']}.f32", "rb") as handle:
        handle.seek(int(pack["offset"]) * POINT_BYTES)
        data = handle.read(int(pack["count"]) * POINT_BYTES)
    if len(data) != int(pack["count"]) * POINT_BYTES:
        raise OSError(f"truncated pack for {entry['id']}")
    return data


class ClipIndex:
    """clips.json held in memory, so a batch reads it once and writes it only on change."""

    def __init__(self) -> None:
        self.clips = {clip["id"]: clip for clip in load_index()}
        self.dirty_days: set[str] = set()
        # Days before this are dropped by prune() on save, so their clips are
        # not read at all: a Pi with months-old clips would otherwise extract
        # them all on every run and keep none.
        self.cutoff_day = retention_cutoff_day()
        self.too_old = 0

    @property
    def dirty(self) -> bool:
        return bool(self.dirty_days)

    def touch(self, entry: dict[str, Any]) -> None:
        self.dirty_days.add(day_of(entry))

    def put(self, entry: dict[str, Any]) -> None:
        self.clips[entry["id"]] = entry
        self.touch(entry)

    def save(self) -> None:
        clips = list(self.clips.values())
        retained = prune(clips)
        kept = {clip["id"] for clip in retained}
        days = self.dirty_days | {day_of(clip) for clip in clips if clip["id"] not in kept}
        if days:
            save_index(retained, days)
        self.clips = {clip["id"]: clip for clip in retained}
        self.dirty_days = set()


def index_front_clip(front: Path, clip_id: str, index: ClipIndex) -> None:
    recorded_at = filename_recorded_at(front).isoformat()
    if recorded_at[:10] < index.cutoff_day:
        index.too_old += 1
        return
    existing = index.clips.get(clip_id)
    if existing and existing.get("state") == "no-gps":
        return
    if existing and existing.get("point_words", 4) == 15 and has_track(existing):
        # Already extracted. "archived" means copied to the archive and is kept
        # while the clip is still on the Pi (the backend shows those as
        # available): re-indexing after every snapshot must not lose it, or the
        # clip would read "overwritten before archiving" once its snapshot goes.
        current = {"source": clip_id, "recorded_at": recorded_at}
        if existing.get("state") != "archived":
            current["state"] = "available"
        if any(existing.get(key) != value for key, value in current.items()):
            existing.update(current)
            index.touch(existing)
        return
    if not front.exists():
        return  # a link whose snapshot was released: nothing left to read
    try:
        samples = extract_track(front)
    except ValueError as exc:
        index.put(
            {
                "id": clip_id,
                "recorded_at": recorded_at,
                "state": "no-gps",
                "front_file": front.name,
                "source": clip_id,
                "duration": 0,
                "samples": 0,
                "point_words": 15,
                "error": str(exc),
            }
        )
        return
    packed = b"".join(struct.pack("<15f", *sample) for sample in samples)
    atomic_write(track_path(clip_id), packed)
    index.put(
        {
            "id": clip_id,
            "recorded_at": recorded_at,
            "state": "available",
            "front_file": front.name,
            "source": clip_id,
            "duration": round(samples[-1][0], 3),
            "samples": len(samples),
            "point_words": 15,
            "bbox": track_bbox(samples),
        }
    )


def _is_non_recent_link(path: Path) -> bool:
    if not path.is_symlink():
        return False
    try:
        target = os.readlink(path)
    except OSError:
        return False
    parts = Path(target).parts
    return "SentryClips" in parts or "SavedClips" in parts


def index_clip(source_path: Path, index: ClipIndex | None = None) -> None:
    """Index one SavedClips event directory or RecentClips front MP4.

    Without an index, loads and saves clips.json around this one clip.
    """
    if index is None:
        index = ClipIndex()
        try:
            index_clip(source_path, index)
        finally:
            index.save()
        return
    if source_path.is_dir() and source_path.parent.name == "SavedClips":
        fronts = sorted(source_path.glob("*-front.mp4"))
        if not fronts:
            raise ValueError("SavedClips event has no front-camera MP4")
        indexed_any = False
        last_error: Exception | None = None
        for front in fronts:
            clip_id = f"SavedClips/{source_path.name}/{front.name}"
            try:
                index_front_clip(front, clip_id, index)
                indexed_any = True
            except (ValueError, OSError) as exc:
                last_error = exc
        if not indexed_any:
            if last_error is not None:
                raise last_error
            raise ValueError("SavedClips event contains no usable GPS telemetry")
    elif source_path.parent.parent.name == "RecentClips" and source_path.name.endswith(
        "-front.mp4"
    ):
        if _is_non_recent_link(source_path):
            raise ValueError("RecentClips symlink points to SentryClips or SavedClips")
        clip_id = f"RecentClips/{source_path.parent.name}/{source_path.name}"
        index_front_clip(source_path, clip_id, index)
    else:
        raise ValueError("Only SavedClips events and RecentClips front-camera MP4s are indexed")


UNREACHABLE_EXIT = 3


def _command_succeeds(command: list[str]) -> bool:
    try:
        result = subprocess.run(  # noqa: S603 — command comes from archiveloop
            command,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=30,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        return False
    return result.returncode == 0


LOCK_FILE = STORE_DIR / ".lock"
BUSY_EXIT = 75


@contextlib.contextmanager
def store_lock(wait: bool = True) -> Iterator[bool]:
    """Hold the telemetry store lock; yields False if wait is off and it is taken.

    The archive cycle and the snapshot loop (TELEMETRY_WHILE_DRIVING) both
    index; each run loads clips.json, adds to it and saves it, so two at once
    would drop each other's clips.
    """
    STORE_DIR.mkdir(parents=True, exist_ok=True)
    with open(LOCK_FILE, "a") as handle:
        try:
            fcntl.flock(handle, fcntl.LOCK_EX | (0 if wait else fcntl.LOCK_NB))
        except BlockingIOError:
            yield False
            return
        try:
            yield True
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


def scan_tree(root: Path) -> list[tuple[str, Path]]:
    """Every clip under a TeslaCam root whose track can be indexed.

    SavedClips events that hold a front-camera file (one entry per event) and
    RecentClips front-camera files, skipping RecentClips links into SentryClips
    or SavedClips. Links whose snapshot is gone are skipped when indexing.
    Telemetry does not depend on which categories are archived. Sentry events
    are not indexed.
    """
    found: list[tuple[str, Path]] = []
    saved = root / "SavedClips"
    if saved.is_dir():
        for event in sorted(saved.iterdir()):
            if event.is_dir() and not event.is_symlink() and any(event.glob("*-front.mp4")):
                found.append((f"SavedClips/{event.name}", event))
    recent = root / "RecentClips"
    if recent.is_dir():
        for directory, dirnames, filenames in os.walk(recent):
            dirnames.sort()
            for name in sorted(filenames):
                if not name.endswith("-front.mp4"):
                    continue
                front = Path(directory) / name
                # No exists() here: following the link mounts its snapshot,
                # which is slow and not needed for clips indexed already.
                if _is_non_recent_link(front):
                    continue
                found.append((front.relative_to(root).as_posix(), front))
    return found


def _recorded_order(entry: list[str]) -> str:
    """Sort key: the clip's file or folder name starts with its recording time."""
    return Path(entry[0]).name


def index_batch(
    lines: Iterable[str],
    reachable_cmd: list[str],
    check_interval: float = 60.0,
    save_interval: float = 30.0,
    budget: float | None = None,
) -> int:
    """Index many clips in one process. Each line is "<clip id>\t<source path>".

    Clips already in clips.json cost a stat, not a read of the video. Only
    new clips are extracted, oldest first: they are the first to be lost when
    snapshots are released. reachable_cmd, when given, is run at most once per
    check_interval seconds; if it fails, indexing stops so archiving is not held
    up for an archive that has gone away. With a budget (seconds), no clip is
    started after it runs out; the rest wait for the next run. Progress is saved
    every save_interval seconds so a power cut loses little work. Prints one
    line per clip that could not be indexed and a summary line.
    """
    started = last_check = last_save = time.monotonic()
    index = ClipIndex()
    entries = sorted(
        (line.rstrip("\n").split("\t", 1) for line in lines if line.strip()),
        key=_recorded_order,
    )
    before = set(index.clips)
    failed = 0
    status = 0
    try:
        for position, entry in enumerate(entries):
            clip_id, source = entry if len(entry) == 2 else (entry[0], entry[0])
            if budget is not None and time.monotonic() - started >= budget:
                print(f"time budget of {budget:.0f}s used, {len(entries) - position} clips left")
                break
            if reachable_cmd and time.monotonic() - last_check >= check_interval:
                if not _command_succeeds(reachable_cmd):
                    print(f"archive unreachable, deferring {len(entries) - position} clips")
                    status = UNREACHABLE_EXIT
                    break
                last_check = time.monotonic()
            try:
                index_clip(Path(source), index)
            except (ValueError, OSError) as exc:
                failed += 1
                print(f"{clip_id}: {exc}")
            if index.dirty and time.monotonic() - last_save >= save_interval:
                index.save()
                last_save = time.monotonic()
    finally:
        index.save()
    added = sum(1 for clip_id in index.clips if clip_id not in before)
    summary = (
        f"{len(entries)} clips checked, {added} new, {failed} failed "
        f"in {time.monotonic() - started:.1f}s"
    )
    if index.too_old:
        summary += f" ({index.too_old} older than {retention_days()} days skipped)"
    print(summary)
    return status


CAMERA_SUFFIX = re.compile(
    r"-(front|back|left_repeater|right_repeater|left_pillar|right_pillar)\.mp4$"
)


def mark_archived(merged_root: Path | None, file_list: Path) -> None:
    """Mark clips whose video was copied to the archive.

    Tracks are keyed by the front-camera file, but any camera counts: with
    RECENTCLIPS_CAMERAS a minute may be archived without its front file.
    With a merged root, a listed file counts only once the copy removed it
    from that overlay; without one, every listed file is known to be archived.
    """
    clips = load_index()
    changed = False
    for line in file_list.read_text().splitlines():
        if not CAMERA_SUFFIX.search(line):
            continue
        front = CAMERA_SUFFIX.sub("-front.mp4", line)
        if line.startswith("SavedClips/"):
            clip_ids = [front, front.rsplit("/", 1)[0]]
        elif line.startswith("RecentClips/"):
            clip_ids = [front]
        else:
            continue
        for clip in clips:
            copied = merged_root is None or not (merged_root / line).is_file()
            if clip["id"] in clip_ids and copied and clip.get("state") != "archived":
                clip["state"] = "archived"
                changed = True
    if changed:
        save_index(clips)


FINISHED_AFTER = timedelta(hours=1)
LINE_TOLERANCE_M = 5.0
LINE_MAX_GAP_S = 30.0


def simplify_line(points: list[tuple[float, ...]]) -> list[tuple[float, float]]:
    """A map line for a track: Douglas-Peucker within about 5 m, plus a point
    at least every 30 s so a slow crawl keeps its timing. Points are the
    15-float samples (t, lat, lon, ...). The backend thins today's raw tracks
    with the same rule (simplifyLine in telemetry_ops.go)."""
    if len(points) <= 2:
        return [(p[1], p[2]) for p in points]
    lat0 = math.radians(points[0][1])
    xs = [p[2] * 111_320.0 * math.cos(lat0) for p in points]
    ys = [p[1] * 110_540.0 for p in points]
    keep = [False] * len(points)
    keep[0] = keep[-1] = True
    stack = [(0, len(points) - 1)]
    while stack:
        first, last = stack.pop()
        if last - first < 2:
            continue
        dx, dy = xs[last] - xs[first], ys[last] - ys[first]
        length = math.hypot(dx, dy)
        worst, worst_distance = -1, 0.0
        for i in range(first + 1, last):
            if length == 0:
                distance = math.hypot(xs[i] - xs[first], ys[i] - ys[first])
            else:
                distance = abs(dy * (xs[i] - xs[first]) - dx * (ys[i] - ys[first])) / length
            if distance > worst_distance:
                worst, worst_distance = i, distance
        if worst_distance > LINE_TOLERANCE_M:
            keep[worst] = True
            stack.append((first, worst))
            stack.append((worst, last))
    last_kept_t = points[0][0]
    for i, point in enumerate(points):
        if keep[i]:
            last_kept_t = point[0]
        elif point[0] - last_kept_t >= LINE_MAX_GAP_S:
            keep[i] = True
            last_kept_t = point[0]
    return [(p[1], p[2]) for i, p in enumerate(points) if keep[i]]


def day_finished(day: str, entries: list[dict[str, Any]], now: datetime) -> bool:
    """Before today, or nothing recorded for an hour: no more clips expected."""
    if day < now.date().isoformat():
        return True
    ends = [
        datetime.fromisoformat(str(e["recorded_at"])).replace(tzinfo=None)
        + timedelta(seconds=float(e.get("duration") or 60))
        for e in entries
    ]
    return not ends or now - max(ends) >= FINISHED_AFTER


def _sweep(index_clips: list[dict[str, Any]]) -> int:
    """Delete pack files no day references and raw tracks of packed or unknown clips."""
    removed = 0
    referenced = {c["pack"]["name"] for c in index_clips if c.get("pack")}
    if DAYS_DIR.is_dir():
        for path in list(DAYS_DIR.glob("*.f32")) + list(DAYS_DIR.glob("*.line")):
            if path.stem not in referenced:
                path.unlink(missing_ok=True)
                removed += 1
    raw = {track_path(c["id"]).name for c in index_clips if not c.get("pack")}
    if TRACK_DIR.is_dir():
        for path in TRACK_DIR.glob("*.f32"):
            if path.name not in raw:
                path.unlink(missing_ok=True)
                removed += 1
    return removed


def compact(now: datetime | None = None) -> list[str]:
    """Pack each finished day's tracks into one file plus a map line.

    New files get a new generation name, the day file is switched to them by
    an atomic rename, and only then are the old pack and raw tracks deleted. A
    power cut at any point leaves every clip readable, and the next run redoes
    or finishes the work. Returns the days packed.
    """
    now = now or datetime.now()
    index = ClipIndex()
    by_day: dict[str, list[dict[str, Any]]] = {}
    for clip in index.clips.values():
        by_day.setdefault(day_of(clip), []).append(clip)
    packed_days = []
    # Clips indexed before bounding boxes existed get one, so place searches
    # (near) can skip them without reading their tracks.
    for day, entries in sorted(by_day.items()):
        missing = [e for e in entries if not e.get("bbox") and e.get("samples") and has_track(e)]
        for entry in missing:
            try:
                points = _points(entry)
            except OSError:
                continue
            if points:
                entry["bbox"] = track_bbox(points)
        if missing:
            save_index(entries, {day})
    for day, entries in sorted(by_day.items()):
        with_tracks = sorted(
            (
                e
                for e in entries
                if e.get("point_words") == POINT_FLOATS and e.get("samples") and has_track(e)
            ),
            key=lambda e: str(e["recorded_at"]),
        )
        if not any(not e.get("pack") for e in with_tracks) or not day_finished(day, entries, now):
            continue
        name = f"{day}.{time.time_ns() // 1_000_000}"
        pack, line = bytearray(), bytearray()
        references = {}
        for entry in with_tracks:
            try:
                data = read_track_bytes(entry)
            except OSError:
                continue
            points = [
                struct.unpack_from(f"<{POINT_FLOATS}f", data, offset)
                for offset in range(0, len(data) - len(data) % POINT_BYTES, POINT_BYTES)
            ]
            simplified = simplify_line(points)
            references[entry["id"]] = {
                "name": name,
                "offset": len(pack) // POINT_BYTES,
                "count": len(points),
                "line_offset": len(line) // 8,
                "line_count": len(simplified),
            }
            pack += data[: len(points) * POINT_BYTES]
            line += b"".join(struct.pack("<2f", lat, lon) for lat, lon in simplified)
        atomic_write(DAYS_DIR / f"{name}.f32", bytes(pack))
        atomic_write(DAYS_DIR / f"{name}.line", bytes(line))
        for entry in with_tracks:
            if entry["id"] in references:
                entry["pack"] = references[entry["id"]]
        save_index(entries, {day})
        packed_days.append(day)
    _sweep(list(index.clips.values()))
    return packed_days


def track_bbox(points: list[tuple[float, ...]]) -> list[float]:
    """[min lat, min lon, max lat, max lon] of a track, for cheap place searches."""
    lats = [p[1] for p in points]
    lons = [p[2] for p in points]
    return [round(min(lats), 6), round(min(lons), 6), round(max(lats), 6), round(max(lons), 6)]


def _points(entry: dict[str, Any]) -> list[tuple[float, ...]]:
    data = read_track_bytes(entry)
    return [
        struct.unpack_from(f"<{POINT_FLOATS}f", data, offset)
        for offset in range(0, len(data) - len(data) % POINT_BYTES, POINT_BYTES)
    ]


def main() -> None:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    index_parser = subparsers.add_parser("index")
    index_parser.add_argument("event_dir", type=Path)
    batch_parser = subparsers.add_parser(
        "index-batch",
        help='index "<clip id>\\t<source path>" lines from stdin in one run',
    )
    batch_parser.add_argument(
        "--scan",
        type=Path,
        default=None,
        metavar="ROOT",
        help="index every eligible clip under this TeslaCam root instead of reading stdin",
    )
    batch_parser.add_argument(
        "--no-wait",
        action="store_true",
        help="exit at once if another indexer holds the store lock",
    )
    batch_parser.add_argument(
        "--budget",
        type=float,
        default=None,
        help="seconds after which no new clip is started",
    )
    batch_parser.add_argument(
        "reachable_cmd",
        nargs=argparse.REMAINDER,
        help="command that exits 0 while the archive is reachable",
    )
    archived_parser = subparsers.add_parser("mark-archived")
    archived_parser.add_argument("merged_root", type=Path)
    archived_parser.add_argument("file_list", type=Path)
    listed_parser = subparsers.add_parser(
        "mark-listed", help="mark the clips of every file in the list as archived"
    )
    listed_parser.add_argument("file_list", type=Path)
    subparsers.add_parser("prune")
    subparsers.add_parser("compact", help="pack finished days into one file each")
    args = parser.parse_args()
    with store_lock(wait=not getattr(args, "no_wait", False)) as locked:
        if not locked:
            print("another indexer is running; skipped")
            sys.exit(BUSY_EXIT)
        if args.command == "index":
            index_clip(args.event_dir)
        elif args.command == "index-batch":
            lines: Iterable[str] = (
                [f"{clip_id}\t{path}" for clip_id, path in scan_tree(args.scan)]
                if args.scan is not None
                else sys.stdin
            )
            sys.exit(index_batch(lines, args.reachable_cmd, budget=args.budget))
        elif args.command == "mark-archived":
            mark_archived(args.merged_root, args.file_list)
        elif args.command == "mark-listed":
            mark_archived(None, args.file_list)
        elif args.command == "compact":
            started = time.monotonic()
            days = compact()
            if days:
                print(
                    f"packed {len(days)} day(s) ({', '.join(days)}) in {time.monotonic() - started:.1f}s"
                )
        else:
            save_index(prune(load_index()))


if __name__ == "__main__":
    main()
