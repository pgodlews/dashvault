#!/usr/bin/env python3
"""Sustained concurrent writes from existing clips to a mounted test filesystem."""

from __future__ import annotations

import argparse
import json
import math
import os
import shutil
import signal
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

CHUNK_BYTES = 1024 * 1024
FSYNC_BYTES = 64 * 1024 * 1024


@dataclass(frozen=True)
class StreamResult:
    stream: int
    source: str
    bytes_written: int
    seconds: float
    write_mbps: float
    fsync_calls: int


def write_stream(
    source: Path,
    output: Path,
    size_bytes: int,
    duration_seconds: float,
    stop: threading.Event,
    stream: int,
    chunk_bytes: int = CHUNK_BYTES,
    fsync_bytes: int = FSYNC_BYTES,
    paced: bool = False,
    pace_interval_seconds: float = 1.0,
) -> StreamResult:
    started = time.perf_counter()
    written = 0
    last_sync = 0
    sync_calls = 0
    slice_count = max(1, math.ceil(duration_seconds / pace_interval_seconds))
    slice_bytes = math.ceil(size_bytes / slice_count)
    slice_index = 0
    with source.open("rb") as reader, output.open("xb", buffering=0) as writer:
        while written < size_bytes and not stop.is_set():
            now = time.perf_counter()
            elapsed = now - started
            if elapsed >= duration_seconds:
                break
            if paced:
                deadline = started + slice_index * pace_interval_seconds
                if now < deadline:
                    stop.wait(min(deadline - now, duration_seconds - elapsed))
                    continue
                slice_target = min(size_bytes, (slice_index + 1) * slice_bytes)
                write_limit = slice_target - written
            else:
                write_limit = size_bytes - written
            chunk = reader.read(min(chunk_bytes, write_limit))
            if not chunk:
                reader.seek(0)
                continue
            writer.write(chunk)
            written += len(chunk)
            if written - last_sync >= fsync_bytes:
                os.fsync(writer.fileno())
                sync_calls += 1
                last_sync = written
            if paced and written >= slice_target:
                slice_index += 1
        os.fsync(writer.fileno())
        sync_calls += 1
    elapsed = max(time.perf_counter() - started, 1e-9)
    return StreamResult(
        stream=stream,
        source=source.name,
        bytes_written=written,
        seconds=elapsed,
        write_mbps=written / elapsed / 1e6,
        fsync_calls=sync_calls,
    )


def write_clip_series(
    source: Path,
    test_dir: Path,
    stream: int,
    size_bytes: int,
    duration_seconds: float,
    cycle_seconds: float,
    stop: threading.Event,
    output_name: str | None = None,
) -> StreamResult:
    started = time.perf_counter()
    total_bytes = 0
    sync_calls = 0
    cycle = 0
    target_name = output_name or source.name
    while not stop.is_set():
        elapsed = time.perf_counter() - started
        remaining = duration_seconds - elapsed
        if remaining <= 0:
            break
        cycle_dir = test_dir / f"cycle-{cycle:04d}"
        cycle_dir.mkdir(exist_ok=True)
        cycle_started = time.perf_counter()
        result = write_stream(
            source,
            cycle_dir / target_name,
            size_bytes,
            min(cycle_seconds, remaining),
            stop,
            stream,
            paced=True,
        )
        total_bytes += result.bytes_written
        sync_calls += result.fsync_calls
        cycle += 1
        remaining = duration_seconds - (time.perf_counter() - started)
        if remaining <= 0:
            break
        wait = min(cycle_seconds - (time.perf_counter() - cycle_started), remaining)
        if wait > 0:
            stop.wait(wait)
    elapsed = max(time.perf_counter() - started, 1e-9)
    return StreamResult(
        stream=stream,
        source=source.name,
        bytes_written=total_bytes,
        seconds=elapsed,
        write_mbps=total_bytes / elapsed / 1e6,
        fsync_calls=sync_calls,
    )


