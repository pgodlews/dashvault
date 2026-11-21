"""os-update.sh: apt upgrades on a board with a read-only root filesystem.

The real upgrade path was exercised in an out-of-date Debian Trixie container;
these cover what a container cannot show: the remounts, the shared lock, the
free-space refusal and what is left behind when apt fails.
"""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "run/os-update.sh"
BASH = (
    "/opt/homebrew/bin/bash"
    if Path("/opt/homebrew/bin/bash").exists()
    else (shutil.which("bash") or "/bin/bash")
)
pytestmark = pytest.mark.skipif(
    int(
        subprocess.run(  # noqa: S603
            [BASH, "-c", "echo ${BASH_VERSINFO[0]}"], capture_output=True, text=True, check=True
        ).stdout
    )
    < 4,
    reason="os-update.sh needs bash 4 or later",
)

STUBS = {
    # $1.. logged to $CALLS as "name args"
    "flock": 'echo "flock $*" >> "$CALLS"',
    "sync": 'echo "sync" >> "$CALLS"',
    "systemctl": 'echo "systemctl $*" >> "$CALLS"',
    "mount": 'echo "mount $*" >> "$CALLS"',
    "dpkg": 'echo "dpkg $*" >> "$CALLS"; exit "${DPKG_EXIT:-0}"',
    # / and /boot/firmware exist and are read-only; /boot is not a mount.
    "findmnt": """
case "$*" in
  "-no OPTIONS /"|"-no OPTIONS /boot/firmware") echo "${MOUNT_MODE:-ro},noatime" ;;
  "/boot/firmware") exit 0 ;;
  *) exit 1 ;;
esac
""",
    "df": 'echo "Filesystem 1024-blocks Used Available Capacity Mounted on"; '
    'echo "/dev/root 4000000 1000000 ${FREE_KB:-2000000} 50% /"',
    "apt-get": """
echo "apt-get $*" >> "$CALLS"
case " $* " in
  *" update "*) exit "${APT_UPDATE_EXIT:-0}" ;;
  *" -s "*)
    for ((i = 1; i <= ${PENDING:-2}; i++)); do echo "Inst pkg$i [1.0-$i] (1.1-$i Debian:13/stable [armhf])"; done
    echo "Conf pkg1 (1.1-1 Debian:13/stable [armhf])" ;;
  *" --print-uris "*)
    for ((i = 1; i <= ${PENDING:-2}; i++)); do echo "'http://deb/pkg$i.deb' pkg$i.deb ${DEB_BYTES:-1048576} SHA256:x"; done ;;
  *" upgrade "*) exit "${APT_UPGRADE_EXIT:-0}" ;;
esac
""",
}


@pytest.fixture
def board(tmp_path: Path) -> dict[str, str]:
    commands = tmp_path / "bin"
    commands.mkdir()
    for name, body in STUBS.items():
        path = commands / name
        path.write_text(f"#!/bin/bash\n{body}\n")
        path.chmod(0o755)
    for directory in ("mutable", "etc", "lib/systemd/system", "lib/tmpfiles.d"):
        (tmp_path / directory).mkdir(parents=True)
    return {
        **os.environ,
        "PATH": f"{commands}:{os.environ['PATH']}",
        "CALLS": str(tmp_path / "calls"),
        "OS_UPDATE_SKIP_ROOT_CHECK": "true",
        "OS_UPDATE_LOCK_FILE": str(tmp_path / "run/rootfs.lock"),
        "OS_UPDATE_STATE_DIR": str(tmp_path / "mutable"),
        "OS_UPDATE_WORK_DIR": str(tmp_path / "work"),
        "OS_UPDATE_CHECK_DIR": str(tmp_path / "check"),
        "OS_UPDATE_ETC_DIR": str(tmp_path / "etc"),
        "OS_UPDATE_LIB_DIR": str(tmp_path / "lib"),
        "OS_UPDATE_SELF": "/root/bin/os-update.sh",
    }


