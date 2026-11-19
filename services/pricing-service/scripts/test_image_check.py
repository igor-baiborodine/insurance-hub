"""Focused Pricing runtime-image contract checks."""

from __future__ import annotations

import image_check
import pytest


def valid_inventory() -> set[str]:
    return {
        "pricing-service",
        "pricing-migrate",
        "pricing-seed",
        "etc/ssl/certs/ca-certificates.crt",
    }


@pytest.mark.parametrize(
    ("defect", "message"),
    [
        ("pricing-migrate", "missing packaged files"),
        ("etc/ssl/certs/ca-certificates.crt", "CA certificate bundle"),
    ],
)
def test_validate_image_contents_rejects_missing_runtime_file(defect: str, message: str) -> None:
    # given
    contents = valid_inventory() - {defect}

    # when / then
    with pytest.raises(image_check.ImageCheckError, match=message):
        image_check.validate_image_contents(contents)


@pytest.mark.parametrize("leak", ["bin/sh", "scripts/administration.py", "service_test.go"])
def test_validate_image_contents_rejects_development_leak(leak: str) -> None:
    # given
    contents = valid_inventory() | {leak}

    # when / then
    with pytest.raises(image_check.ImageCheckError):
        image_check.validate_image_contents(contents)
