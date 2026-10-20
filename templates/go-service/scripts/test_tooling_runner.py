"""Fast tests for the scaffold tooling suite runner."""

from __future__ import annotations

import os
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path

import pytest

import test_tooling as tooling_suite
import tooling_runner


def assert_process_stopped(pid: int) -> None:
    deadline = time.monotonic() + 3
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.02)
    raise AssertionError(f"process {pid} is still running")


def test_main_rejects_unexpected_arguments() -> None:
    # when / then
    with pytest.raises(SystemExit) as error:
        tooling_runner.main(["unexpected"])
    assert error.value.code == 2


@pytest.mark.parametrize(
    ("received_signal", "expected_status"),
    [(signal.SIGINT, 130), (signal.SIGTERM, 143)],
)
def test_run_suite_maps_interruption_and_restores_handlers(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    received_signal: signal.Signals,
    expected_status: int,
) -> None:
    # given
    previous_interrupt = signal.getsignal(signal.SIGINT)
    previous_terminate = signal.getsignal(signal.SIGTERM)

    def interrupt(_arguments: list[str]) -> int:
        signal.raise_signal(received_signal)
        return 0

    monkeypatch.setattr(tooling_runner.pytest, "main", interrupt)

    # when
    status = tooling_runner.run_suite(tmp_path, tmp_path)

    # then
    assert status == expected_status
    assert signal.getsignal(signal.SIGINT) is previous_interrupt
    assert signal.getsignal(signal.SIGTERM) is previous_terminate


@pytest.mark.parametrize(
    ("outcome", "expected_status"),
    [("pass", 0), ("test-failure", 1), ("setup-failure", 1)],
)
def test_runner_removes_fixture_tree_after_completed_session(
    tmp_path: Path,
    outcome: str,
    expected_status: int,
) -> None:
    # given
    module_root = tmp_path / "module"
    scripts = module_root / "scripts"
    scripts.mkdir(parents=True)
    shutil.copy2(tooling_runner.__file__, scripts / "tooling_runner.py")
    (scripts / "test_tooling.py").write_text(
        "import os\n"
        "import pytest\n\n"
        "@pytest.fixture(scope='module')\n"
        "def owned_tree(tmp_path_factory):\n"
        "    root = tmp_path_factory.mktemp('go-service-tooling')\n"
        "    if os.environ['CONTROLLED_OUTCOME'] == 'setup-failure':\n"
        "        raise RuntimeError('controlled setup failure')\n"
        "    return root\n\n"
        "@pytest.mark.integration\n"
        "def test_controlled_outcome(owned_tree):\n"
        "    assert os.environ['CONTROLLED_OUTCOME'] != 'test-failure'\n",
        encoding="utf-8",
    )
    temporary = tmp_path / "temporary"
    temporary.mkdir()
    source_module_root = Path(tooling_runner.__file__).resolve().parent.parent
    environment = {
        **os.environ,
        "CONTROLLED_OUTCOME": outcome,
        "INSURANCE_HUB_ROOT": str(tooling_runner.repository_root(source_module_root)),
        "PYTHONDONTWRITEBYTECODE": "1",
        "TMPDIR": str(temporary),
    }

    # when
    result = subprocess.run(
        [sys.executable, str(scripts / "tooling_runner.py")],
        cwd=module_root,
        env=environment,
        capture_output=True,
        text=True,
        timeout=20,
        check=False,
    )

    # then
    assert result.returncode == expected_status, result.stdout + result.stderr
    assert list(temporary.rglob("go-service-tooling*")) == []


def test_tooling_target_forwards_selected_tool_overrides(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    fixture = tmp_path / "fixture"
    fixture.mkdir()
    tooling = tooling_suite.Tooling(
        make_arguments=("make",),
        repository_root=tmp_path,
        temporary_root=tmp_path,
        base_fixture=fixture,
        deadline=time.monotonic() + 10,
        go="/controlled/go",
        uv="/controlled/uv",
    )
    captured: dict[str, object] = {}

    def capture(arguments: list[str], **options: object) -> None:
        captured["arguments"] = arguments
        captured["options"] = options

    monkeypatch.setattr(tooling_suite, "run_command", capture)

    # when
    tooling_suite.run_target(tooling, fixture, "controlled", "check")

    # then
    assert "GO=/controlled/go" in captured["arguments"]
    assert "UV=/controlled/uv" in captured["arguments"]


def test_launcher_sigterm_cleans_active_command_and_resistant_descendant(
    tmp_path: Path,
) -> None:
    # given
    module_root = Path(tooling_runner.__file__).resolve().parent.parent
    repository_root = tooling_runner.repository_root(module_root)
    common_tooling = repository_root / "scripts" / "go" / "module"
    fixture_module = tmp_path / "module"
    shutil.copytree(
        module_root,
        fixture_module,
        ignore=lambda _path, names: {
            name
            for name in names
            if name
            in {".common-go-tooling", ".git", ".pytest_cache", ".ruff_cache", ".tools", ".venv"}
        },
    )
    tools_bin = tmp_path / "tools"
    tools_bin.mkdir()
    for tool in ("buf", "protoc-gen-go", "protoc-gen-go-grpc", "golangci-lint", "govulncheck"):
        (tools_bin / tool).write_text("controlled tool\n", encoding="utf-8")

    leader_pid = tmp_path / "leader.pid"
    descendant_pid = tmp_path / "descendant.pid"
    descendant = (
        "import os, pathlib, signal, time; "
        "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        f"pathlib.Path({str(descendant_pid)!r}).write_text(str(os.getpid())); "
        "time.sleep(60)"
    )
    fake_make = tmp_path / "fake_make.py"
    fake_make.write_text(
        "import os, pathlib, subprocess, sys, time\n"
        f"pathlib.Path({str(leader_pid)!r}).write_text(str(os.getpid()))\n"
        f"subprocess.Popen([sys.executable, '-c', {descendant!r}])\n"
        "time.sleep(60)\n",
        encoding="utf-8",
    )
    temporary = tmp_path / "temporary"
    temporary.mkdir()
    environment = {
        **os.environ,
        "COMMON_GO_TOOLING_DIR": str(common_tooling),
        "COMMON_GO_TOOLING_PYTHON": sys.executable,
        "INSURANCE_HUB_ROOT": str(repository_root),
        "MAKE_COMMAND": f"{sys.executable} {fake_make}",
        "MODULE_ROOT": str(fixture_module),
        "PYTHONDONTWRITEBYTECODE": "1",
        "TMPDIR": str(temporary),
        "TOOLS_BIN": str(tools_bin),
    }
    process = subprocess.Popen(
        [str(module_root / "scripts" / "test-tooling.sh")],
        cwd=module_root,
        env=environment,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    deadline = time.monotonic() + 10
    while (
        not leader_pid.is_file() or not descendant_pid.is_file()
    ) and time.monotonic() < deadline:
        time.sleep(0.02)
    if not leader_pid.is_file() or not descendant_pid.is_file():
        stdout, stderr = process.communicate(timeout=5)
        pytest.fail(f"controlled child did not start\nstdout:\n{stdout}\nstderr:\n{stderr}")

    # when
    process.send_signal(signal.SIGTERM)
    _, stderr = process.communicate(timeout=20)

    # then
    assert process.returncode == 143, stderr
    assert "test-tooling: interrupted by SIGTERM" in stderr
    assert "cleanup exceeded" not in stderr
    assert_process_stopped(int(leader_pid.read_text(encoding="utf-8")))
    assert_process_stopped(int(descendant_pid.read_text(encoding="utf-8")))
    assert list(temporary.rglob("go-service-tooling*")) == []
