"""run/lib/startup_drives.sh: when archiveloop may repair a USB drive image."""

from __future__ import annotations

import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
HELPER = ROOT / "run/lib/startup_drives.sh"
BASH = (
    "/opt/homebrew/bin/bash"
    if Path("/opt/homebrew/bin/bash").exists()
    else (shutil.which("bash") or "/bin/bash")
)


class Board:
    """A fake gadget, UDC and mount table, with stub commands that change them."""

    def __init__(self, tmp_path: Path) -> None:
        self.tmp = tmp_path
        self.gadget = tmp_path / "gadget"
        self.udc_root = tmp_path / "udc"
        self.mounts = tmp_path / "mounts"
        self.events = tmp_path / "events"
        self.umount_failures = tmp_path / "umount-failures"
        self.detach_failures = tmp_path / "detach-failures"
        self.state = tmp_path / "archive_state"
        self.mounts.write_text("")
        self.events.write_text("")
        self.state.write_text("idle\n")
        commands = tmp_path / "commands"
        commands.mkdir()
        self._command(
            commands / "findmnt",
            'grep -qxF "$2" "$MOUNTS"',
        )
        self._command(
            commands / "umount",
            'echo "umount $*" >> "$EVENTS"\n'
            'if [ "$1" = -l ]; then exit 9; fi\n'
            'left=$(cat "$UMOUNT_FAILURES" 2>/dev/null || echo 0)\n'
            'if [ "$left" -gt 0 ]; then echo $((left - 1)) > "$UMOUNT_FAILURES"; exit 32; fi\n'
            'grep -vxF "$1" "$MOUNTS" > "$MOUNTS.new" || true\n'
            'mv "$MOUNTS.new" "$MOUNTS"\n',
        )
        self.disable = tmp_path / "disable_gadget.sh"
        self._command(
            self.disable,
            'echo detach >> "$EVENTS"\n'
            'left=$(cat "$DETACH_FAILURES" 2>/dev/null || echo 0)\n'
            'if [ "$left" -gt 0 ]; then echo $((left - 1)) > "$DETACH_FAILURES"; exit 1; fi\n'
            'rm -f "$USB_GADGET_ROOT/UDC"\n'
            'rmdir "$USB_GADGET_ROOT"\n',
        )
        self.path = f"{commands}:{os.environ['PATH']}"

    @staticmethod
    def _command(path: Path, body: str) -> None:
        path.write_text(f"#!/bin/bash\n{body}\n")
        path.chmod(0o755)

    def attach(self, udc_state: str | None, udc: str = "usb0") -> None:
        self.gadget.mkdir()
        (self.gadget / "UDC").write_text(f"{udc}\n" if udc else "\n")
        if udc and udc_state is not None:
            (self.udc_root / udc).mkdir(parents=True)
            (self.udc_root / udc / "state").write_text(f"{udc_state}\n")

    def mount(self, *names: str) -> None:
        self.mounts.write_text("".join(f"{self.tmp}/mnt/{name}\n" for name in names))

    def prepare(self) -> tuple[int, list[str]]:
        script = (
            f'source "{HELPER}"\n'
            'log() { echo "$*" >> "$LOG_FILE"; }\n'
            'write_archive_state() { printf "%s\\n" "$*" > "$ARCHIVE_STATE_FILE"; }\n'
            "prepare_drives_for_startup\n"
        )
        env = {
            **os.environ,
            "PATH": self.path,
            "USB_GADGET_ROOT": str(self.gadget),
            "UDC_CLASS_ROOT": str(self.udc_root),
            "DISABLE_GADGET_SCRIPT": str(self.disable),
            "STARTUP_DRIVE_RETRY_SECONDS": "0",
            "CAM_MOUNT": f"{self.tmp}/mnt/cam",
            "MUSIC_MOUNT": f"{self.tmp}/mnt/music",
            "LIGHTSHOW_MOUNT": f"{self.tmp}/mnt/lightshow",
            "BOOMBOX_MOUNT": f"{self.tmp}/mnt/boombox",
            "LOG_FILE": str(self.tmp / "archiveloop.log"),
            "ARCHIVE_STATE_FILE": str(self.state),
            "MOUNTS": str(self.mounts),
            "EVENTS": str(self.events),
            "UMOUNT_FAILURES": str(self.umount_failures),
            "DETACH_FAILURES": str(self.detach_failures),
        }
        result = subprocess.run(  # noqa: S603 - repository script with stub commands
            [BASH, "-eu", "-c", f"set -o pipefail\nif ! (\n{script}\n); then exit 1; fi"],
            env=env,
            capture_output=True,
            text=True,
            timeout=20,
            check=False,
        )
        assert result.returncode in (0, 1), result.stderr
        return result.returncode, self.events.read_text().splitlines()


