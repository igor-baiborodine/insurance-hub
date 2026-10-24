"""Tests for direct-launcher CPython runtime enforcement."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
LAUNCHERS = {
    REPOSITORY_ROOT / "scripts" / "markdown-toc.sh": (
        "selected Python is missing or not executable: {python}",
        "selected Python must be CPython 3.14.8",
        "python-toolchain-setup",
    ),
    REPOSITORY_ROOT / "ai" / "framework" / "scripts" / "check_upstream_sync.sh": (
        "Selected Python unavailable; run explicit setup. No installation attempted.",
        "CPython 3.14.8 required; no fallback",
        "",
    ),
}


def run_launcher(launcher: Path, python: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(launcher), "--help"],
        cwd=REPOSITORY_ROOT,
        env={**os.environ, "PYTHON": str(python)},
        check=False,
        text=True,
        capture_output=True,
        timeout=10,
    )


def system_python_with_wrong_version() -> Path:
    candidate = Path("/usr/bin/python3")
    if not candidate.is_file():
        pytest.skip("supported Ubuntu system Python is unavailable")
    identity = subprocess.run(
        [
            str(candidate),
            "-I",
            "-c",
            "import platform; print(platform.python_implementation(), platform.python_version())",
        ],
        check=True,
        text=True,
        capture_output=True,
        timeout=10,
    ).stdout.strip()
    if identity == "CPython 3.14.8":
        pytest.skip("system Python matches the managed runtime")
    return candidate


@pytest.mark.parametrize("launcher", LAUNCHERS, ids=lambda path: path.name)
def test_direct_launcher_accepts_managed_runtime(launcher: Path) -> None:
    # when
    result = run_launcher(launcher, Path(sys.executable))

    # then
    assert result.returncode == 0, result.stderr


@pytest.mark.parametrize("launcher", LAUNCHERS, ids=lambda path: path.name)
def test_direct_launcher_rejects_missing_runtime_before_cli(launcher: Path, tmp_path: Path) -> None:
    # given
    missing = tmp_path / "missing-python"

    # when
    result = run_launcher(launcher, missing)

    # then
    assert result.returncode == 1
    assert LAUNCHERS[launcher][0].format(python=missing) in result.stderr
    if LAUNCHERS[launcher][2]:
        assert LAUNCHERS[launcher][2] in result.stderr


@pytest.mark.parametrize("launcher", LAUNCHERS, ids=lambda path: path.name)
def test_direct_launcher_rejects_wrong_runtime_before_cli(launcher: Path, tmp_path: Path) -> None:
    # given
    wrong_python = system_python_with_wrong_version()
    cli_started = tmp_path / "cli-started"
    wrapper = tmp_path / "wrong-python"
    wrapper.write_text(
        "#!/bin/sh\n"
        'if [ "$1" = -I ]; then exec "' + str(wrong_python) + '" "$@"; fi\n'
        f': > "{cli_started}"\n'
        f'exec "{sys.executable}" "$@"\n',
        encoding="utf-8",
    )
    wrapper.chmod(0o755)

    # when
    result = run_launcher(launcher, wrapper)

    # then
    assert result.returncode == 1
    assert LAUNCHERS[launcher][1] in result.stderr
    if LAUNCHERS[launcher][2]:
        assert LAUNCHERS[launcher][2] in result.stderr
    assert not cli_started.exists()