def run(env: dict[str, str], *args: str) -> tuple[subprocess.CompletedProcess[str], list[str]]:
    result = subprocess.run(  # noqa: S603
        [BASH, str(SCRIPT), *args], env=env, capture_output=True, text=True, check=False
    )
    calls = Path(env["CALLS"])
    return result, calls.read_text().splitlines() if calls.exists() else []


def is_real_upgrade(call: str) -> bool:
    """The upgrade itself, not the simulation or the download-size query."""
    return call.endswith(" upgrade") and " -s " not in call and "--print-uris" not in call


def index(calls: list[str], prefix: str) -> int:
    return next(i for i, call in enumerate(calls) if call.startswith(prefix))


def test_apply_remounts_under_the_shared_lock_and_reboots(board: dict[str, str]) -> None:
    result, calls = run(board, "apply")
    assert result.returncode == 0, result.stdout + result.stderr

    # The lock the backend takes around its own remounts comes first.
    assert calls[0].startswith("flock -w 600 -x")
    assert index(calls, "mount -o remount,rw /") < index(calls, "dpkg --configure -a")
    assert "mount -o remount,rw /boot/firmware" in calls
    upgrade = next(call for call in calls if is_real_upgrade(call))
    assert "--force-confold" in upgrade
    assert "Upgrade-Allow-New=true" in upgrade

    # Read-only again, lock released, and only then the reboot.
    tail = calls[index(calls, upgrade) :]
    assert index(tail, "mount -o remount,ro /") < index(tail, "flock -u")
    assert "mount -o remount,ro /boot/firmware" in tail
    assert tail[-1] == "systemctl reboot"

    state = Path(board["OS_UPDATE_STATE_DIR"])
    assert not (state / "os-update.in-progress").exists()
    assert "installed 2 package(s)" in (state / "os-update.log").read_text()
    assert not Path(board["OS_UPDATE_WORK_DIR"]).exists()
    unit = Path(board["OS_UPDATE_ETC_DIR"]) / "systemd/system/teslausb-os-update-repair.service"
    assert "ExecStart=/root/bin/os-update.sh repair" in unit.read_text()
    assert "systemctl enable teslausb-os-update-repair.service" in calls


def test_apply_leaves_a_writable_root_writable_and_can_skip_the_reboot(
    board: dict[str, str],
) -> None:
    result, calls = run({**board, "MOUNT_MODE": "rw"}, "apply", "--no-reboot")
    assert result.returncode == 0, result.stdout + result.stderr
    assert not any(call.startswith("mount ") for call in calls)
    assert "systemctl reboot" not in calls


def test_apply_refuses_when_the_card_is_too_full(board: dict[str, str]) -> None:
    # 2 packages x 100 MB download: about 950 MB wanted, 500 MB free.
    env = {**board, "DEB_BYTES": str(100 * 1024 * 1024), "FREE_KB": str(500 * 1024)}
    result, calls = run(env, "apply")
    assert result.returncode == 1
    assert "not enough free space" in result.stdout
    assert not any(is_real_upgrade(call) for call in calls)
    assert not (Path(board["OS_UPDATE_STATE_DIR"]) / "os-update.in-progress").exists()
    assert "mount -o remount,ro /" in calls
    assert "systemctl reboot" not in calls


def test_failed_upgrade_keeps_the_marker_for_the_boot_time_repair(board: dict[str, str]) -> None:
    result, calls = run({**board, "APT_UPGRADE_EXIT": "100"}, "apply")
    assert result.returncode == 1
    marker = Path(board["OS_UPDATE_STATE_DIR"]) / "os-update.in-progress"
    assert marker.exists()
    assert "mount -o remount,ro /" in calls
    assert "systemctl reboot" not in calls

    Path(board["CALLS"]).unlink()
    result, calls = run(board, "repair")
    assert result.returncode == 0, result.stdout + result.stderr
    assert index(calls, "mount -o remount,rw /") < index(calls, "dpkg --configure -a")
    assert not marker.exists()
    assert calls[-1].startswith("flock -u")


