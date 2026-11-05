"""Tests for pull-request Go module selection."""

from __future__ import annotations

import pytest

import select_pr_module


@pytest.mark.parametrize(
    ("paths", "expected"),
    [
        ([], {"module_dir": ""}),
        (["docs/readme.md"], {"module_dir": ""}),
        (["libs/servicekit/README.md"], {"module_dir": ""}),
        (["templates/go-service/cmd/server/main.go"], {"module_dir": "templates/go-service"}),
        (
            ["templates/go-service/gen/openapiv2/scaffold/v1/example_service.swagger.json"],
            {"module_dir": "templates/go-service"},
        ),
        (
            ["libs/servicekit/README.md", "templates/go-service/go.mod"],
            {"module_dir": "templates/go-service"},
        ),
        (
            [
                "services/product-service/scripts/check_ci.py",
                "templates/go-service/cmd/server/main.go",
            ],
            {"module_dir": "templates/go-service"},
        ),
        (["services/product-service/Makefile"], {"module_dir": ""}),
        (["libs/servicekit/health/health.go"], {"module_dir": "libs/servicekit"}),
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


def test_policy_search_uploads_every_check_go_pr_coverage_profile() -> None:
    # when
    result = select_pr_module.selection_outputs(
        ["services/policy-search-service/internal/service/service.go"],
        "a" * 40,
    )

    # then
    assert result["coverage_files"].split(",") == [
        "services/policy-search-service/coverage.txt",
        "services/policy-search-service/integration-coverage.txt",
        "services/policy-search-service/elasticsearch-integration-coverage.txt",
        "services/policy-search-service/event-integration-coverage.txt",
    ]


def test_product_service_keeps_its_owned_coverage_profiles() -> None:
    # when
    result = select_pr_module.selection_outputs(
        ["services/product-service/internal/app.go"],
        "a" * 40,
    )

    # then
    assert result["coverage_files"].split(",") == [
        "services/product-service/coverage.txt",
        "services/product-service/integration-coverage.txt",
    ]


def test_dashboard_service_selects_standard_business_service_coverage() -> None:
    # when
    result = select_pr_module.selection_outputs(
        ["services/dashboard-service/internal/service/echo.go"],
        "a" * 40,
    )

    # then
    assert result["coverage_files"].split(",") == [
        "services/dashboard-service/coverage.txt",
        "services/dashboard-service/integration-coverage.txt",
    ]
    assert result["coverage_flag"] == "dashboard-service"


@pytest.mark.parametrize(
    "path",
    [
        "templates/go-service/main.go",
        "templates/go-service/go.mod",
        "templates/go-service/api/scaffold/v1/example.proto",
        "templates/go-service/gen/openapiv2/scaffold/v1/example.swagger.json",
        "templates/go-service/internal/catalog/testdata/case.json",
        "services/product-service/internal/postgres/sql/schema.sql",
    ],
)
def test_go_pr_qualifying_paths_match_explicit_workflow_categories(path: str) -> None:
    assert select_pr_module.is_go_pr_qualifying_module_path(path)


@pytest.mark.parametrize(
    "path",
    [
        "templates/go-service/Makefile",
        "templates/go-service/README.md",
        "templates/go-service/scripts/check_copy.py",
        "services/product-service/Dockerfile",
        "services/product-service/scripts/check_ci.py",
        "legacy/product-service/internal/incorrect.go",
        "services/group/claims-service/internal/incorrect.go",
        "libs/servicekit/health/health.go",
    ],
)
def test_go_pr_nonqualifying_paths_remain_local_or_separately_owned(path: str) -> None:
    assert not select_pr_module.is_go_pr_qualifying_module_path(path)
