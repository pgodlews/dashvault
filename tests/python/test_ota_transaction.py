import os
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
TRANSACTION = ROOT / "run/ota_transaction.sh"
BASH = (
    "/opt/homebrew/bin/bash"
    if Path("/opt/homebrew/bin/bash").exists()
    else (shutil.which("bash") or "/bin/bash")
)


@pytest.mark.parametrize(("initial_mode", "exit_code"), [("ro", 0), ("ro", 7), ("rw", 0)])
def test_ota_transaction_restores_original_root_state(
    tmp_path: Path, initial_mode: str, exit_code: int
) -> None:
    commands = tmp_path / "bin"
    commands.mkdir()
    mount_log = tmp_path / "mount.log"
    for name in ("flock", "findmnt", "mount"):
        path = commands / name
        if name == "findmnt":
            body = f"echo '{initial_mode},noatime'"
        elif name == "mount":
            body = 'echo "$*" >> "$MOUNT_LOG"'
        else:
            body = "exit 0"
        path.write_text(f"#!/bin/bash\n{body}\n")
        path.chmod(0o755)

    script = f'source "{TRANSACTION}"; ota_begin_transaction; exit {exit_code}'
    result = subprocess.run(  # noqa: S603
        [BASH, "-c", script],
        env={
            **os.environ,
            "PATH": f"{commands}:{os.environ['PATH']}",
            "OTA_LOCK_FILE": str(tmp_path / "rootfs.lock"),
            "MOUNT_LOG": str(mount_log),
        },
        capture_output=True,
        text=True,
    )
    assert result.returncode == exit_code, result.stderr
    operations = mount_log.read_text().splitlines()
    assert operations[0] == "-o remount,rw /"
    if initial_mode == "ro":
        assert operations[-1] == "-o remount,ro /"
    else:
        assert len(operations) == 1


def test_ota_scripts_begin_transaction_before_logging_or_writes() -> None:
    for name in ("ota_install.sh", "ota_rollback.sh"):
        source = (ROOT / "run" / name).read_text()
        assert 'source "${OTA_TRANSACTION_LIB:-/root/bin/ota_transaction.sh}"' in source
        assert source.index("ota_begin_transaction") < source.index("log()")
        assert "/run/teslausb/rootfs.lock" in TRANSACTION.read_text()


def test_release_and_ota_install_include_provisioning_and_skip_backend_in_bin() -> None:
    # The workflow delegates to the build script, which defines the contents.
    assert "tools/build-release.sh" in (ROOT / ".github/workflows/release.yml").read_text()
    release_yml = (ROOT / "tools/build-release.sh").read_text()
    assert "provisioning_config.py" in release_yml
    assert 'setup/pi/teslausb-telegram-bot.service "$STAGING/units/"' in release_yml
    assert "reconfigure-archive.sh" in release_yml

    ota_install = (ROOT / "run/ota_install.sh").read_text()
    assert "teslausb-backend.arm64|teslausb-backend.armv7|teslausb-backend.armv6" in ota_install
    assert "armv6l)" in ota_install
    assert "teslausb-backend.arm64" in release_yml
    assert "teslausb-backend.armv7" in release_yml
    assert "teslausb-backend.armv6" in release_yml
    assert "qemu-backend.sh" in (ROOT / ".github/workflows/ci.yml").read_text()
    assert 'INSTALL_PROVISIONING="${OTA_INSTALL_PROVISIONING:-/root/provisioning}"' in ota_install


@pytest.mark.parametrize("active", [True, False])
@pytest.mark.parametrize("local_override", [True, False])
@pytest.mark.parametrize("legacy_installer", [True, False])
def test_ota_upgrades_and_restores_telegram_unit(
    tmp_path: Path, active: bool, local_override: bool, legacy_installer: bool
) -> None:
    commands = tmp_path / "commands"
    commands.mkdir()
    log = tmp_path / "services.log"
    for name, body in {
        "systemctl": 'echo "$*" >> "$SERVICE_LOG"\nif [ "$1" = is-active ]; then exit "$ACTIVE_EXIT"; fi',
        "mount": "exit 0",
        "findmnt": "echo ro",
        "flock": "exit 0",
        "sleep": "exit 0",
    }.items():
        target = commands / name
        target.write_text(f"#!/bin/bash\n{body}\n")
        target.chmod(0o755)
    paths = {
        key: tmp_path / key.lower()
        for key in (
            "BACKUP_ROOT",
            "INSTALL_BIN",
            "INSTALL_PROVISIONING",
            "INSTALL_WWW",
            "INSTALL_UNITS",
            "OVERRIDE_UNITS",
        )
    }
    for path in paths.values():
        path.mkdir()
    unit_name = "teslausb-telegram-bot.service"
    old_unit = "[Service]\nPrivateTmp=yes\n"
    unit_dir = paths["OVERRIDE_UNITS"] if local_override else paths["INSTALL_UNITS"]
    (unit_dir / unit_name).write_text(old_unit)
    extract = tmp_path / "extract"
    (extract / "units").mkdir(parents=True)
    (extract / "VERSION").write_text("v1.2.3")
    corrected = (ROOT / "setup/pi" / unit_name).read_text()
    (extract / "units" / unit_name).write_text(corrected)
    env = {
        **os.environ,
        **{f"OTA_{key}": str(path) for key, path in paths.items()},
        "OTA_TRANSACTION_LIB": str(TRANSACTION),
        "OTA_LOCK_FILE": str(tmp_path / "root.lock"),
        "OTA_LOG_FILE": str(tmp_path / "update.log"),
        "OTA_VERSION_FILE": str(tmp_path / "version"),
        "OTA_INSTALL_BACKEND_BIN": str(tmp_path / "backend"),
        "PATH": f"{commands}:{os.environ['PATH']}",
        "SERVICE_LOG": str(log),
        "ACTIVE_EXIT": "0" if active else "3",
    }
    if legacy_installer:
        # Legacy apply has copied bin/ and saved the previous runtime, but left
        # the old unit and bot untouched. The new backend calls this finalizer.
        backup = paths["BACKUP_ROOT"] / "2026-11-08T010000"
        (backup / "bin").mkdir(parents=True)
        env["OTA_PACKAGED_TELEGRAM_UNIT"] = str(extract / "units" / unit_name)
    for script, args in (("ota_install.sh", [str(extract)]), ("ota_rollback.sh", [])):
        command_script = (
            "ota_finalize.sh" if legacy_installer and script == "ota_install.sh" else script
        )
        command_args = [] if command_script == "ota_finalize.sh" else args
        result = subprocess.run(  # noqa: S603 — repository scripts only
            [BASH, "-eu", str(ROOT / "run" / command_script), *command_args],
            env=env,
            capture_output=True,
            text=True,
        )
        assert result.returncode == 0, result.stdout + result.stderr
        assert (unit_dir / unit_name).read_text() == (
            corrected if script == "ota_install.sh" else old_unit
        )
        actions = log.read_text().splitlines()
        assert "daemon-reload" in actions
        assert (f"stop {unit_name}" in actions) == active
        assert (f"start {unit_name}" in actions) == active
        assert not any(action.startswith(("enable ", "disable ")) for action in actions)
        if command_script == "ota_finalize.sh":
            repeated = subprocess.run(  # noqa: S603 — repository script only
                [BASH, "-eu", str(ROOT / "run/ota_finalize.sh")],
                env=env,
                capture_output=True,
                text=True,
            )
            assert repeated.returncode == 0, repeated.stderr
            assert log.read_text().splitlines() == actions  # idempotent startup
        log.write_text("")


