"""Focused checks for the disposable PDF text extractor."""

from __future__ import annotations

import subprocess

import pytest
import test_administration_integration as integration


def test_extract_pdf_text_uses_bounded_pinned_container(tmp_path, monkeypatch) -> None:
    # given
    pdf = tmp_path / "synthetic.pdf"
    pdf.write_bytes(b"%PDF-1.7\n")
    calls = []
    cleanups = []

    def controlled_run(arguments, **kwargs):
        calls.append((arguments, kwargs))
        return "Synthetic policy text"

    def controlled_cleanup(arguments, **kwargs):
        cleanups.append((arguments, kwargs))
        return subprocess.CompletedProcess(arguments, 0, "", "")

    monkeypatch.setattr(integration, "run", controlled_run)
    monkeypatch.setattr(integration.subprocess, "run", controlled_cleanup)

    # when
    extracted = integration.extract_pdf_text(pdf)

    # then
    assert extracted == "Synthetic policy text"
    arguments, options = calls.pop()
    assert options == {"timeout": 60}
    assert arguments[:3] == [integration.DOCKER, "run", "--pull=missing"]
    assert "--network" in arguments and arguments[arguments.index("--network") + 1] == "none"
    assert "--read-only" in arguments
    assert "--cap-drop" in arguments and arguments[arguments.index("--cap-drop") + 1] == "ALL"
    assert integration.PDF_TEXT_IMAGE in arguments
    assert integration.PDF_TEXT_IMAGE.endswith(
        "@sha256:e1c8434466222f6ace15f91b892bc41a0d4ac102e9147eb38e44ff76f1a6ea5a"
    )
    assert arguments[-4:] == ["-enc", "UTF-8", "input.pdf", "-"]
    cleanup_arguments, cleanup_options = cleanups.pop()
    assert cleanup_arguments[:3] == [integration.DOCKER, "rm", "-f"]
    assert cleanup_options == {"capture_output": True, "text": True, "timeout": 10}


def test_extract_pdf_text_preserves_failure_and_cleans_container(tmp_path, monkeypatch) -> None:
    # given
    pdf = tmp_path / "synthetic.pdf"
    pdf.write_bytes(b"%PDF-1.7\n")
    cleanups = []

    def controlled_failure(*_args, **_kwargs):
        raise AssertionError("controlled extraction failure")

    def controlled_cleanup(arguments, **kwargs):
        cleanups.append((arguments, kwargs))
        return subprocess.CompletedProcess(arguments, 0, "", "")

    monkeypatch.setattr(integration, "run", controlled_failure)
    monkeypatch.setattr(integration.subprocess, "run", controlled_cleanup)

    # when / then
    with pytest.raises(AssertionError, match="controlled extraction failure"):
        integration.extract_pdf_text(pdf)
    assert len(cleanups) == 1
