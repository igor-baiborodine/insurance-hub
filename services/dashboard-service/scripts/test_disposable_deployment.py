"""Owned disposable Dashboard deployment acceptance."""

from __future__ import annotations

import disposable_deployment
import pytest

pytestmark = pytest.mark.integration


def test_dashboard_disposable_deployment() -> None:
    disposable_deployment.run()
