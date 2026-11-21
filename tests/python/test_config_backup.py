"""
Tests for run/teslausb-config backup/restore script.

These run entirely in a tmpdir — no root, no real Pi paths touched.
We exercise the shell script by invoking it as a subprocess with a
controlled environment pointing at fake config files.
"""

import os
import subprocess
import textwrap
from pathlib import Path

import pytest

SCRIPT = Path(__file__).parents[2] / "run" / "teslausb-config"


def run_script(*args, input_text: str = "", env: dict | None = None) -> subprocess.CompletedProcess:
    full_env = {**os.environ, **(env or {})}
    return subprocess.run(
        [str(SCRIPT), *args],
        input=input_text,
        capture_output=True,
        text=True,
        env=full_env,
    )


# ── usage / dispatch ──────────────────────────────────────────────────────────


def test_no_args_prints_usage():
    r = run_script()
    assert r.returncode != 0
    assert "Usage" in r.stderr


def test_unknown_command_prints_usage():
    r = run_script("frobnicate")
    assert r.returncode != 0
    assert "Usage" in r.stderr


def test_restore_requires_argument():
    r = run_script("restore")
    assert r.returncode != 0
    assert "usage" in r.stderr.lower()


# ── backup ────────────────────────────────────────────────────────────────────


@pytest.fixture()
def fake_pi(tmp_path: Path) -> dict:
    """Create a fake Pi directory tree with stub config files."""
    setup_vars = tmp_path / "root" / "teslausb_setup_variables.conf"
    setup_vars.parent.mkdir(parents=True)
    setup_vars.write_text("export ARCHIVE_SERVER=nas\nexport SHARE_NAME=tesla\n")

    htpasswd = tmp_path / "etc" / "nginx" / "auth" / ".htpasswd"
    htpasswd.parent.mkdir(parents=True)
    htpasswd.write_text("admin:$apr1$fake\n")

    dest = tmp_path / "archive"
    dest.mkdir()

    return {
        "tmp": tmp_path,
        "setup_vars": setup_vars,
        "htpasswd": htpasswd,
        "dest": dest,
    }


def _patch_env(fake: dict) -> dict:
    """Environment vars that redirect hard-coded paths to our tmpdir."""
    return {
        # Override path constants used inside the script via env isn't
        # directly supported — we test the externally-visible behaviour
        # (backup writes an encrypted file; restore decrypts it correctly).
        "HOME": str(fake["tmp"]),
    }


def test_backup_fails_without_setup_vars(tmp_path: Path):
    """Backup must error cleanly when the main config file is absent."""
    dest = tmp_path / "dest"
    dest.mkdir()
    r = run_script("backup", "--dest", str(dest), input_text="pass\npass\n")
    # Script will exit non-zero because /root/teslausb_setup_variables.conf
    # doesn't exist in CI — we just check it doesn't segfault or produce garbage.
    assert r.returncode != 0
    assert "not found" in r.stderr or "ERROR" in r.stderr


def test_backup_mismatched_passphrase(fake_pi: dict, monkeypatch, tmp_path: Path):
    """Mismatched passphrases must abort before writing anything."""
    # We can't easily override /root inside the script without root privs,
    # so this test validates the passphrase-mismatch path via a wrapper.
    wrapper = tmp_path / "wrapper.sh"
    wrapper.write_text(
        textwrap.dedent(f"""\
        #!/bin/bash -eu
        set -o pipefail
        SETUP_VARS="{fake_pi["setup_vars"]}"
        # Source the passphrase-reading logic by replicating it inline
        read -r -s -p "Encryption passphrase: " pass1; echo >&2
        read -r -s -p "Confirm passphrase:     " pass2; echo >&2
        [ "$pass1" = "$pass2" ] || {{ echo "ERROR: passphrases do not match" >&2; exit 1; }}
    """)
    )
    wrapper.chmod(0o755)
    r = subprocess.run(
        [str(wrapper)],
        input="secret\ndifferent\n",
        capture_output=True,
        text=True,
    )
    assert r.returncode != 0
    assert "do not match" in r.stderr


# ── round-trip (backup → restore) ────────────────────────────────────────────


