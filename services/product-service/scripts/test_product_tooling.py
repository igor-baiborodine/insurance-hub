"""Focused smoke tests for Product-specific Python tooling."""

from __future__ import annotations

import json
from pathlib import Path

import image_check
import integration_test
import pytest


@pytest.mark.parametrize("missing", sorted(image_check.PACKAGED_FILES))
def test_image_contents_require_each_runtime_asset(missing: str) -> None:
    # given
    names = {*image_check.PACKAGED_FILES, "etc/ssl/certs/ca-certificates.crt"}
    names.remove(missing)

    # when / then
    with pytest.raises(image_check.ImageCheckError, match="missing packaged files"):
        image_check.validate_image_contents(names)


def test_image_assets_reject_content_drift(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    # given
    source = tmp_path / "migration.sql"
    source.write_bytes(b"reviewed\n")
    monkeypatch.setattr(image_check, "EXPECTED_ASSETS", {"migration.sql": source})

    # when / then
    with pytest.raises(image_check.ImageCheckError, match="differs from source"):
        image_check.validate_image_assets({"migration.sql": b"changed\n"})


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


def access_report(*, complete: bool) -> str:
    names = sorted(integration_test.EXPECTED_ACCESS_CASES)
    if not complete:
        names.pop()
    records = [
        {"Action": "run", "Test": "TestProductAccess"},
        {"Action": "pass", "Test": "TestProductAccess"},
    ]
    for name in names:
        records.extend(({"Action": "run", "Test": name}, {"Action": "pass", "Test": name}))
    return "".join(json.dumps(record) + "\n" for record in records)


def test_access_results_require_the_complete_direct_matrix(tmp_path: Path) -> None:
    # given
    complete = tmp_path / "complete.json"
    incomplete = tmp_path / "incomplete.json"
    complete.write_text(access_report(complete=True), encoding="utf-8")
    incomplete.write_text(access_report(complete=False), encoding="utf-8")

    # when / then
    integration_test.validate_access_results(complete)
    with pytest.raises(integration_test.IntegrationError, match="incomplete or altered"):
        integration_test.validate_access_results(incomplete)


@pytest.mark.integration
def test_integration_runner_uses_captured_go_json(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("GO", "controlled-go")
    monkeypatch.setenv("DOCKER", "controlled-docker")
    monkeypatch.setenv("POSTGRES_IMAGE", "controlled-postgres")
    monkeypatch.setenv("INTEGRATION_SUITE", "reader")
    monkeypatch.setenv("FIXTURE_SET", "qa")
    monkeypatch.delenv("PRODUCT_DATABASE_URL", raising=False)
    monkeypatch.delenv("INTEGRATION_COVERAGE_FILE", raising=False)
    monkeypatch.setattr(integration_test, "MODULE_ROOT", tmp_path)
    passed = "TestPostgresReader/real_reads_emit_bounded_database_and_pool_signals"

    def fake_run(arguments: list[str], **kwargs: object) -> str:
        if arguments[0] == "controlled-go":
            assert kwargs["capture"] is True
            return json.dumps({"Action": "pass", "Test": passed}) + "\n"
        return ""

    monkeypatch.setattr(integration_test, "run", fake_run)

    # when
    status = integration_test.main([])

    # then
    assert status == 0
    assert passed in capsys.readouterr().out


def test_image_gate_requires_each_administration_fault(tmp_path: Path) -> None:
    # given
    required = {
        "TestProductRuntimeImage",
        "TestProductAdministrationImage",
        *image_check.ADMINISTRATION_CASES,
    }
    log = tmp_path / "image.json"
    log.write_text(
        "\n".join(
            json.dumps({"Action": "pass", "Test": name})
            for name in required - {image_check.ADMINISTRATION_CASES[0]}
        ),
        encoding="utf-8",
    )

    # when / then
    with pytest.raises(image_check.ImageCheckError, match="required cases"):
        image_check.validate_image_tests(log)
