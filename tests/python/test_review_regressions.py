"""Run real shell functions with temporary files and fake hardware commands."""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

BASH = (
    "/opt/homebrew/bin/bash"
    if Path("/opt/homebrew/bin/bash").exists()
    else (shutil.which("bash") or "/bin/bash")
)

ROOT = Path(__file__).resolve().parents[2]


def function(name: str) -> str:
    source = (ROOT / "run/archiveloop").read_text()
    start = source.index(f"function {name} () {{")
    return source[start : source.index("\n}\n", start) + 3]


@pytest.mark.parametrize("backend", ["cifs", "nfs", "rsync", "none"])
@pytest.mark.parametrize(
    "music_image,music_share", [(False, False), (True, False), (False, True), (True, True)]
)
def test_archive_optional_music_does_not_exit(
    tmp_path: Path, backend: str, music_image: bool, music_share: bool
) -> None:
    (tmp_path / "cam_disk.bin").touch()
    if music_image:
        (tmp_path / "music_disk.bin").touch()
    result = subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [
            BASH,
            "-eu",
            "-o",
            "pipefail",
            "-c",
            function("configure_archive_env")
            + '\nconfigure_archive_env\necho "OK:${MUSIC_ARCHIVE_MOUNT:-}"',
        ],
        env={
            **os.environ,
            "ARCHIVE_SYSTEM": backend,
            "ARCHIVE_SERVER": "nas",
            "RSYNC_SERVER": "nas",
            "SHARE_NAME": "clips",
            "CAM_DISK": str(tmp_path / "cam_disk.bin"),
            "BACKING_DIR": str(tmp_path),
            "MUSIC_SHARE_NAME": "music" if music_share else "",
        },
        capture_output=True,
        text=True,
        check=True,
    )
    assert result.stdout.startswith("OK:")
    if backend in ("cifs", "nfs"):
        assert result.stdout.strip() == "OK:" + (
            "/mnt/musicarchive" if music_image and music_share else ""
        )


@pytest.mark.parametrize(
    "cameras,kept",
    [
        ("", {"front", "back", "left_repeater", "right_repeater", "left_pillar", "right_pillar"}),
        ("front", {"front"}),
        ("back,left_repeater", {"back", "left_repeater"}),
        (" back  left_repeater ", {"back", "left_repeater"}),
    ],
)
def test_recent_clips_camera_filter(tmp_path: Path, cameras: str, kept: set[str]) -> None:
    all_cameras = [
        "front",
        "back",
        "left_repeater",
        "right_repeater",
        "left_pillar",
        "right_pillar",
    ]
    recent = [f"RecentClips/2026-11-07/2026-11-07_10-00-00-{c}.mp4" for c in all_cameras]
    others = [
        "SavedClips/2026-11-07_10-05-00/2026-11-07_10-04-00-back.mp4",
        "SentryClips/2026-11-07_11-00-00/2026-11-07_10-59-00-right_repeater.mp4",
        "TeslaTrackMode/lap-back.mp4",
        "RecentClips/2026-11-07/2026-11-07_10-00-00-roof.mp4",
    ]
    file_list = tmp_path / "files"
    file_list.write_text("\n".join(recent + others) + "\n")
    subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [
            BASH,
            "-eu",
            "-o",
            "pipefail",
            "-c",
            function("filter_recent_cameras")
            + 'log() { :; }\nfilter_recent_cameras "$FILE_LIST"\n',
        ],
        env={**os.environ, "FILE_LIST": str(file_list), "RECENTCLIPS_CAMERAS": cameras},
        check=True,
    )
    remaining = file_list.read_text().splitlines()
    assert [line for line in remaining if line not in recent] == others
    assert {line.rsplit("-", 1)[1][:-4] for line in remaining if line in recent} == kept


