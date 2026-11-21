import subprocess
from pathlib import Path

import pytest

SCRIPT = Path(__file__).parents[2] / "setup/generic/rootfs-size.sh"


@pytest.mark.parametrize(
    ("used_bytes", "expected"),
    [
        (0, "8G"),
        (3 * (1 << 30), "8G"),
        (7_400_000_000, "12G"),
        (8 * (1 << 30), "13G"),
    ],
)
def test_rootfs_size(used_bytes: int, expected: str) -> None:
    result = subprocess.run(  # noqa: S603
        ["/bin/bash", str(SCRIPT), str(used_bytes)],
        check=True,
        capture_output=True,
        text=True,
    )
    assert result.stdout.strip() == expected


@pytest.mark.parametrize("used_bytes", ["", "-1", "1.5", "abc"])
def test_rootfs_size_rejects_invalid_input(used_bytes: str) -> None:
    result = subprocess.run(  # noqa: S603
        ["/bin/bash", str(SCRIPT), used_bytes],
        check=False,
        capture_output=True,
        text=True,
    )
    assert result.returncode != 0