def _ota_commands(tmp_path: Path) -> Path:
    commands = tmp_path / "commands"
    commands.mkdir()
    for name, body in {
        "systemctl": 'echo "$*" >> "$SERVICE_LOG"',
        "mount": "exit 0",
        "findmnt": "echo ro",
        "flock": "exit 0",
        "sleep": "exit 0",
        "uname": 'echo "$FAKE_UNAME"',
    }.items():
        path = commands / name
        path.write_text(f"#!/bin/bash\n{body}\n")
        path.chmod(0o755)
    return commands


def test_failed_ota_restarts_services_and_stops_the_backend_socket(tmp_path: Path) -> None:
    commands = _ota_commands(tmp_path)
    log = tmp_path / "services.log"
    extract = tmp_path / "extract"
    (extract / "bin").mkdir(parents=True)
    (extract / "VERSION").write_text("v9")
    (extract / "bin" / "teslausb-backend.arm64").write_bytes(b"new")
    env = {
        **os.environ,
        "PATH": f"{commands}:{os.environ['PATH']}",
        "OTA_TRANSACTION_LIB": str(TRANSACTION),
        "OTA_LOCK_FILE": str(tmp_path / "root.lock"),
        "OTA_LOG_FILE": str(tmp_path / "update.log"),
        "OTA_VERSION_FILE": str(tmp_path / "version"),
        "OTA_BACKUP_ROOT": str(tmp_path / "backups"),
        "OTA_INSTALL_BIN": str(tmp_path / "bin"),
        "OTA_INSTALL_PROVISIONING": str(tmp_path / "provisioning"),
        "OTA_INSTALL_WWW": str(tmp_path / "www"),
        "OTA_INSTALL_UNITS": str(tmp_path / "units"),
        "OTA_INSTALL_BACKEND_BIN": str(tmp_path / "missing-dir" / "teslausb-backend"),
        "SERVICE_LOG": str(log),
        "FAKE_UNAME": "aarch64",
    }
    result = subprocess.run(  # noqa: S603
        [BASH, "-eu", str(ROOT / "run/ota_install.sh"), str(extract)],
        env=env,
        capture_output=True,
        text=True,
    )
    assert result.returncode != 0, result.stdout + result.stderr
    actions = log.read_text().splitlines()
    assert actions.index("stop teslausb-backend.socket") < actions.index("stop teslausb-backend")
    assert "start teslausb-backend.socket" in actions
    assert "start teslausb" in actions


def test_rollback_without_a_backup_reports_the_error(tmp_path: Path) -> None:
    commands = _ota_commands(tmp_path)
    update_log = tmp_path / "update.log"
    env = {
        **os.environ,
        "PATH": f"{commands}:{os.environ['PATH']}",
        "OTA_TRANSACTION_LIB": str(TRANSACTION),
        "OTA_LOCK_FILE": str(tmp_path / "root.lock"),
        "OTA_LOG_FILE": str(update_log),
        "OTA_BACKUP_ROOT": str(tmp_path / "backups"),
        "OTA_INSTALL_BIN": str(tmp_path / "bin"),
        "OTA_INSTALL_PROVISIONING": str(tmp_path / "provisioning"),
        "OTA_INSTALL_WWW": str(tmp_path / "www"),
        "OTA_VERSION_FILE": str(tmp_path / "version"),
        "SERVICE_LOG": str(tmp_path / "services.log"),
        "FAKE_UNAME": "aarch64",
    }
    (tmp_path / "backups").mkdir()
    result = subprocess.run(  # noqa: S603
        [BASH, "-eu", str(ROOT / "run/ota_rollback.sh")],
        env=env,
        capture_output=True,
        text=True,
    )
    assert result.returncode != 0
    assert "no backup found" in update_log.read_text()
