"""Fast controlled-failure tests for Product-specific Python tooling."""

from __future__ import annotations

import json
import os
import sys
import time
from pathlib import Path

import bootstrap_docs
import build_image
import integration_test
import pytest
from product_process import ProductCommandError, run

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


def test_integration_reports_zero_selected_tests(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    integration_environment(monkeypatch, "all")
    monkeypatch.setenv("PRODUCT_TEST_FORCE_ZERO_SELECTION", "1")
    monkeypatch.setattr(integration_test, "MODULE_ROOT", tmp_path)

    def fake_run(_arguments: list[str], **kwargs: object) -> str:
        log = kwargs.get("log_path")
        if isinstance(log, Path):
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

    def fake_run(_arguments: list[str], **kwargs: object) -> str:
        log = kwargs.get("log_path")
        if isinstance(log, Path):
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
        log = kwargs.get("log_path")
        if isinstance(log, Path):
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
