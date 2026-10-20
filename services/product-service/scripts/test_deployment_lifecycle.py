"""Explicit disposable Kind integration for the Product deployment lifecycle."""

from __future__ import annotations

import pytest

import deployment_lifecycle

pytestmark = pytest.mark.integration


def test_product_deployment_lifecycle() -> None:
    # when
    status = deployment_lifecycle.main([])

    # then
    assert status == 0
