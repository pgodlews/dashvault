import json
import os
import re
import subprocess
from pathlib import Path


def test_push_message_scripts_install_into_legacy_runtime(tmp_path: Path) -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "setup/pi/configure.sh").read_text()
    match = re.search(r"function install_push_message_scripts\(\) \{.*?\n\}", source, re.DOTALL)
    assert match is not None

    install_path = tmp_path / "root/bin"
    install_path.mkdir(parents=True)
    stubs = """
set -euo pipefail
copy_script() {
    cp "$REPO_ROOT/$1" "$2/"
}
"""
    result = subprocess.run(
        ["bash", "-s", "--", str(install_path)],
        input=stubs + match.group() + '\ninstall_push_message_scripts "$1"\n',
        text=True,
        capture_output=True,
        env={**os.environ, "REPO_ROOT": str(repo_root)},
    )

    assert result.returncode == 0, result.stderr
    assert (install_path / "send-push-message").is_file()
    assert (install_path / "teslausb_telegram_bot.py").is_file()
    assert (install_path / "lib/paths.sh").is_file()


def test_readonly_setup_does_not_replace_working_time_service() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "setup/pi/make-root-fs-readonly.sh").read_text()

    assert "install ntp busybox-syslogd" not in source
    assert "install busybox-syslogd" in source


def test_readonly_setup_allows_unset_access_point_ssid() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "setup/pi/make-root-fs-readonly.sh").read_text()

    assert '[ -n "${AP_SSID:-}" ]' in source


def test_one_time_cleanup_does_not_require_interactive_log_function() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "setup/pi/once/clean_sentry_files_archived").read_text()

    assert '\nlog "Fixing previously-archived list"' not in source
    assert '\necho "Fixing previously-archived list"' in source


def test_one_time_cleanup_counter_survives_first_entry() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "setup/pi/once/clean_sentry_files_archived").read_text()

    assert "(( current++ ))" not in source
    assert "(( ++current ))" in source


def test_one_time_scripts_do_not_require_caller_functions() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    for script in (repo_root / "setup/pi/once").iterdir():
        if script.is_file():
            source = script.read_text()
            assert not re.search(r"^(?:log|setup_progress) ", source, re.MULTILINE), script.name


def test_web_install_includes_sdcard_tools() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "setup/pi/configure-web.sh").read_text()

    assert "copy_script run/benchmark.sh /root/bin" in source
    assert "copy_script run/sdcard_info.sh /root/bin" in source


def test_benchmark_writes_to_sd_backed_mutable_partition() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "run/benchmark.sh").read_text()

    assert 'TEST_PREFIX="${BENCHMARK_TEST_FILE:-/mutable/teslausb_benchmark_test}"' in source
    assert "/var/tmp/teslausb_benchmark_test" not in source
    assert "dd if=/dev/zero" in source
    assert "/dev/urandom" not in source


def test_music_archive_mount_is_disabled_without_music_drive() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    paths = repo_root / "run/lib/paths.sh"
    result = subprocess.run(
        ["bash", "-c", f'source "{paths}"; printf "%s" "$MUSIC_ARCHIVE_MOUNT"'],
        text=True,
        capture_output=True,
    )

    assert result.returncode == 0, result.stderr
    assert result.stdout == ""


def test_telegram_service_is_installed_while_disabled(tmp_path: Path) -> None:
    repo_root = Path(__file__).resolve().parents[2]
    source = (repo_root / "setup/pi/configure.sh").read_text()
    match = re.search(r"function install_telegram_bot_service\(\) \{.*?\n\}", source, re.DOTALL)
    assert match is not None
    trace = tmp_path / "trace"
    stubs = """
set -euo pipefail
SOURCE_DIR=/source
log_progress() { :; }
install() { printf 'install\n' >> "$TRACE"; }
systemctl() { printf '%s\n' "$1" >> "$TRACE"; }
"""
    result = subprocess.run(
        ["bash"],
        input=stubs + match.group() + "\nTELEGRAM_ENABLED=false\ninstall_telegram_bot_service\n",
        text=True,
        capture_output=True,
        env={**os.environ, "TRACE": str(trace)},
    )

    assert result.returncode == 0, result.stderr
    assert trace.read_text().splitlines() == ["install", "daemon-reload"]


def run_benchmark_with_timeout_status(tmp_path: Path, status: int) -> dict:
    repo_root = Path(__file__).resolve().parents[2]
    commands = tmp_path / "bin"
    commands.mkdir()
    timeout = commands / "timeout"
    timeout.write_text('#!/bin/bash\nexit "$FAKE_TIMEOUT_STATUS"\n')
    timeout.chmod(0o755)
    results = tmp_path / "results.json"
    result = subprocess.run(
        ["bash", str(repo_root / "run/benchmark.sh")],
        env={
            **os.environ,
            "PATH": f"{commands}:{os.environ['PATH']}",
            "FAKE_TIMEOUT_STATUS": str(status),
            "BENCHMARK_RESULTS_FILE": str(results),
            "BENCHMARK_TEST_FILE": str(tmp_path / "test-file"),
        },
        text=True,
        capture_output=True,
    )
    assert result.returncode == 0, result.stderr
    return json.loads(results.read_text())["results"][0]


def test_benchmark_preserves_timeout_status(tmp_path: Path) -> None:
    result = run_benchmark_with_timeout_status(tmp_path, 124)
    assert result["timed_out"] is True
    assert result["rating"] == "slow"


def test_benchmark_distinguishes_write_error_from_timeout(tmp_path: Path) -> None:
    result = run_benchmark_with_timeout_status(tmp_path, 1)
    assert result["rating"] == "unknown"
    assert "timed_out" not in result
    assert result["error"] == "dd failed"