def test_repair_does_nothing_without_an_interrupted_update(board: dict[str, str]) -> None:
    result, calls = run(board, "repair")
    assert result.returncode == 0
    assert calls == []


def test_nothing_pending_changes_no_packages(board: dict[str, str]) -> None:
    result, calls = run({**board, "PENDING": "0"}, "apply")
    assert result.returncode == 0
    assert "nothing to do" in result.stdout
    assert not any(is_real_upgrade(call) for call in calls)
    assert "systemctl reboot" not in calls


def test_setup_edits_to_package_files_are_kept_in_etc(board: dict[str, str]) -> None:
    lib, etc = Path(board["OS_UPDATE_LIB_DIR"]), Path(board["OS_UPDATE_ETC_DIR"])
    # As setup leaves them on a read-only board ...
    (etc / "fake-hwclock.data").symlink_to("/mutable/etc/fake-hwclock.data")
    unit = lib / "systemd/system/fake-hwclock.service"
    unit.write_text("[Unit]\nAfter=mutable.mount\n[Service]\nExecStart=/sbin/fake-hwclock load\n")
    var_conf = lib / "tmpfiles.d/var.conf"
    var_conf.write_text("d /var/log 0755 - - -\nd /var/spool 1777 - - -\n")
    result, _ = run(board, "apply", "--no-reboot")
    assert result.returncode == 0, result.stdout + result.stderr

    # ... and as a package upgrade then resets them.
    unit.write_text("[Unit]\nBefore=sysinit.target\n[Service]\nExecStart=/sbin/fake-hwclock load\n")
    var_conf.write_text("d /var/log 0755 - - -\nd /var/spool 0755 - - -\n")
    result, _ = run(board, "apply", "--no-reboot")
    assert result.returncode == 0, result.stdout + result.stderr
    override = (etc / "systemd/system/fake-hwclock.service").read_text()
    assert "After=mutable.mount" in override and "Before=" not in override
    assert "d /var/spool 1777 - - -" in (etc / "tmpfiles.d/var.conf").read_text()


def test_boards_setup_did_not_edit_get_no_overrides(board: dict[str, str]) -> None:
    lib, etc = Path(board["OS_UPDATE_LIB_DIR"]), Path(board["OS_UPDATE_ETC_DIR"])
    (lib / "systemd/system/fake-hwclock.service").write_text("[Unit]\nBefore=sysinit.target\n")
    (lib / "tmpfiles.d/var.conf").write_text("d /var/spool 0755 - - -\n")
    result, _ = run(board, "apply", "--no-reboot")
    assert result.returncode == 0, result.stdout + result.stderr
    assert not (etc / "systemd/system/fake-hwclock.service").exists()
    assert not (etc / "tmpfiles.d/var.conf").exists()


def test_check_lists_updates_without_touching_the_card(board: dict[str, str]) -> None:
    result, calls = run({**board, "PENDING": "3"}, "check")
    assert result.returncode == 0, result.stdout + result.stderr
    assert "3 package update(s) available" in result.stdout
    assert "pkg2" in result.stdout and "1.0-2 -> 1.1-2" in result.stdout
    assert all(call.startswith("apt-get ") for call in calls)
    assert all(f"Dir::State::lists={board['OS_UPDATE_CHECK_DIR']}/lists" in call for call in calls)
    assert not Path(board["OS_UPDATE_CHECK_DIR"]).exists()

    result, _ = run({**board, "PENDING": "0"}, "check")
    assert "up to date" in result.stdout

    result, _ = run({**board, "APT_UPDATE_EXIT": "100"}, "check")
    assert result.returncode == 1
    assert "could not reach the package servers" in result.stderr


def test_setup_and_releases_install_the_command() -> None:
    assert (
        'copy_script run/os-update.sh "$install_path"'
        in (ROOT / "setup/pi/configure.sh").read_text()
    )
    assert "os-update.sh" in (ROOT / "tools/build-release.sh").read_text()
