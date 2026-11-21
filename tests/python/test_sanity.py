"""Sanity tests — ensure the test harness itself is wired up correctly."""

from __future__ import annotations

import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
RUN_DIR = REPO_ROOT / "run"

if str(RUN_DIR) not in sys.path:
    sys.path.insert(0, str(RUN_DIR))


def test_repo_root_resolves() -> None:
    assert REPO_ROOT.is_dir()
    assert (REPO_ROOT / "README.md").is_file()


def test_run_dir_has_python_helpers() -> None:
    assert RUN_DIR.is_dir()
    expected = {"tesla_api.py", "teslausb_telegram_bot.py"}
    found = {p.name for p in RUN_DIR.glob("*.py")}
    assert expected.issubset(found), f"missing: {expected - found}"


def test_python_helpers_compile() -> None:
    import py_compile

    for helper in RUN_DIR.glob("*.py"):
        py_compile.compile(str(helper), doraise=True)


def test_shell_entry_points_exist() -> None:
    for name in ("archiveloop", "make_snapshot.sh", "send-push-message"):
        p: Path = RUN_DIR / name
        assert p.is_file(), f"missing run/{name}"
