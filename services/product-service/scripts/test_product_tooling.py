"""Fast controlled-failure tests for Product-specific Python tooling."""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path

import bootstrap_docs
import build_image
import integration_test
import pytest
from product_process import CAPTURE_LIMIT, ProductCommandError, run

import check_docs


def configure_doc_paths(monkeypatch: pytest.MonkeyPatch, root: Path) -> Path:
    binary = root / "bin/mmdc"
    monkeypatch.setattr(bootstrap_docs, "TOOLS_ROOT", root)
    monkeypatch.setattr(bootstrap_docs, "TOOLS_BIN", root / "bin")
    monkeypatch.setattr(bootstrap_docs, "MMDC", binary)
    return binary


def executable(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("#!/bin/sh\n", encoding="utf-8")
    path.chmod(0o755)


def test_bootstrap_docs_accepts_complete_pinned_install(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    mmdc = configure_doc_paths(monkeypatch, tmp_path)
    executable(mmdc)
    executable(tmp_path / "puppeteer/chrome-headless-shell")

    def fake_captured(arguments: list[str], _activity: str) -> str:
        if any("process.versions.node" in argument for argument in arguments):
            return "20"
        if arguments[0] == str(mmdc):
            return bootstrap_docs.MERMAID_VERSION
        return bootstrap_docs.PUPPETEER_VERSION

    monkeypatch.setattr(bootstrap_docs, "captured", fake_captured)
    monkeypatch.setattr(
        bootstrap_docs,
        "run",
        lambda *_args, **_kwargs: pytest.fail("complete install attempted provisioning"),
    )

    # when / then
    assert bootstrap_docs.main([]) == 0


def test_bootstrap_docs_provisions_missing_install(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    mmdc = configure_doc_paths(monkeypatch, tmp_path)
    inspections = 0
    commands: list[list[str]] = []

    def fake_captured(arguments: list[str], _activity: str) -> str:
        nonlocal inspections
        if any("process.versions.node" in argument for argument in arguments):
            return "20"
        inspections += 1
        if inspections <= 1:
            raise ProductCommandError("missing tooling")
        if arguments[0] == str(mmdc):
            return bootstrap_docs.MERMAID_VERSION
        return bootstrap_docs.PUPPETEER_VERSION

    def fake_run(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if "install" in arguments:
            executable(tmp_path / "node_modules/.bin/mmdc")
            executable(tmp_path / "puppeteer/chrome-headless-shell")
        return ""

    monkeypatch.setattr(bootstrap_docs, "captured", fake_captured)
    monkeypatch.setattr(bootstrap_docs, "run", fake_run)

    # when
    status = bootstrap_docs.main([])

    # then
    assert status == 0
    assert any("install" in command for command in commands)


def test_bootstrap_docs_provisions_when_renderer_executable_is_absent(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    mmdc = configure_doc_paths(monkeypatch, tmp_path / "tools")
    fake_bin = tmp_path / "fake-bin"
    fake_bin.mkdir()
    node = fake_bin / "node"
    npm = fake_bin / "npm"
    renderer_program = f"#!{sys.executable}\nprint({bootstrap_docs.MERMAID_VERSION!r})\n"
    node.write_text(
        f"#!{sys.executable}\n"
        "import sys\n"
        f"print('20' if 'process.versions.node' in ' '.join(sys.argv) else {bootstrap_docs.PUPPETEER_VERSION!r})\n",
        encoding="utf-8",
    )
    npm.write_text(
        f"#!{sys.executable}\n"
        "import os, pathlib, sys\n"
        "root = pathlib.Path(os.environ['DOC_FAKE_ROOT'])\n"
        "if 'install' in sys.argv:\n"
        "    renderer = root / 'node_modules/.bin/mmdc'\n"
        "    renderer.parent.mkdir(parents=True, exist_ok=True)\n"
        f"    renderer.write_text({renderer_program!r})\n"
        "    renderer.chmod(0o755)\n"
        "    browser = root / 'puppeteer/chrome-headless-shell'\n"
        "    browser.parent.mkdir(parents=True, exist_ok=True)\n"
        "    browser.write_text('controlled browser')\n"
        "    browser.chmod(0o755)\n",
        encoding="utf-8",
    )
    node.chmod(0o755)
    npm.chmod(0o755)
    monkeypatch.setenv("NODE", str(node))
    monkeypatch.setenv("NPM", str(npm))
    monkeypatch.setenv("DOC_FAKE_ROOT", str(tmp_path / "tools"))
    assert not mmdc.exists()

    # when
    status = bootstrap_docs.main([])

    # then
    assert status == 0
    assert mmdc.is_symlink()


def test_bootstrap_docs_rejects_wrong_node_before_provisioning(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    configure_doc_paths(monkeypatch, tmp_path)
    monkeypatch.setattr(bootstrap_docs, "captured", lambda *_args: "19")
    monkeypatch.setattr(
        bootstrap_docs,
        "run",
        lambda *_args, **_kwargs: pytest.fail("invalid runtime attempted provisioning"),
    )

    # when
    status = bootstrap_docs.main([])

    # then
    assert status == 1
    assert "Node.js >= 20 is required" in capsys.readouterr().err


def test_bootstrap_docs_reports_partial_install_failure(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    configure_doc_paths(monkeypatch, tmp_path)
    calls = 0

    def fake_captured(*_args: object) -> str:
        nonlocal calls
        calls += 1
        if calls == 1:
            return "20"
        raise ProductCommandError("missing tooling")

    monkeypatch.setattr(bootstrap_docs, "captured", fake_captured)
    monkeypatch.setattr(
        bootstrap_docs,
        "run",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            ProductCommandError("controlled install failure")
        ),
    )

    # when
    status = bootstrap_docs.main([])

    # then
    assert status == 1
    assert "controlled install failure" in capsys.readouterr().err


def test_image_build_rejects_missing_identity_before_command(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    for name in build_image.REQUIRED:
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setattr(
        build_image,
        "command",
        lambda *_args, **_kwargs: pytest.fail("invalid image identity reached a command"),
    )

    # when
    status = build_image.main([])

    # then
    assert status == 1
    assert "IMAGE_NAME is required" in capsys.readouterr().err


@pytest.mark.parametrize(
    ("suite", "fixture_set", "message"),
    [
        ("", "qa", "INTEGRATION_SUITE must not be empty"),
        ("unknown", "qa", "unknown INTEGRATION_SUITE"),
        ("reader", "", "FIXTURE_SET must not be empty"),
        ("reader", "local-dev", "unknown FIXTURE_SET"),
    ],
)
def test_integration_selection_fails_before_docker(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    suite: str,
    fixture_set: str,
    message: str,
) -> None:
    # given
    monkeypatch.setenv("GO", "go")
    monkeypatch.setenv("DOCKER", "docker")
    monkeypatch.setenv("POSTGRES_IMAGE", "controlled")
    monkeypatch.setenv("INTEGRATION_SUITE", suite)
    monkeypatch.setenv("FIXTURE_SET", fixture_set)
    monkeypatch.setattr(
        integration_test,
        "run",
        lambda *_args, **_kwargs: pytest.fail("invalid selection reached Docker"),
    )

    # when
    status = integration_test.main([])

    # then
    assert status == 1
    assert message in capsys.readouterr().err


def test_integration_rejects_external_database_before_docker(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("GO", "go")
    monkeypatch.setenv("DOCKER", "docker")
    monkeypatch.setenv("POSTGRES_IMAGE", "controlled")
    monkeypatch.setenv("INTEGRATION_SUITE", "reader")
    monkeypatch.setenv("FIXTURE_SET", "qa")
    monkeypatch.setenv("PRODUCT_DATABASE_URL", "synthetic-secret-sentinel")
    monkeypatch.setattr(
        integration_test,
        "run",
        lambda *_args, **_kwargs: pytest.fail("external database input reached Docker"),
    )

    # when
    status = integration_test.main([])

    # then
    assert status == 1
    diagnostic = capsys.readouterr().err
    assert "PRODUCT_DATABASE_URL must be unset" in diagnostic
    assert "synthetic-secret-sentinel" not in diagnostic


def integration_environment(monkeypatch: pytest.MonkeyPatch, suite: str) -> None:
    monkeypatch.setenv("GO", "controlled-go")
    monkeypatch.setenv("DOCKER", "controlled-docker")
    monkeypatch.setenv("POSTGRES_IMAGE", "controlled-postgres")
    monkeypatch.setenv("INTEGRATION_SUITE", suite)
    monkeypatch.setenv("FIXTURE_SET", "qa")
    monkeypatch.delenv("PRODUCT_DATABASE_URL", raising=False)
    monkeypatch.delenv("PRODUCT_TEST_FORCE_ZERO_SELECTION", raising=False)
    monkeypatch.delenv("INTEGRATION_COVERAGE_FILE", raising=False)


@pytest.mark.parametrize("exit_status", [0, 7])
def test_stream_test_log_preserves_output_and_status(tmp_path: Path, exit_status: int) -> None:
    # given
    log = tmp_path / "go-test.json"
    script = Path(integration_test.__file__).resolve().parent / "stream-test-log.sh"
    event = {"Action": "pass", "Test": "TestProductAccess"}
    diagnostic = "go: downloading github.com/pressly/goose/v3 v3.27.0"

    # when
    result = subprocess.run(
        [
            "bash",
            str(script),
            str(log),
            sys.executable,
            "-c",
            "import json, sys; "
            f"print({diagnostic!r}, file=sys.stderr); "
            f"print(json.dumps({event!r})); raise SystemExit({exit_status})",
        ],
        check=False,
        capture_output=True,
        text=True,
        timeout=10,
    )

    # then
    assert result.returncode == exit_status
    assert result.stdout == ""
    assert result.stderr == diagnostic + "\n"
    assert [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines()] == [event]


def integration_log_path(arguments: list[str], kwargs: dict[str, object]) -> Path | None:
    if (
        len(arguments) >= 3
        and arguments[0] == "bash"
        and arguments[1].endswith("/stream-test-log.sh")
    ):
        return Path(arguments[2])
    log = kwargs.get("log_path")
    return log if isinstance(log, Path) else None


def test_integration_reports_zero_selected_tests(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    integration_environment(monkeypatch, "all")
    monkeypatch.setenv("PRODUCT_TEST_FORCE_ZERO_SELECTION", "1")
    monkeypatch.setattr(integration_test, "MODULE_ROOT", tmp_path)

    def fake_run(arguments: list[str], **kwargs: object) -> str:
        log = integration_log_path(arguments, kwargs)
        if log is not None:
            log.write_text("", encoding="utf-8")
        return ""

    monkeypatch.setattr(integration_test, "run", fake_run)

    # when
    status = integration_test.main([])

    # then
    assert status == 1
    assert "suite all selected zero tests" in capsys.readouterr().err


@pytest.mark.parametrize(
    ("suite", "records", "message"),
    [
        (
            "schema-adoption",
            ["TestProductSchemaAdoption"],
            "did not pass schema adoption case fresh_and_sqlc_schema_parity",
        ),
        (
            "migration",
            ["TestProductMigration"],
            "did not pass migration case fresh_schema_and_read-only_status",
        ),
        (
            "seed",
            ["TestProductSeed"],
            "did not pass seed case empty_catalog_matches_accepted_storage_and_transports",
        ),
        (
            "all",
            ["TestProductHTTP"],
            "did not pass schema adoption case fresh_and_sqlc_schema_parity",
        ),
        (
            "telemetry",
            [
                "TestProductTelemetry",
                *[f"TestProductTelemetry/{name}" for name in integration_test.TELEMETRY_CASES[:-1]],
            ],
            "did not pass telemetry case SIGTERM_bounds_an_in-flight_export",
        ),
        (
            "reader",
            ["TestPostgresReader"],
            "did not pass database telemetry case",
        ),
    ],
)
def test_integration_reports_missing_required_case(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    suite: str,
    records: list[str],
    message: str,
) -> None:
    # given
    integration_environment(monkeypatch, suite)
    monkeypatch.setattr(integration_test, "MODULE_ROOT", tmp_path)

    def fake_run(arguments: list[str], **kwargs: object) -> str:
        log = integration_log_path(arguments, kwargs)
        if log is not None:
            log.write_text(
                "".join(json.dumps({"Action": "pass", "Test": name}) + "\n" for name in records),
                encoding="utf-8",
            )
        return ""

    monkeypatch.setattr(integration_test, "run", fake_run)

    # when
    status = integration_test.main([])

    # then
    assert status == 1
    assert message in capsys.readouterr().err


@pytest.mark.parametrize("create_profile", [True, False])
def test_integration_coverage_profile_contract(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    create_profile: bool,
) -> None:
    # given
    integration_environment(monkeypatch, "reader")
    monkeypatch.setenv("INTEGRATION_COVERAGE_FILE", "controlled-coverage.out")
    monkeypatch.setattr(integration_test, "MODULE_ROOT", tmp_path)

    def fake_run(arguments: list[str], **kwargs: object) -> str:
        log = integration_log_path(arguments, kwargs)
        if log is not None:
            log.write_text(
                json.dumps(
                    {
                        "Action": "pass",
                        "Test": "TestPostgresReader/real_reads_emit_bounded_database_and_pool_signals",
                    }
                )
                + "\n",
                encoding="utf-8",
            )
            if create_profile:
                (tmp_path / "controlled-coverage.out").write_text(
                    "mode: atomic\n", encoding="utf-8"
                )
            assert "-covermode=atomic" in arguments
        return ""

    monkeypatch.setattr(integration_test, "run", fake_run)

    # when
    status = integration_test.main([])

    # then
    assert status == (0 if create_profile else 1)
    if not create_profile:
        assert "coverage profile was not created" in capsys.readouterr().err


def documentation_inputs(tmp_path: Path, markdown: str) -> tuple[Path, Path, Path]:
    renderer = tmp_path / "mmdc"
    executable(renderer)
    config = tmp_path / "puppeteer.json"
    config.write_text(
        json.dumps({"args": ["--no-sandbox", "--disable-setuid-sandbox"]}),
        encoding="utf-8",
    )
    document = tmp_path / "document.md"
    document.write_text(markdown, encoding="utf-8")
    return renderer, config, document


def test_check_docs_rejects_broken_local_link(tmp_path: Path) -> None:
    # given
    renderer, config, document = documentation_inputs(
        tmp_path, "# Document\n\n[Missing](missing.md)\n"
    )

    # when / then
    with pytest.raises(SystemExit, match="missing local link target"):
        check_docs.main(
            ["--renderer", str(renderer), "--puppeteer-config", str(config), str(document)]
        )


def test_check_docs_rejects_unsafe_browser_configuration(tmp_path: Path) -> None:
    # given
    renderer, config, document = documentation_inputs(
        tmp_path, "# Document\n\n```mermaid\ngraph TD; A-->B\n```\n"
    )
    config.write_text(json.dumps({"args": ["--disable-setuid-sandbox"]}), encoding="utf-8")

    # when / then
    with pytest.raises(SystemExit, match="missing required Chromium launch argument --no-sandbox"):
        check_docs.main(
            ["--renderer", str(renderer), "--puppeteer-config", str(config), str(document)]
        )


def test_check_docs_bounds_invalid_mermaid_renderer(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    renderer, config, document = documentation_inputs(
        tmp_path, "# Document\n\n```mermaid\ncontrolled invalid diagram\n```\n"
    )
    monkeypatch.setattr(
        check_docs,
        "run",
        lambda *_args, **_kwargs: (_ for _ in ()).throw(
            ProductCommandError("controlled renderer failure")
        ),
    )

    # when / then
    with pytest.raises(SystemExit, match="Mermaid render failed.*controlled renderer failure"):
        check_docs.main(
            ["--renderer", str(renderer), "--puppeteer-config", str(config), str(document)]
        )


def test_product_process_reports_nonzero_without_argument_values(tmp_path: Path) -> None:
    # when / then
    with pytest.raises(ProductCommandError, match="controlled command failed with status 7"):
        run(
            [sys.executable, "-c", "raise SystemExit(7)", "synthetic-secret-sentinel"],
            cwd=tmp_path,
            env=os.environ,
            timeout_seconds=5,
            activity="controlled command",
            capture=False,
        )


def test_product_process_captures_output_larger_than_pipe_capacity(tmp_path: Path) -> None:
    # given
    payload = "x" * (1024 * 1024)

    # when
    output = run(
        [
            sys.executable,
            "-c",
            "import sys; value = sys.stdin.read(); sys.stdout.write('x' * (1024 * 1024) + value)",
        ],
        cwd=tmp_path,
        env=os.environ,
        timeout_seconds=5,
        activity="controlled large capture",
        input_text="complete",
        capture=True,
    )

    # then
    assert output == payload + "complete"


def test_product_process_rejects_output_beyond_capture_limit(tmp_path: Path) -> None:
    # when / then
    with pytest.raises(ProductCommandError, match="stdout exceeded.*capture limit"):
        run(
            [sys.executable, "-c", f"print('x' * {CAPTURE_LIMIT})"],
            cwd=tmp_path,
            env=os.environ,
            timeout_seconds=5,
            activity="controlled capture limit",
            capture=True,
        )


def test_product_process_does_not_wait_for_descendant_capture_eof(tmp_path: Path) -> None:
    # given
    descendant_pid = tmp_path / "descendant.pid"
    command = (
        "import pathlib, subprocess, sys, time\n"
        f"pid_file = pathlib.Path({str(descendant_pid)!r})\n"
        "subprocess.Popen([sys.executable, '-c', "
        "'import os, pathlib, time; pathlib.Path(os.environ[\"PID_FILE\"]).write_text(str(os.getpid())); time.sleep(60)'"
        "])\n"
        "deadline = time.monotonic() + 2\n"
        "while not pid_file.exists() and time.monotonic() < deadline:\n"
        "    time.sleep(0.01)\n"
        "print('leader complete')\n"
    )
    environment = {**os.environ, "PID_FILE": str(descendant_pid)}

    # when
    started = time.monotonic()
    output = run(
        [sys.executable, "-c", command],
        cwd=tmp_path,
        env=environment,
        timeout_seconds=5,
        teardown_seconds=2,
        activity="controlled inherited capture",
        capture=True,
    )

    # then
    assert output == "leader complete\n"
    assert time.monotonic() - started < 3
    wait_for_process_exit(int(descendant_pid.read_text(encoding="utf-8")))


def test_product_process_bounds_timeout_and_descendant_cleanup(tmp_path: Path) -> None:
    # given
    child = tmp_path / "child.py"
    child.write_text(
        "import subprocess, sys, time\n"
        "subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])\n"
        "time.sleep(60)\n",
        encoding="utf-8",
    )

    # when
    started = time.monotonic()
    with pytest.raises(ProductCommandError, match="deadline"):
        run(
            [sys.executable, str(child)],
            cwd=tmp_path,
            env=os.environ,
            timeout_seconds=0.2,
            teardown_seconds=2,
            activity="controlled timeout",
        )

    # then
    assert time.monotonic() - started < 3


def process_is_running(pid: int) -> bool:
    try:
        state = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8").split()[2]
    except FileNotFoundError, ProcessLookupError:
        return False
    return state != "Z"


def wait_for_process_exit(pid: int, timeout: float = 3) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline and process_is_running(pid):
        time.sleep(0.02)
    assert not process_is_running(pid), f"process {pid} remained alive"


@pytest.mark.parametrize(
    ("requested_signal", "expected_status"),
    [(signal.SIGINT, 130), (signal.SIGTERM, 143)],
)
def test_integration_runner_signal_reaps_active_process_group(
    tmp_path: Path, requested_signal: signal.Signals, expected_status: int
) -> None:
    # given
    fake_docker = tmp_path / "docker"
    ready = tmp_path / "ready"
    pids = tmp_path / "pids.json"
    fake_docker.write_text(
        f"#!{sys.executable}\n"
        "import json, os, pathlib, subprocess, sys, time\n"
        "child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])\n"
        "pathlib.Path(os.environ['PRODUCT_SIGNAL_PIDS']).write_text(json.dumps([os.getpid(), child.pid]))\n"
        "pathlib.Path(os.environ['PRODUCT_SIGNAL_READY']).write_text('ready')\n"
        "time.sleep(60)\n",
        encoding="utf-8",
    )
    fake_docker.chmod(0o755)
    environment = dict(os.environ)
    environment.pop("PRODUCT_DATABASE_URL", None)
    environment.update(
        GO=sys.executable,
        DOCKER=str(fake_docker),
        POSTGRES_IMAGE="controlled-postgres",
        INTEGRATION_SUITE="reader",
        FIXTURE_SET="qa",
        PRODUCT_SIGNAL_PIDS=str(pids),
        PRODUCT_SIGNAL_READY=str(ready),
    )
    process = subprocess.Popen(
        [sys.executable, str(Path(integration_test.__file__).resolve())],
        cwd=integration_test.MODULE_ROOT,
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and not ready.is_file():
            assert process.poll() is None
            time.sleep(0.02)
        assert ready.is_file()

        # when
        os.kill(process.pid, requested_signal)
        _stdout, stderr = process.communicate(timeout=15)

        # then
        assert process.returncode == expected_status
        assert requested_signal.name in stderr
        for pid in json.loads(pids.read_text(encoding="utf-8")):
            wait_for_process_exit(pid)
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=3)


@pytest.mark.parametrize("failure", [False, True])
@pytest.mark.parametrize(
    ("suite", "test_name", "cases"),
    [
        ("schema-adoption", "TestProductSchemaAdoption", integration_test.SCHEMA_ADOPTION_CASES),
        ("migration", "TestProductMigration", integration_test.MIGRATION_CASES),
        ("seed", "TestProductSeed", integration_test.SEED_CASES),
    ],
)
def test_administration_runner_preserves_results_and_selects_owned_package(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    failure: bool,
    suite: str,
    test_name: str,
    cases: tuple[str, ...],
) -> None:
    # given
    integration_environment(monkeypatch, suite)
    monkeypatch.setattr(integration_test, "MODULE_ROOT", tmp_path)
    result_paths: list[Path] = []

    def fake_run(arguments: list[str], **kwargs: object) -> str:
        log = integration_log_path(arguments, kwargs)
        if log is not None:
            assert "-race" in arguments
            assert "-tags=integration" in arguments
            assert f"^{test_name}$" in arguments
            assert "./internal/testing" in arguments
            result_paths.append(log)
            records = [test_name] + [f"{test_name}/{case}" for case in cases]
            log.write_text(
                "".join(json.dumps({"Action": "pass", "Test": name}) + "\n" for name in records),
                encoding="utf-8",
            )
            if failure:
                raise ProductCommandError("controlled test failure")
        return ""

    monkeypatch.setattr(integration_test, "run", fake_run)

    # when
    status = integration_test.main([])

    # then
    assert status == int(failure)
    output = capsys.readouterr()
    assert test_name in output.out
    assert bool("controlled test failure" in output.err) == failure
    assert result_paths and all(not path.exists() for path in result_paths)
