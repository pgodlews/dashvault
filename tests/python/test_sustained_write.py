import argparse
import importlib.util
import sys
import threading
from pathlib import Path
from typing import Any

import pytest


@pytest.fixture()
def sustained_write() -> Any:
    path = Path(__file__).resolve().parents[2] / "tools/sustained-write-test.py"
    spec = importlib.util.spec_from_file_location("sustained_write_test", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def test_stream_repeats_source_and_honors_size(sustained_write: Any, tmp_path: Path) -> None:
    source = tmp_path / "source.mp4"
    output = tmp_path / "stream-1.bin"
    source.write_bytes(b"abcdefghij")

    result = sustained_write.write_stream(
        source,
        output,
        size_bytes=100,
        duration_seconds=10,
        stop=threading.Event(),
        stream=1,
        chunk_bytes=7,
        fsync_bytes=20,
    )

    assert result.bytes_written == 100
    assert output.stat().st_size == 100
    assert output.read_bytes() == (b"abcdefghij" * 10)
    assert result.fsync_calls >= 2


def test_stream_paces_writes_over_duration(sustained_write: Any, tmp_path: Path) -> None:
    source = tmp_path / "source.mp4"
    output = tmp_path / "paced.mp4"
    source.write_bytes(b"abcdefghij")

    result = sustained_write.write_stream(
        source,
        output,
        size_bytes=100,
        duration_seconds=0.05,
        stop=threading.Event(),
        stream=1,
        chunk_bytes=100,
        paced=True,
        pace_interval_seconds=0.01,
    )

    assert result.bytes_written == 100
    assert result.seconds >= 0.03
    assert output.read_bytes() == b"abcdefghij" * 10


def test_clip_series_preserves_source_layout_and_size(sustained_write: Any, tmp_path: Path) -> None:
    source = tmp_path / "2026-11-07_12-00-00-front.mp4"
    source.write_bytes(b"original clip contents")
    test_dir = tmp_path / "test"
    test_dir.mkdir()

    result = sustained_write.write_clip_series(
        source,
        test_dir,
        stream=1,
        size_bytes=source.stat().st_size,
        duration_seconds=0.15,
        cycle_seconds=0.03,
        stop=threading.Event(),
    )

    cycle_dirs = sorted(test_dir.glob("cycle-*"))
    assert len(cycle_dirs) >= 2
    assert cycle_dirs[0].name == "cycle-0000"
    assert all((cycle / source.name).read_bytes() == source.read_bytes() for cycle in cycle_dirs)
    assert result.bytes_written == source.stat().st_size * len(cycle_dirs)


def test_unmounted_target_is_rejected(sustained_write: Any, tmp_path: Path) -> None:
    clip = tmp_path / "clip.mp4"
    clip.write_bytes(b"clip")
    args = argparse.Namespace(
        target=tmp_path,
        clips=[clip],
        size_mb=1,
        duration=1,
        keep=False,
    )

    with pytest.raises(ValueError, match="mounted directory"):
        sustained_write.run_test(args)


def test_duplicate_clip_basenames_are_disambiguated(
    sustained_write: Any, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    dir_a = tmp_path / "eventA"
    dir_b = tmp_path / "eventB"
    dir_a.mkdir()
    dir_b.mkdir()
    clip_a = dir_a / "clip-front.mp4"
    clip_b = dir_b / "clip-front.mp4"
    clip_a.write_bytes(b"aaaa" * 16)
    clip_b.write_bytes(b"bbbb" * 16)

    target = tmp_path / "mnt"
    target.mkdir()
    monkeypatch.setattr(sustained_write.os.path, "ismount", lambda path: Path(path) == target)

    args = argparse.Namespace(
        target=target,
        clips=[clip_a, clip_b],
        size_mb=0,
        duration=0.05,
        cycle_seconds=0.05,
        keep=True,
    )
    summary = sustained_write.run_test(args)
    assert len(summary["streams"]) == 2
    test_dirs = list(target.glob(".teslausb-write-test-*"))
    assert len(test_dirs) == 1
    cycle_files = sorted(p.name for p in (test_dirs[0] / "cycle-0000").iterdir())
    assert cycle_files == ["stream-01-clip-front.mp4", "stream-02-clip-front.mp4"]