@pytest.fixture
def board(tmp_path: Path) -> Board:
    return Board(tmp_path)


def test_fresh_boot_may_repair(board: Board) -> None:
    assert board.prepare() == (0, [])


@pytest.mark.parametrize(
    "state", ["configured", "addressed", "default", "powered", "suspended", "something new", None]
)
def test_attached_drive_is_left_alone(board: Board, state: str | None) -> None:
    board.attach(state)
    assert board.prepare() == (1, [])
    assert board.gadget.is_dir()


def test_unreadable_udc_counts_as_in_use(board: Board) -> None:
    board.attach("configured")
    (board.gadget / "UDC").unlink()
    (board.gadget / "UDC").mkdir()
    assert board.prepare() == (1, [])


@pytest.mark.parametrize(("state", "udc"), [("not attached", "usb0"), (None, "")])
def test_gadget_with_no_host_is_detached_then_repaired(
    board: Board, state: str | None, udc: str
) -> None:
    board.attach(state, udc)
    assert board.prepare() == (0, ["detach"])
    assert not board.gadget.exists()


@pytest.mark.parametrize("name", ["cam", "music", "lightshow", "boombox"])
def test_mounted_drive_is_unmounted_before_repair(board: Board, name: str) -> None:
    board.mount(name)
    code, events = board.prepare()
    assert code == 0
    assert events == [f"umount {board.tmp}/mnt/{name}"]
    assert board.mounts.read_text() == ""


def test_attached_and_mounted_drive_is_detached_first(board: Board) -> None:
    board.attach("configured")
    board.mount("cam")
    code, events = board.prepare()
    assert code == 0
    assert events == ["detach", f"umount {board.tmp}/mnt/cam"]


def test_failed_unmount_retries_without_lazy_unmount(board: Board) -> None:
    board.mount("cam")
    board.umount_failures.write_text("2")
    code, events = board.prepare()
    assert code == 0
    assert events == [f"umount {board.tmp}/mnt/cam"] * 3
    assert not any(" -l" in event for event in events)
    assert board.state.read_text() == "idle\n"
    assert "Could not release" in (board.tmp / "archiveloop.log").read_text()


def test_failed_detach_retries_until_released(board: Board) -> None:
    board.attach("not attached")
    board.detach_failures.write_text("2")
    code, events = board.prepare()
    assert code == 0
    assert events == ["detach"] * 3
    assert not board.gadget.exists()


def test_recovery_restores_an_existing_error_state(board: Board) -> None:
    board.state.write_text("error: Missing rsync\n")
    board.mount("cam")
    board.umount_failures.write_text("1")
    assert board.prepare()[0] == 0
    assert board.state.read_text() == "error: Missing rsync\n"


def test_archiveloop_only_repairs_at_startup_after_the_check() -> None:
    source = (ROOT / "run/archiveloop").read_text()
    start = source.index("if prepare_drives_for_startup")
    end = source.index("connect_usb_drives_to_host", start)
    block = source[start:end]
    repaired, kept = block.split("elif has_cam_disk")
    assert "fix_errors_in_images" in repaired
    assert "clean_cam_mount boot" in repaired
    assert "nofsck" in repaired
    assert "fix_errors_in_images" not in kept
    assert "clean_cam_mount" not in kept
    assert "nofsck" not in kept
    assert re.search(r"make_snapshot\.sh \|\| log", kept)
    # The helper's check is the only startup path to a repair.
    main_body = source[source.index("wifichecker &") : start]
    assert "fix_errors_in_images" not in main_body
    assert "clean_cam_mount" not in main_body


def test_configure_installs_the_helper() -> None:
    source = (ROOT / "setup/pi/configure.sh").read_text()
    assert 'copy_script run/lib/startup_drives.sh "$install_path/lib"' in source
