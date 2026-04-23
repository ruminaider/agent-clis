"""Small adapter for the official circleci binary."""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path


def is_installed() -> bool:
    return shutil.which("circleci") is not None


def validate_config(path: str | Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["circleci", "config", "validate", str(path)],
        capture_output=True,
        text=True,
        check=False,
    )
