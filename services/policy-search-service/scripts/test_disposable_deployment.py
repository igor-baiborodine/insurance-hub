"""Explicit disposable Kind-namespace integration for Policy Search."""

from __future__ import annotations

import disposable_deployment
import pytest

pytestmark = pytest.mark.integration


def test_policy_search_disposable_deployment() -> None:
    assert disposable_deployment.main() == 0
