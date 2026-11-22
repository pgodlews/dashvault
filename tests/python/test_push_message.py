import json
import os
import re
import subprocess
from pathlib import Path


def test_push_message_reads_sd_data_from_mutable(tmp_path: Path) -> None:
    source_path = Path(__file__).resolve().parents[2] / "run/send-push-message"
    source = source_path.read_text()
    assert "/root/sdcard" not in source
    match = re.search(r"function _sd_stats_line \(\) \{.*?\n\}", source, re.DOTALL)
    assert match is not None

    (tmp_path / "sdcard_write_stats.json").write_text(
        json.dumps({"total_bytes_written": 11000000000})
    )
    (tmp_path / "sdcard_benchmark.json").write_text(
        json.dumps(
            {
                "results": [{"write_mbps": 10.9, "rating": "slow"}],
            }
        )
    )
    result = subprocess.run(
        ["bash"],
        input=match.group() + "\n_sd_stats_line\n",
        text=True,
        capture_output=True,
        env={**os.environ, "MUTABLE_DIR": str(tmp_path)},
    )

    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == "SD: 0.01 TB written | 10.9 MB/s (slow)"


def test_push_message_loads_telegram_env_and_sends_standalone(tmp_path: Path) -> None:
    script = Path(__file__).resolve().parents[2] / "run/send-push-message"
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    curl_log = tmp_path / "curl.log"
    fake_curl = bin_dir / "curl"
    fake_curl.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$CURL_LOG"\n')
    fake_curl.chmod(0o755)

    env_file = tmp_path / "teslausb_telegram.env"
    env_file.write_text(
        "TELEGRAM_BOT_TOKEN=123456:token\n"
        "TELEGRAM_CHAT_ID=-100999,@mychannel\n"
        "TELEGRAM_SILENT_NOTIFY=true\n"
    )

    result = subprocess.run(  # noqa: S603, S607
        ["bash", str(script), "TeslaUSB", "Archived 2 clip(s).", "finish"],
        text=True,
        capture_output=True,
        env={
            **os.environ,
            "PATH": f"{bin_dir}:{os.environ['PATH']}",
            "CURL_LOG": str(curl_log),
            "MUTABLE_DIR": str(tmp_path),
            "TESLAUSB_TELEGRAM_ENV": str(env_file),
            "ARCHIVE_LOG": str(tmp_path / "archiveloop.log"),
        },
    )

    assert result.returncode == 0, result.stderr
    calls = curl_log.read_text()
    assert "https://api.telegram.org/bot123456:token/sendMessage" in calls
    assert '"chat_id":-100999' in calls
    assert '"chat_id":"@mychannel"' in calls
    assert '"disable_notification":true' in calls


def test_push_message_title_has_one_colon(tmp_path: Path) -> None:
    script = Path(__file__).resolve().parents[2] / "run/send-push-message"
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    curl_log = tmp_path / "curl.log"
    fake_curl = bin_dir / "curl"
    fake_curl.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$CURL_LOG"\n')
    fake_curl.chmod(0o755)
    env_file = tmp_path / "teslausb_telegram.env"
    env_file.write_text("TELEGRAM_BOT_TOKEN=123456:token\nTELEGRAM_CHAT_ID=123456\n")

    for title in ("dashvault:", "dashvault"):
        curl_log.unlink(missing_ok=True)
        result = subprocess.run(  # noqa: S603
            ["bash", str(script), title, "Archived 2 files in 5 seconds", "start"],  # noqa: S607
            text=True,
            capture_output=True,
            env={
                **os.environ,
                "PATH": f"{bin_dir}:{os.environ['PATH']}",
                "CURL_LOG": str(curl_log),
                "MUTABLE_DIR": str(tmp_path),
                "TESLAUSB_TELEGRAM_ENV": str(env_file),
                "ARCHIVE_LOG": str(tmp_path / "archiveloop.log"),
            },
        )

        assert result.returncode == 0, result.stderr
        assert '"text":"dashvault: Archived 2 files in 5 seconds' in curl_log.read_text()
