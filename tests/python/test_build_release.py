"""tools/build-release.sh: what the two release archives contain.

The script is run for real, with stand-ins for the compiled backend and web
app, and the archives it writes are opened.
"""

from __future__ import annotations

import hashlib
import os
import re
import shutil
import subprocess
import tarfile
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "tools/build-release.sh"
BASH = (
    "/opt/homebrew/bin/bash"
    if Path("/opt/homebrew/bin/bash").exists()
    else (shutil.which("bash") or "/bin/bash")
)


def names(archive: Path) -> set[str]:
    with tarfile.open(archive) as tar:
        return {member.name.removeprefix("./") for member in tar.getmembers() if member.isfile()}


@pytest.fixture(scope="module")
def release(tmp_path_factory: pytest.TempPathFactory) -> Path:
    if not (ROOT / ".git").exists():
        pytest.skip("needs a git checkout: only tracked files are packaged")
    work = tmp_path_factory.mktemp("release")
    backends, web = work / "backends", work / "web"
    backends.mkdir()
    (web / "assets").mkdir(parents=True)
    for arch in ("arm64", "armv7", "armv6"):
        (backends / f"teslausb-backend.{arch}").write_text(f"binary for {arch}")
    (web / "index.html").write_text("<html></html>")
    (web / "assets/app.js").write_text("// app")
    result = subprocess.run(  # noqa: S603
        [BASH, str(SCRIPT), "v9.8.7", str(work / "out")],
        env={**os.environ, "PREBUILT_BACKENDS": str(backends), "PREBUILT_UI": str(web)},
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    return work / "out"


def test_app_update_archive_has_what_the_installer_reads(release: Path) -> None:
    files = names(release / "teslausb-update.tar.gz")
    for expected in (
        "VERSION",
        "bin/archiveloop",
        "bin/os-update.sh",
        "bin/ota_install.sh",
        "bin/teslausb_telegram_bot.py",
        "bin/lib/paths.sh",
        "bin/lib/backends/cifs/archive-clips.sh",
        "bin/provisioning/provisioning_config.py",
        "bin/teslausb-backend.arm64",
        "bin/teslausb-backend.armv7",
        "bin/teslausb-backend.armv6",
        "units/teslausb-telegram-bot.service",
        "www/index.html",
        "www/assets/app.js",
    ):
        assert expected in files, expected


def test_every_script_setup_installs_is_also_in_the_app_update(release: Path) -> None:
    """A script setup copies to /root/bin but an update omits would go stale."""
    files = names(release / "teslausb-update.tar.gz")
    configure = (ROOT / "setup/pi/configure.sh").read_text()
    installed = set(re.findall(r'copy_script run/([A-Za-z0-9_.-]+) "\$install_path"', configure))
    # Present on the board but deliberately not replaced by an app update.
    not_updated = {"auto.teslausb", "force_sync.sh", "mountimage", "mountoptsforimage"}
    missing = {name for name in installed - not_updated if f"bin/{name}" not in files}
    assert not missing, f"setup installs these but the app update does not carry them: {missing}"


def test_release_bundle_has_everything_setup_copies(release: Path) -> None:
    files = names(release / "teslausb-release.tar.gz")
    assert "VERSION" in files
    for arch in ("arm64", "armv7", "armv6"):
        assert f"dist/teslausb-backend.{arch}" in files
    assert "teslausb-www/new/dist/index.html" in files
    assert "teslausb-www/new/dist/assets/app.js" in files

    setup_sources = "".join(
        path.read_text()
        for path in (ROOT / "setup").rglob("*")
        if path.is_file() and path.suffix in ("", ".sh")
    )
    wanted = set(
        re.findall(r"copy_script ((?:run|setup|pi-gen-sources)/[A-Za-z0-9_./-]+)", setup_sources)
    )
    wanted |= {
        "setup/pi/setup-teslausb",  # fetched by rc.local
        "setup/pi/teslausb-telegram-bot.service",
        "run/teslausb-config",
        "teslausb-www/teslausb.nginx",
        "teslausb-www/teslausb-backend.service",
        "teslausb-www/teslausb-backend.socket",
        "teslausb-www/cttseraser.cpp",
        "teslausb-www/html/favicon.ico",
        "tools/debian-resizefs.sh",
        "pi-gen-sources/00-teslausb-tweaks/files/teslausb_setup_variables.conf.sample",
    }
    assert len(wanted) > 40
    missing = sorted(path for path in wanted if path not in files)
    assert not missing, f"setup needs these but the release bundle lacks them: {missing}"
    for backend in ("cifs", "nfs", "rsync", "rclone", "none"):
        assert f"run/{backend}_archive/archive-clips.sh" in files


def test_archives_carry_the_version_and_only_tracked_files(release: Path) -> None:
    for archive in ("teslausb-update.tar.gz", "teslausb-release.tar.gz"):
        with tarfile.open(release / archive) as tar:
            version = tar.extractfile("./VERSION")
            assert version is not None and version.read().decode().strip() == "v9.8.7"
            assert {member.uid for member in tar.getmembers()} == {0}

    tracked = set(
        subprocess.run(  # noqa: S603
            ["git", "-C", str(ROOT), "ls-files"],  # noqa: S607
            capture_output=True,
            text=True,
            check=True,
        ).stdout.splitlines()
    )
    built = {
        "VERSION",
        "dist/teslausb-backend.arm64",
        "dist/teslausb-backend.armv7",
        "dist/teslausb-backend.armv6",
    }
    bundle = names(release / "teslausb-release.tar.gz")
    stray = {
        name
        for name in bundle - built
        if not name.startswith("teslausb-www/new/dist/") and name not in tracked
    }
    assert not stray, f"untracked files in the release bundle: {stray}"
    assert not any(".env" in name or "__pycache__" in name for name in bundle)

    sums = (release / "SHA256SUMS").read_text()
    assert "teslausb-update.tar.gz" in sums and "teslausb-release.tar.gz" in sums


def run_use_release(tmp_path: Path, **env: str) -> tuple[subprocess.CompletedProcess[str], Path]:
    source = (ROOT / "setup/generic/install.sh").read_text()
    match = re.search(r"^function use_release \{.*?^\}\n", source, re.DOTALL | re.MULTILINE)
    assert match
    commands = tmp_path / "bin"
    commands.mkdir(exist_ok=True)
    # The script targets Debian; macOS has a BSD sha256sum with a different -c.
    gnu = subprocess.run(  # noqa: S603
        [shutil.which("sha256sum") or "false", "--version"],
        capture_output=True,
        text=True,
        check=False,
    )
    if "coreutils" not in gnu.stdout:
        (commands / "sha256sum").write_text('#!/bin/bash\nexec shasum -a 256 "$@"\n')
        (commands / "sha256sum").chmod(0o755)
    target = tmp_path / "share" / "teslausb-release"
    script = (
        'error_exit() { echo "STOP: $*"; exit 1; }\n'
        f'RELEASE_DIR="{target}"\nBRANCH_INSTALL="${{BRANCH_INSTALL:-false}}"\n'
        + match.group(0)
        + 'use_release\necho "SOURCE_DIR=${SOURCE_DIR:-}"\n'
    )
    result = subprocess.run(  # noqa: S603
        [BASH, "-euc", script],
        env={**os.environ, "PATH": f"{commands}:{os.environ['PATH']}", **env},
        capture_output=True,
        text=True,
        check=False,
    )
    return result, target


@pytest.fixture
def published(tmp_path: Path) -> Path:
    site, content = tmp_path / "site", tmp_path / "content"
    site.mkdir()
    (content / "dist").mkdir(parents=True)
    (content / "VERSION").write_text("v1.2.3\n")
    (content / "dist/teslausb-backend.arm64").write_text("binary")
    with tarfile.open(site / "teslausb-release.tar.gz", "w:gz") as tar:
        tar.add(content, arcname=".")
    digest = hashlib.sha256((site / "teslausb-release.tar.gz").read_bytes()).hexdigest()
    (site / "SHA256SUMS").write_text(f"{digest}  teslausb-release.tar.gz\n")
    return site


def test_installer_uses_a_verified_release(tmp_path: Path, published: Path) -> None:
    result, target = run_use_release(tmp_path, RELEASE_BASE_URL=f"file://{published}")
    assert result.returncode == 0, result.stdout + result.stderr
    assert f"SOURCE_DIR={target}" in result.stdout
    assert (target / "VERSION").read_text().strip() == "v1.2.3"
    assert sorted(path.name for path in target.parent.iterdir()) == ["teslausb-release"]


def test_installer_refuses_a_tampered_release(tmp_path: Path, published: Path) -> None:
    with (published / "teslausb-release.tar.gz").open("ab") as archive:
        archive.write(b"tampered")
    result, target = run_use_release(tmp_path, RELEASE_BASE_URL=f"file://{published}")
    assert result.returncode == 1
    assert "failed its checksum" in result.stdout
    assert not target.exists() and not list(target.parent.iterdir())


def test_installer_stops_when_the_release_is_missing(tmp_path: Path) -> None:
    result, target = run_use_release(tmp_path, RELEASE_BASE_URL=f"file://{tmp_path}/none")
    assert result.returncode == 1
    assert "Could not download" in result.stdout
    assert not target.exists()


@pytest.mark.parametrize("env", [{"SOURCE_DIR": "/srv/checkout"}, {"BRANCH_INSTALL": "true"}])
def test_installer_leaves_development_installs_alone(tmp_path: Path, env: dict[str, str]) -> None:
    result, target = run_use_release(tmp_path, RELEASE_BASE_URL="file:///nonexistent", **env)
    assert result.returncode == 0, result.stdout + result.stderr
    assert not target.exists()


def test_setup_no_longer_fetches_the_upstream_web_app() -> None:
    assert "marcone/teslausb-webui" not in (ROOT / "setup/pi/configure-web.sh").read_text()