def run_test(args: argparse.Namespace) -> dict[str, Any]:
    target = args.target.resolve()
    clips = [path.resolve() for path in args.clips]
    if not target.is_dir() or not os.path.ismount(target):
        raise ValueError(f"Target must be a mounted directory: {target}")
    if not os.access(target, os.W_OK):
        raise ValueError(f"Target is not writable: {target}")
    for clip in clips:
        if not clip.is_file() or clip.stat().st_size == 0:
            raise ValueError(f"Clip is missing or empty: {clip}")
    clip_sizes = [
        args.size_mb * 1024 * 1024 if args.size_mb > 0 else clip.stat().st_size for clip in clips
    ]
    cycle_count = max(1, math.ceil(args.duration / args.cycle_seconds))
    required = sum(clip_sizes) * cycle_count + 256 * 1024 * 1024
    available = shutil.disk_usage(target).free
    if required > available:
        raise ValueError(
            f"Insufficient free space: need {required / 1e9:.1f} GB, have {available / 1e9:.1f} GB"
        )
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S")  # noqa: UP017
    test_dir = target / f".teslausb-write-test-{timestamp}-{os.getpid()}"
    test_dir.mkdir()
    stop = threading.Event()

    def request_stop(*_: object) -> None:
        stop.set()

    previous_handlers = {}
    for signum in (signal.SIGINT, signal.SIGTERM):
        previous_handlers[signum] = signal.signal(signum, request_stop)

    name_counts: dict[str, int] = {}
    for clip in clips:
        name_counts[clip.name] = name_counts.get(clip.name, 0) + 1
    output_names = [
        f"stream-{index:02d}-{clip.name}" if name_counts[clip.name] > 1 else clip.name
        for index, clip in enumerate(clips, 1)
    ]

    results: list[StreamResult] = []
    try:
        print(
            f"Writing {len(clips)} camera stream(s) for {args.duration}s in "
            f"{args.cycle_seconds}s cycles; test directory: {test_dir}",
            flush=True,
        )
        with ThreadPoolExecutor(max_workers=len(clips)) as pool:
            futures = [
                pool.submit(
                    write_clip_series,
                    clip,
                    test_dir,
                    index,
                    clip_sizes[index - 1],
                    args.duration,
                    args.cycle_seconds,
                    stop,
                    output_names[index - 1],
                )
                for index, clip in enumerate(clips, 1)
            ]
            while any(not future.done() for future in futures):
                time.sleep(min(5.0, args.duration))
                if stop.is_set():
                    break
            for future in futures:
                results.append(future.result())
    finally:
        stop.set()
        for signum, handler in previous_handlers.items():
            signal.signal(signum, handler)
        if not args.keep:
            shutil.rmtree(test_dir)
    results.sort(key=lambda result: result.stream)
    total_bytes = sum(result.bytes_written for result in results)
    total_seconds = max((result.seconds for result in results), default=0)
    summary = {
        "target_filesystem": os.stat(target).st_dev,
        "test_directory_removed": not args.keep,
        "duration_seconds": round(max((result.seconds for result in results), default=0), 3),
        "streams": [result.__dict__ for result in results],
        "total_bytes": total_bytes,
        "aggregate_write_mbps": round(total_bytes / total_seconds / 1e6, 1) if total_seconds else 0,
    }
    print(json.dumps(summary, indent=2), flush=True)
    return summary


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", type=Path, required=True, help="Mounted filesystem to test")
    parser.add_argument(
        "--clips", type=Path, nargs="+", required=True, help="Existing clips to repeat"
    )
    parser.add_argument(
        "--duration", type=float, default=300, help="Maximum seconds per stream (default: 300)"
    )
    parser.add_argument(
        "--cycle-seconds", type=float, default=60, help="Clip cycle cadence (default: 60)"
    )
    parser.add_argument(
        "--size-mb",
        type=int,
        default=0,
        help="Override MiB per clip; default 0 matches each source",
    )
    parser.add_argument("--keep", action="store_true", help="Keep generated test files")
    args = parser.parse_args()
    if args.duration <= 0 or args.cycle_seconds <= 0 or args.size_mb < 0:
        parser.error(
            "--duration and --cycle-seconds must be positive; --size-mb cannot be negative"
        )
    return args


def main() -> None:
    try:
        run_test(parse_args())
    except (OSError, ValueError) as error:
        raise SystemExit(f"Test failed safely: {error}") from error


if __name__ == "__main__":
    main()