def test_archive_list_is_split_by_priority(tmp_path: Path) -> None:
    files = [
        "RecentClips/2026-11-07/2026-11-07_10-00-00-front.mp4",
        "SavedClips/2026-11-07_10-05-00/2026-11-07_10-04-00-back.mp4",
        "SentryClips/2026-11-07_11-00-00/2026-11-07_10-59-00-front.mp4",
        "TeslaTrackMode/lap-back.mp4",
        "Extra/added-by-filter.mp4",
        "SavedClips/2026-11-07_10-05-00/2026-11-07_10-04-00-front.mp4",
    ]
    file_list = tmp_path / "files"
    file_list.write_text("\n".join(sorted(files)) + "\n")
    log = tmp_path / "log"
    result = subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [
            BASH,
            "-eu",
            "-o",
            "pipefail",
            "-c",
            function("split_archive_list")
            + 'log() { echo "$*" >> "$LOG"; }\nsplit_archive_list "$FILE_LIST"\n',
        ],
        env={**os.environ, "FILE_LIST": str(file_list), "LOG": str(log)},
        check=True,
        capture_output=True,
        text=True,
    )
    parts = [Path(p).read_text().splitlines() for p in result.stdout.splitlines()]
    assert [part[0].split("/")[0] for part in parts] == [
        "SavedClips",
        "SentryClips",
        "TeslaTrackMode",
        "RecentClips",
        "Extra",
    ]
    assert sorted(line for part in parts for line in part) == sorted(files)
    assert log.read_text() == (
        "Copy order: SavedClips 2, SentryClips 1, TeslaTrackMode 1, RecentClips 1, other 1\n"
    )

    file_list.write_text("RecentClips/2026-11-07/2026-11-07_10-00-00-front.mp4\n")
    result = subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [
            BASH,
            "-eu",
            "-o",
            "pipefail",
            "-c",
            function("split_archive_list") + 'log() { :; }\nsplit_archive_list "$FILE_LIST"\n',
        ],
        env={**os.environ, "FILE_LIST": str(file_list)},
        check=True,
        capture_output=True,
        text=True,
    )
    assert len(result.stdout.splitlines()) == 1


@pytest.mark.parametrize(
    "conf,expected",
    [
        ("export TELEMETRY_WHILE_DRIVING=true\n", "true"),
        ("export TELEMETRY_WHILE_DRIVING='true'\n", "true"),
        ('export TELEMETRY_WHILE_DRIVING="false" # off\n', "false"),
        ("TELEMETRY_WHILE_DRIVING=true\nexport TELEMETRY_WHILE_DRIVING=false\n", "false"),
        ("# export TELEMETRY_WHILE_DRIVING=true\n", "default"),
        ("export TELEMETRY_WHILE_DRIVING_X=true\n", "default"),
    ],
)
def test_current_setting_reads_the_saved_config(tmp_path: Path, conf: str, expected: str) -> None:
    conf_file = tmp_path / "teslausb_setup_variables.conf"
    conf_file.write_text(conf)
    result = subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [
            BASH,
            "-eu",
            "-o",
            "pipefail",
            "-c",
            function("current_setting") + "current_setting TELEMETRY_WHILE_DRIVING default\n",
        ],
        env={**os.environ, "setup_file": str(conf_file)},
        check=True,
        capture_output=True,
        text=True,
    )
    assert result.stdout == expected + "\n"


@pytest.mark.parametrize("while_driving", [False, True])
def test_index_after_snapshot_is_opt_in_and_quiet(tmp_path: Path, while_driving: bool) -> None:
    conf_file = tmp_path / "conf"
    conf_file.write_text(
        "export TELEMETRY_MAP_ENABLED=true\n"
        f"export TELEMETRY_WHILE_DRIVING={str(while_driving).lower()}\n"
        "export TELEMETRY_RETENTION_DAYS='90'\n"
    )
    calls = tmp_path / "calls"
    log = tmp_path / "log"
    script = (
        function("current_setting")
        + function("index_telemetry_after_snapshot")
        + """
log() { echo "$*" >> "$LOG"; }
ionice() {
  echo "$TELEMETRY_RETENTION_DAYS $*" >> "$CALLS"
  echo "1 clips checked, 0 new, 0 failed in 0.1s"
  echo "time budget of 240s used, 3 clips left"
}
index_telemetry_after_snapshot 480
"""
    )
    subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [BASH, "-eu", "-o", "pipefail", "-c", script],
        env={
            **os.environ,
            "setup_file": str(conf_file),
            "TESLACAM_DIR": "/mutable/TeslaCam",
            "CALLS": str(calls),
            "LOG": str(log),
        },
        check=True,
    )
    if not while_driving:
        assert not calls.exists()
        return
    # The retention saved in Settings applies without restarting archiveloop.
    assert calls.read_text().splitlines() == [
        "90 -c3 nice -n 19 /root/bin/telemetry_index.py index-batch"
        " --scan /mutable/TeslaCam --no-wait --budget 240",
    ]
    # "nothing new" is not logged; anything else is.
    assert log.read_text() == "Telemetry (after snapshot): time budget of 240s used, 3 clips left\n"


