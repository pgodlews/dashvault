"""Setup installs from the release carried in the SD image, when it should.

The same shell block decides this in three scripts (the first-boot script, the
setup script's bootstrap and the shared environment). It is run here with its
two fixed paths pointed at a temporary directory.
"""

from __future__ import annotations

import re
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = [
    "pi-gen-sources/00-teslausb-tweaks/files/rc.local",
    "setup/pi/setup-teslausb",
    "setup/pi/envsetup.sh",
]
BLOCK = re.compile(r"^  # The SD image carries the release.*?^  fi\n", re.DOTALL | re.MULTILINE)
RELEASE_DIR = "/usr/local/share/teslausb-release"


def block(script: str) -> str:
    match = BLOCK.search((ROOT / script).read_text())
    assert match, f"{script} has no bundled-release block"
    return match.group(0)


def test_all_three_scripts_decide_the_same_way() -> None:
    blocks = {block(script) for script in SCRIPTS}
    assert len(blocks) == 1


def source_dir(tmp_path: Path, env: dict[str, str]) -> str:
    snippet = (
        block("setup/pi/envsetup.sh")
        .replace(RELEASE_DIR, str(tmp_path / "release"))
        .replace("/root/teslausb_version", str(tmp_path / "installed_version"))
    )
    result = subprocess.run(  # noqa: S603
        ["/bin/bash", "-euc", snippet + '\necho "SOURCE_DIR=${SOURCE_DIR:-}"'],
        env={"PATH": "/usr/bin:/bin", **env},
        capture_output=True,
        text=True,
        check=True,
    )
    return result.stdout.strip().removeprefix("SOURCE_DIR=")


@pytest.fixture
def image(tmp_path: Path) -> Path:
    (tmp_path / "release").mkdir()
    (tmp_path / "release/VERSION").write_text("v1.2.0\n")
    return tmp_path


def test_first_boot_installs_from_the_image(image: Path) -> None:
    assert source_dir(image, {}) == str(image / "release")


def test_setup_rerun_on_the_same_release_uses_the_image(image: Path) -> None:
    (image / "installed_version").write_text("v1.2.0\n")
    assert source_dir(image, {}) == str(image / "release")


@pytest.mark.parametrize("installed", ["v1.3.0\n", "source-20261125\n"])
def test_image_copy_is_not_used_once_something_newer_is_installed(
    image: Path, installed: str
) -> None:
    # An app update, or "setup-teslausb upgrade", replaced what the image installed.
    (image / "installed_version").write_text(installed)
    assert source_dir(image, {}) == ""


@pytest.mark.parametrize(
    "env",
    [
        {"REPO": "someone"},
        {"BRANCH": "feature"},
        {"TESLAUSB_IGNORE_BUNDLED_RELEASE": "1"},
    ],
)
def test_explicit_choices_win_over_the_image(image: Path, env: dict[str, str]) -> None:
    assert source_dir(image, env) == ""


def test_a_given_source_directory_is_kept(image: Path) -> None:
    assert source_dir(image, {"SOURCE_DIR": "/srv/checkout"}) == "/srv/checkout"


def test_thin_image_without_a_release_downloads_as_before(tmp_path: Path) -> None:
    assert source_dir(tmp_path, {}) == ""


def test_upgrade_command_bypasses_the_image_copy() -> None:
    setup = (ROOT / "setup/pi/setup-teslausb").read_text()
    upgrade = setup[
        setup.index("function cmd_upgrade {") : setup.index("function cmd_upgrade_prepare")
    ]
    assert "export TESLAUSB_IGNORE_BUNDLED_RELEASE=1" in upgrade
    assert upgrade.index("TESLAUSB_IGNORE_BUNDLED_RELEASE") < upgrade.index("copy_script")


def test_first_boot_script_copies_setup_from_the_image() -> None:
    rc_local = (ROOT / "pi-gen-sources/00-teslausb-tweaks/files/rc.local").read_text()
    get_script = rc_local[
        rc_local.index("function get_script ()") : rc_local.index("function nm_connect_wifi")
    ]
    assert 'cp "$SOURCE_DIR/$remote_path/$name" "$local_path/$name"' in get_script
    assert get_script.index("SOURCE_DIR") < get_script.index("curl ")
    # The decision is made before the script is fetched.
    assert rc_local.index("The SD image carries the release") < rc_local.index(
        "get_script /root/bin setup-teslausb"
    )


def test_setup_records_what_it_installed() -> None:
    configure_web = (ROOT / "setup/pi/configure-web.sh").read_text()
    assert 'cp "$SOURCE_DIR/VERSION" /root/teslausb_version' in configure_web
    assert "teslausb-backend.$backend_arch" in configure_web
