"""The hosted setup page must not be able to send anything over the network."""

from __future__ import annotations

import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def build(tmp_path: Path) -> str:
    script = str(ROOT / "tools/build-pages.sh")
    subprocess.run(["bash", script, str(tmp_path)], check=True)  # noqa: S603, S607
    return (tmp_path / "setup/index.html").read_text()


def test_policy_blocks_all_network_requests(tmp_path: Path) -> None:
    page = build(tmp_path)
    policy = re.search(r'http-equiv="Content-Security-Policy" content="([^"]+)"', page)
    assert policy is not None
    directives = policy.group(1)
    assert "default-src 'none'" in directives
    assert "connect-src" not in directives
    assert "form-action 'none'" in directives
    assert "base-uri 'none'" in directives


def test_policy_applies_before_any_script(tmp_path: Path) -> None:
    page = build(tmp_path)
    assert page.index("Content-Security-Policy") < page.index("<script")


def test_page_loads_nothing_from_elsewhere(tmp_path: Path) -> None:
    page = build(tmp_path)
    assert not re.search(r"<(script|link|img|iframe)\b[^>]*\b(src|href)=", page)


def test_site_root_opens_the_setup_page(tmp_path: Path) -> None:
    build(tmp_path)
    assert 'url=setup/"' in (tmp_path / "index.html").read_text()