def test_archive_cycle_indexes_the_whole_tree() -> None:
    body = function("index_telemetry")
    assert 'index-batch --scan "$root"' in body
    assert "archive-is-reachable.sh" in body


def test_snapshot_loop_indexes_in_the_background() -> None:
    source = (ROOT / "run/archiveloop").read_text()
    start = source.index("function snapshotloop {")
    body = source[start : source.index("\n}\n", start)]
    assert 'index_telemetry_after_snapshot "${SNAPSHOT_INTERVAL:-480}" &' in body
    assert body.index("make_snapshot.sh") < body.index("index_telemetry_after_snapshot")


def test_benchmark_check_preserves_preexisting_file(tmp_path: Path) -> None:
    scratch = tmp_path / "unrelated"
    scratch.write_text("preserve this")
    result = subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [BASH, str(ROOT / "run/benchmark.sh"), "--check"],
        env={
            **os.environ,
            "BENCHMARK_TEST_FILE": str(scratch),
            "BENCHMARK_RESULTS_FILE": str(tmp_path / "missing.json"),
        },
        capture_output=True,
        text=True,
        check=True,
    )
    assert result.stdout.strip() == '{"results":[]}'
    assert scratch.read_text() == "preserve this"
    assert list(tmp_path.iterdir()) == [scratch]


@pytest.mark.parametrize("busy", [False, True])
def test_archive_mount_reconciliation_uses_normal_unmount(tmp_path: Path, busy: bool) -> None:
    commands = tmp_path / "commands"
    commands.mkdir()
    log = tmp_path / "commands.log"
    # Everything check-deps.sh wants for core + cifs, so the test is hermetic.
    for name in (
        "systemctl",
        "findmnt",
        "umount",
        "mount.cifs",
        "rsync",
        "python3",
        "curl",
        "sntp",
        "fsck.vfat",
        "killall",
    ):
        body = 'printf "%s %s\\n" "$(basename "$0")" "$*" >> "$COMMAND_LOG"\n'
        body += "exit 1\n" if busy and name == "umount" else "exit 0\n"
        target = commands / name
        target.write_text("#!/bin/bash\n" + body)
        target.chmod(0o755)
    archive = tmp_path / "archive"
    music = tmp_path / "music"
    result = subprocess.run(  # noqa: S603 — repository scripts and test-created commands only
        [BASH, "-eu", str(ROOT / "run/reconfigure-archive.sh"), "prepare", "cifs"],
        env={
            **os.environ,
            "PATH": f"{commands}:{os.environ['PATH']}",
            "COMMAND_LOG": str(log),
            "ARCHIVE_MOUNT": str(archive),
            "MUSIC_ARCHIVE_MOUNT": str(music),
        },
        capture_output=True,
        text=True,
    )
    assert result.returncode == (1 if busy else 0)
    operations = log.read_text().splitlines()
    assert operations[0] == "systemctl stop teslausb.service"
    assert f"umount {archive}" in operations
    assert not any("umount -l" in line or "restart" in line for line in operations)
    if not busy:
        assert f"umount {music}" in operations
        assert archive.is_dir() and music.is_dir()