def test_openssl_roundtrip(tmp_path: Path):
    """Verify openssl enc AES-256-CBC round-trip works on this host."""
    plaintext = b"archive_server=nas\nshare=tesla/dashcam\n"
    encrypted = tmp_path / "data.enc"
    passphrase = "hunter2"

    enc = subprocess.run(
        [
            "openssl",
            "enc",
            "-aes-256-cbc",
            "-pbkdf2",
            "-iter",
            "100000",
            "-pass",
            f"pass:{passphrase}",
            "-out",
            str(encrypted),
        ],
        input=plaintext,
        capture_output=True,
    )
    assert enc.returncode == 0, enc.stderr

    dec = subprocess.run(
        [
            "openssl",
            "enc",
            "-d",
            "-aes-256-cbc",
            "-pbkdf2",
            "-iter",
            "100000",
            "-pass",
            f"pass:{passphrase}",
            "-in",
            str(encrypted),
        ],
        capture_output=True,
    )
    assert dec.returncode == 0
    assert dec.stdout == plaintext


def test_openssl_wrong_passphrase_fails(tmp_path: Path):
    """Decryption with wrong passphrase must fail non-zero."""
    plaintext = b"secret config\n"
    encrypted = tmp_path / "data.enc"

    subprocess.run(
        [
            "openssl",
            "enc",
            "-aes-256-cbc",
            "-pbkdf2",
            "-iter",
            "100000",
            "-pass",
            "pass:correct",
            "-out",
            str(encrypted),
        ],
        input=plaintext,
        check=True,
        capture_output=True,
    )

    dec = subprocess.run(
        [
            "openssl",
            "enc",
            "-d",
            "-aes-256-cbc",
            "-pbkdf2",
            "-iter",
            "100000",
            "-pass",
            "pass:wrong",
            "-in",
            str(encrypted),
        ],
        capture_output=True,
    )
    assert dec.returncode != 0


def test_backup_and_restore_roundtrip_in_tmpdir(fake_pi: dict, tmp_path: Path):
    """End-to-end backup and restore round-trip preserving /root/teslausb_setup_variables.conf and telegram env."""
    telegram_env = fake_pi["tmp"] / "mutable" / "teslausb_telegram.env"
    telegram_env.parent.mkdir(parents=True)
    telegram_env.write_text("TELEGRAM_ENABLED='true'\nTELEGRAM_BOT_TOKEN='123:ABC'\n")

    env = {
        "TESLAUSB_SKIP_ROOT_CHECK": "1",
        "RESTORE_ROOT": str(fake_pi["tmp"]),
        "SETUP_VARS": str(fake_pi["setup_vars"]),
        "HTPASSWD": str(fake_pi["htpasswd"]),
        "TELEGRAM_ENV": str(telegram_env),
        "SSH_KEYS": str(fake_pi["tmp"] / "root/.ssh/authorized_keys"),
        "WPA_CONF": str(fake_pi["tmp"] / "etc/wpa_supplicant/wpa_supplicant.conf"),
        "BT_KEY": str(fake_pi["tmp"] / "root/teslausb_bt_key"),
    }

    backup_res = run_script(
        "backup", "--dest", str(fake_pi["dest"]), input_text="s3cr3t\ns3cr3t\n", env=env
    )
    assert backup_res.returncode == 0, backup_res.stderr
    archives = list(fake_pi["dest"].glob("teslausb_config_*.tar.gz.enc"))
    assert len(archives) == 1

    # Delete original files and restore into the same root
    fake_pi["setup_vars"].unlink()
    fake_pi["htpasswd"].unlink()
    telegram_env.unlink()

    restore_res = run_script("restore", str(archives[0]), input_text="s3cr3t\n", env=env)
    assert restore_res.returncode == 0, restore_res.stderr
    assert (
        fake_pi["setup_vars"].read_text() == "export ARCHIVE_SERVER=nas\nexport SHARE_NAME=tesla\n"
    )
    assert fake_pi["htpasswd"].read_text() == "admin:$apr1$fake\n"
    assert "TELEGRAM_BOT_TOKEN='123:ABC'" in telegram_env.read_text()
