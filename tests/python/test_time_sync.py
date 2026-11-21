"""Exercise the merged time-sync function with commands that never use a network."""

import os
import re
import subprocess
from pathlib import Path

import pytest


@pytest.mark.parametrize(
    ("success", "expected"),
    [
        ("sntp:named", ["sntp:named"]),
        ("ntpdig:named", ["sntp:named", "ntpdig:named"]),
        ("sntp:static", ["sntp:named", "ntpdig:named", "sntp:static"]),
        ("ntpdig:static", ["sntp:named", "ntpdig:named", "sntp:static", "ntpdig:static"]),
        ("failure", ["sntp:named", "ntpdig:named", "sntp:static", "ntpdig:static"] * 5),
    ],
)
def test_time_sync_fallback(tmp_path: Path, success: str, expected: list[str]) -> None:
    source = (Path(__file__).parents[2] / "run" / "archiveloop").read_text()
    match = re.search(r"function set_time \(\) \{.*?\n\}", source, re.DOTALL)
    assert match is not None
    trace = tmp_path / "trace"
    trace.touch()
    # Only record command type and fallback category, never endpoint values.
    # Stub clock/logging/sleep as well, so exhaustion takes no real time.
    stubs = r"""
set -euo pipefail
log() { :; }
sleep() { :; }
date() { echo 0; }
awk() { echo 0; }
sync_stub() {
    local kind=named
    [[ "$3" =~ ^[0-9] ]] && kind=static
    printf '%s:%s\n' "$1" "$kind" >> "$TRACE"
    [[ "$1:$kind" == "$SUCCESS" ]]
}
sntp() { sync_stub sntp "$@"; }
ntpdig() { sync_stub ntpdig "$@"; }
"""
    result = subprocess.run(
        ["bash"],
        input=stubs + match.group() + "\nset_time\n",
        text=True,
        capture_output=True,
        env={**os.environ, "TRACE": str(trace), "SUCCESS": success},
    )
    assert result.returncode == 0
    assert trace.read_text().splitlines() == expected


def test_free_space_manager_uses_integer_byte_counts(tmp_path: Path) -> None:
    source = (Path(__file__).parents[2] / "run" / "archiveloop").read_text()
    match = re.search(r"function freespacemanager \{.*?\n\}", source, re.DOTALL)
    assert match is not None
    function_source = match.group().replace("/proc/self/comm", str(tmp_path / "comm"))
    stubs = """
set -euo pipefail
echo() { :; }
stat() { printf '25000000 4096\n'; }
sleep() { exit 0; }
"""
    result = subprocess.run(
        ["bash"],
        input=stubs + function_source + "\nfreespacemanager\n",
        text=True,
        capture_output=True,
        env={**os.environ, "CAM_DISK": "/tmp"},
    )

    assert result.returncode == 0, result.stderr


def test_telemetry_indexing_runs_after_copying() -> None:
    """A telemetry backlog must never delay copying clips to the archive."""
    source = (Path(__file__).parents[2] / "run" / "archiveloop").read_text()
    copy_marker = '/root/bin/archive-clips.sh "${archive_args[@]}"'
    index_marker = 'index_telemetry "$overlaylower"'

    assert source.index(copy_marker) < source.index(index_marker)
    assert source.index(index_marker) < source.index('umount "$overlaymerged"')


def test_archive_cycle_reloads_telemetry_settings_before_indexing() -> None:
    source = (Path(__file__).parents[2] / "run" / "archiveloop").read_text()
    match = re.search(r"function archive_teslacam_clips \(\) \{", source)
    assert match is not None
    function_start = match.end()
    reload_marker = "read_setup_variables"
    assert source.index(reload_marker, function_start) < source.index(
        'index_telemetry "$overlaylower"', function_start
    )