def test_benchmark_run_owns_unique_scratch_file(tmp_path: Path) -> None:
    prefix = tmp_path / "existing"
    prefix.write_text("preserve")
    commands = tmp_path / "commands"
    commands.mkdir()
    dd = commands / "dd"
    dd.write_text("""#!/bin/bash
for arg in "$@"; do
  case "$arg" in of=*) printf '%s\\n' "${arg#of=}" > "$SCRATCH_LOG" ;; esac
done
sleep 0.05
""")
    dd.chmod(0o755)
    log = tmp_path / "scratch.log"
    result = subprocess.run(  # noqa: S603 — only fake dd runs, no benchmark writes
        [BASH, "-eu", str(ROOT / "run/benchmark.sh")],
        env={
            **os.environ,
            "PATH": f"{commands}:{os.environ['PATH']}",
            "BENCHMARK_TEST_FILE": str(prefix),
            "BENCHMARK_RESULTS_FILE": str(tmp_path / "results.json"),
            "SCRATCH_LOG": str(log),
        },
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, result.stderr
    scratch = Path(log.read_text().strip())
    assert scratch != prefix
    assert scratch.parent == tmp_path
    assert not scratch.exists()
    assert prefix.read_text() == "preserve"


def snapshot_function(name: str) -> str:
    source = (ROOT / "run/make_snapshot.sh").read_text()
    start = source.index(f"function {name} () {{")
    return source[start : source.index("\n}\n", start) + 3]


@pytest.mark.parametrize(
    "cameras,telemetry,kept",
    [
        ("", "true", {"front", "back", "left_repeater", "right_repeater"}),
        ("back left_repeater", "false", {"back", "left_repeater"}),
        ("back,left_repeater", "true", {"front", "back", "left_repeater"}),
        ("front", "false", {"front"}),
    ],
)
def test_snapshot_prune_touches_only_recent_clips(
    tmp_path: Path, cameras: str, telemetry: str, kept: set[str]
) -> None:
    root = tmp_path / "snap"
    recent = root / "TeslaCam/RecentClips"
    dated = recent / "2026-11-07"
    dated.mkdir(parents=True)
    all_cameras = ["front", "back", "left_repeater", "right_repeater"]
    recent_files = [recent / f"2026-11-07_10-00-00-{c}.mp4" for c in all_cameras]
    recent_files += [dated / f"2026-11-07_10-01-00-{c}.mp4" for c in all_cameras]
    untouched = [
        root / f"TeslaCam/SavedClips/2026-11-07_10-05-00/2026-11-07_10-04-00-{c}.mp4"
        for c in all_cameras
    ] + [
        root / f"TeslaCam/SentryClips/2026-11-07_11-00-00/2026-11-07_10-59-00-{c}.mp4"
        for c in all_cameras
    ]
    untouched += [
        root / "TeslaTrackMode/lap-back.mp4",
        recent / "notes-back.mp4",  # not a dashcam file name
        recent / "2026-11-07_10-00-00-roof.mp4",  # camera this code does not know
    ]
    for path in recent_files + untouched:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(b"x")
    link = recent / "2026-11-07_10-02-00-back.mp4"
    link.symlink_to(untouched[1])
    result = subprocess.run(  # noqa: S603 — repository scripts and test-created files only
        [
            BASH,
            "-eu",
            "-o",
            "pipefail",
            "-c",
            snapshot_function("prune_recent_cameras") + 'prune_recent_cameras "$ROOT"\n',
        ],
        env={
            **os.environ,
            "ROOT": str(root),
            "RECENTCLIPS_CAMERAS": cameras,
            "TELEMETRY_MAP_ENABLED": telemetry,
        },
        capture_output=True,
        text=True,
        check=True,
    )
    left = {p.name.rsplit("-", 1)[1][:-4] for p in recent_files if p.exists()}
    assert left == kept
    assert int(result.stdout) == len(recent_files) - 2 * len(kept)
    assert all(path.exists() for path in untouched)
    assert link.is_symlink()


def test_snapshot_prune_is_off_by_default(tmp_path: Path) -> None:
    script = snapshot_function("prune_snapshot") + 'prune_snapshot "$IMAGE"\necho done\n'
    for env in ({}, {"RECENTCLIPS_PRUNE_SNAPSHOTS": "true"}, {"RECENTCLIPS_CAMERAS": "front"}):
        result = subprocess.run(  # noqa: S603 — repository scripts only
            [BASH, "-eu", "-o", "pipefail", "-c", script],
            env={
                **{k: v for k, v in os.environ.items() if not k.startswith("RECENTCLIPS")},
                "IMAGE": str(tmp_path / "missing.bin"),
                **env,
            },
            capture_output=True,
            text=True,
            check=True,
        )
        assert result.stdout == "done\n"
