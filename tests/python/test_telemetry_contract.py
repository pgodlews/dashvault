"""The Go backend reads the store telemetry_index.py writes (days, packs, lines)."""

import importlib
import json
import os
import shutil
import subprocess
import sys
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any

import pytest

ROOT = Path(__file__).resolve().parents[2]
RUN_DIR = ROOT / "run"
if str(RUN_DIR) not in sys.path:
    sys.path.insert(0, str(RUN_DIR))
GO = shutil.which("go")


@pytest.mark.skipif(GO is None, reason="needs the Go toolchain")
def test_go_backend_reads_python_store(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    store = tmp_path / "store"
    monkeypatch.setenv("TELEMETRY_STORE_DIR", str(store))
    monkeypatch.setenv("TELEMETRY_RETENTION_DAYS", "30")
    import telemetry_index

    telemetry: Any = importlib.reload(telemetry_index)

    def track(lat0: float, seconds: int) -> list[tuple[float, ...]]:
        # A drive north then east: the corner must survive simplification.
        points = []
        for t in range(seconds):
            north = min(t, seconds // 2) * 0.0002
            east = max(t - seconds // 2, 0) * 0.0002
            points.append((float(t), lat0 + north, 18.0 + east, 12.0) + (0.0,) * 11)
        return points

    days = [(datetime.now() - timedelta(days=n)).strftime("%Y-%m-%d") for n in (2, 1, 0)]
    teslacam = tmp_path / "TeslaCam"
    expect: dict[str, Any] = {"clips": {}, "days": len(days)}
    for n, day in enumerate(days):
        clip_id = f"RecentClips/{day}/{day}_08-00-00-front.mp4"
        video = teslacam / clip_id
        video.parent.mkdir(parents=True)
        video.touch()
        samples = track(53.0 + n, 60)
        monkeypatch.setattr(telemetry, "extract_track", lambda path, s=samples: s)
        assert telemetry.index_batch([f"{clip_id}\t{video}\n"], []) == 0
        expect["clips"][clip_id] = {
            "points": len(samples),
            "first": [samples[0][1], samples[0][2]],
            "min_line": 3,
            "max_line": 6,
            "recorded_at": f"{day}T08:00:00",
        }
    # Days before today are packed; today's clip stays raw.
    packed = telemetry.compact(datetime.fromisoformat(f"{days[2]}T08:30:00"))
    assert packed == days[:2]
    stored = {c["id"]: c for c in telemetry.load_index()}
    assert [bool(stored[k].get("pack")) for k in expect["clips"]] == [True, True, False]

    expect_file = tmp_path / "expect.json"
    expect_file.write_text(json.dumps(expect))
    result = subprocess.run(  # noqa: S603 — the Go toolchain on PATH and repository tests
        [GO, "test", "-v", "-count=1", "-run", "TestReadsStoreWrittenByPython", "."],
        cwd=ROOT / "teslausb-www/go-backend",
        env={
            **os.environ,
            "TELEMETRY_PY_STORE": str(store),
            "TELEMETRY_PY_TESLACAM": str(teslacam),
            "TELEMETRY_PY_EXPECT": str(expect_file),
        },
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert "--- PASS: TestReadsStoreWrittenByPython" in result.stdout, result.stdout
