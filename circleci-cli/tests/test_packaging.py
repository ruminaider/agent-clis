from __future__ import annotations

import subprocess
import sys
import zipfile
from pathlib import Path


def test_wheel_includes_targets_yaml(tmp_path):
    repo_root = Path(__file__).resolve().parents[1]
    wheel_dir = tmp_path / "wheelhouse"
    wheel_dir.mkdir()

    subprocess.run(
        [
            sys.executable,
            "-m",
            "pip",
            "wheel",
            "--no-deps",
            "-w",
            str(wheel_dir),
            str(repo_root),
        ],
        check=True,
        cwd=repo_root,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )

    wheels = sorted(wheel_dir.glob("circleci_cli-*.whl"))
    assert wheels, "expected a built wheel in the temporary wheelhouse"

    with zipfile.ZipFile(wheels[0]) as wheel:
        assert "circleci_cli/targets.yml" in wheel.namelist()
        with wheel.open("circleci_cli/targets.yml") as fh:
            contents = fh.read().decode("utf-8")

    assert "default_project_slug" in contents
