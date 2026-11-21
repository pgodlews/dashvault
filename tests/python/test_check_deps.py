"""Tests for run/check-deps.sh and the dependency manifest."""

from __future__ import annotations

import re
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "run/check-deps.sh"
MANIFEST = ROOT / "run/lib/dependencies.txt"
# check-deps.sh targets the Pi's bash 5; macOS /bin/bash is 3.2.
BASH = shutil.which("bash", path="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin") or "bash"


def run(
    tmp_path: Path,
    *args: str,
    manifest: str,
    present: tuple[str, ...] = (),
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir(exist_ok=True)
    for name in present:
        stub = bin_dir / name
        stub.write_text("#!/bin/sh\nexit 0\n")
        stub.chmod(0o755)
    # apt-get stub: "installs" a package by creating the command named in INSTALLS_<pkg>.
    apt = bin_dir / "apt-get"
    apt.write_text(
        '#!/bin/bash\nprintf "%s\\n" "$*" >> "$APT_LOG"\n'
        'for a in "$@"; do v="INSTALLS_${a//-/_}"; [ -n "${!v:-}" ] && '
        'printf "#!/bin/sh\\n" > "$STUB_DIR/${!v}" && chmod +x "$STUB_DIR/${!v}"; done; exit 0\n'
    )
    apt.chmod(0o755)
    manifest_path = tmp_path / "deps.txt"
    manifest_path.write_text(manifest)
    full_env = {
        # Only the stubs plus the basics the script itself uses.
        "PATH": f"{bin_dir}:/usr/bin:/bin",
        "TESLAUSB_DEPS_MANIFEST": str(manifest_path),
        "TESLAUSB_SETUP_CONF": str(tmp_path / "missing.conf"),
        "APT_LOG": str(tmp_path / "apt.log"),
        "STUB_DIR": str(bin_dir),
        **(env or {}),
    }
    return subprocess.run(  # noqa: S603 - repository script with test-created stubs
        [BASH, str(SCRIPT), *args], env=full_env, capture_output=True, text=True
    )


MANIFEST_TEXT = """\
# comment
core   tool-a          pkg-a
core   alt-1|alt-2     pkg-alt
cifs   mount.fake      fake-utils
nfs    nfs-only        nfs-pkg
rclone rclone-x        -           see https://rclone.org/install/
"""


def test_all_present_exits_zero(tmp_path: Path) -> None:
    result = run(
        tmp_path, "core", "cifs", manifest=MANIFEST_TEXT, present=("tool-a", "alt-2", "mount.fake")
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert result.stdout == ""


def test_reports_only_selected_features(tmp_path: Path) -> None:
    result = run(tmp_path, "core", "cifs", manifest=MANIFEST_TEXT, present=("tool-a",))
    assert result.returncode == 1
    assert result.stdout.splitlines() == [
        "core: alt-1|alt-2 (apt: pkg-alt)",
        "cifs: mount.fake (apt: fake-utils)",
    ]
    assert "nfs-only" not in result.stdout


def test_defaults_to_core_and_active_backend(tmp_path: Path) -> None:
    result = run(
        tmp_path, manifest=MANIFEST_TEXT, present=("tool-a", "alt-1"), env={"ARCHIVE_SYSTEM": "nfs"}
    )
    assert result.stdout.splitlines() == ["nfs: nfs-only (apt: nfs-pkg)"]


def test_reads_backend_from_setup_conf(tmp_path: Path) -> None:
    conf = tmp_path / "setup.conf"
    conf.write_text("export ARCHIVE_SYSTEM='cifs'\n")
    result = run(
        tmp_path,
        manifest=MANIFEST_TEXT,
        present=("tool-a", "alt-1"),
        env={"TESLAUSB_SETUP_CONF": str(conf)},
    )
    assert result.stdout.splitlines() == ["cifs: mount.fake (apt: fake-utils)"]


def test_install_fetches_missing_packages_once(tmp_path: Path) -> None:
    result = run(
        tmp_path,
        "--install",
        "core",
        "cifs",
        manifest=MANIFEST_TEXT + "cifs   tool-a          pkg-a\n",
        env={
            "INSTALLS_pkg_a": "tool-a",
            "INSTALLS_pkg_alt": "alt-1",
            "INSTALLS_fake_utils": "mount.fake",
        },
    )
    assert result.returncode == 0, result.stdout + result.stderr
    apt_calls = (tmp_path / "apt.log").read_text().splitlines()
    assert apt_calls == ["install -y --no-install-recommends pkg-a pkg-alt fake-utils"]


def test_non_apt_dependency_stays_missing_with_hint(tmp_path: Path) -> None:
    result = run(tmp_path, "--install", "rclone", manifest=MANIFEST_TEXT)
    assert result.returncode == 1
    assert result.stdout.strip() == (
        "rclone: rclone-x (not available from apt; see https://rclone.org/install/)"
    )
    assert not (tmp_path / "apt.log").exists()


def test_missing_manifest_is_a_usage_error(tmp_path: Path) -> None:
    result = run(tmp_path, manifest="", env={"TESLAUSB_DEPS_MANIFEST": str(tmp_path / "nope.txt")})
    assert result.returncode == 2


# ── manifest must cover what the backends actually call ──────────────────────


def manifest_checks(feature: str) -> set[str]:
    checks: set[str] = set()
    for line in MANIFEST.read_text().splitlines():
        fields = line.split()
        if fields and not fields[0].startswith("#") and fields[0] in (feature, "core"):
            checks.update(fields[1].split("|"))
    return checks


# External (non-Essential) tools each backend's runtime scripts call.
# If a backend starts using a new tool, add it here AND to dependencies.txt.
BACKEND_TOOLS = {
    "cifs": {"rsync", "mount.cifs"},
    "nfs": {"rsync", "mount.nfs", "killall"},
    "rsync": {"rsync", "ssh", "ping"},
    "rclone": {"rclone", "ping"},
}
NON_ESSENTIAL = {
    "rsync",
    "rclone",
    "ssh",
    "ping",
    "killall",
    "nc",
    "hping3",
    "curl",
    "sntp",
    "ntpdig",
}


@pytest.mark.parametrize("backend", sorted(BACKEND_TOOLS))
def test_manifest_lists_every_backend_tool(backend: str) -> None:
    assert BACKEND_TOOLS[backend] <= manifest_checks(backend)


@pytest.mark.parametrize("backend", sorted(BACKEND_TOOLS))
def test_backend_runtime_scripts_use_only_declared_tools(backend: str) -> None:
    # Catches the original bug class: a runtime script quietly starts calling
    # a tool (nc) that only setup's verify step ever installed.
    declared = manifest_checks(backend)
    for script in (ROOT / f"run/{backend}_archive").glob("*.sh"):
        if script.name == "verify-and-configure-archive.sh":
            continue  # setup-only; installs its own tools
        source = re.sub(r"#.*", "", script.read_text())
        used = {
            tool
            for tool in NON_ESSENTIAL
            if re.search(rf"(^|[\s;|&(!]){re.escape(tool)}\s", source, re.M)
        }
        assert used <= declared, f"{script.name} uses {used - declared} not in dependencies.txt"


def test_real_manifest_parses(tmp_path: Path) -> None:
    for line in MANIFEST.read_text().splitlines():
        fields = line.split()
        if not fields or fields[0].startswith("#"):
            continue
        assert len(fields) >= 3, line
        assert fields[0] in {"core", "cifs", "nfs", "rsync", "rclone", "telegram"}, line
