"""Tests for pull-request Go module selection."""

from __future__ import annotations

import pytest

import select_pr_module


@pytest.mark.parametrize(
    ("paths", "expected"),
    [
        ([], {"module_dir": ""}),
        (["docs/readme.md"], {"module_dir": ""}),
        (["templates/go-service/cmd/server/main.go"], {"module_dir": "templates/go-service"}),
        (["services/product-service/internal/app.go"], {"module_dir": "services/product-service"}),
    ],
)
def test_selection_preserves_zero_or_single_module_boundary(
    paths: list[str], expected: dict[str, str]
) -> None:
    # when
    result = select_pr_module.selection_outputs(paths, "a" * 40)

    # then
    assert result["module_dir"] == expected["module_dir"]
    if result["module_dir"]:
        assert result["format_base"] == "a" * 40
        assert result["validation_target"] == "check-go-pr"


def test_multiple_modules_are_rejected() -> None:
    # given
    paths = [
        "templates/go-service/main.go",
        "services/product-service/internal/app.go",
    ]

    # when / then
    with pytest.raises(select_pr_module.SelectionError, match="Multiple Go modules changed"):
        select_pr_module.selection_outputs(paths, "a" * 40)
