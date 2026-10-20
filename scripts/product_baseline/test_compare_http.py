"""Behavior tests for Product HTTP baseline comparison."""

from __future__ import annotations

import copy
import json
from pathlib import Path

import pytest

from scripts.product_baseline import compare_http

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
ACCEPTED = (
    REPOSITORY_ROOT
    / "legacy/product-service/src/test/resources/product-read-baseline/http/local-dev.json"
)


@pytest.fixture
def document() -> dict[str, object]:
    return json.loads(ACCEPTED.read_text(encoding="utf-8"))


@pytest.mark.parametrize("mutation", ["object-order", "volatile-header", "product-order"])
def test_comparator_accepts_documented_non_contract_differences(
    document: dict[str, object], mutation: str
) -> None:
    # given
    candidate = copy.deepcopy(document)
    observations = candidate["observations"]
    assert isinstance(observations, list)
    if mutation == "object-order":
        body = json.loads(observations[0]["response"]["rawBody"])
        observations[0]["response"]["rawBody"] = json.dumps(dict(reversed(body.items())))
    elif mutation == "volatile-header":
        observations[0]["response"]["headers"]["date"] = "volatile"
    else:
        body = json.loads(observations[0]["response"]["rawBody"])
        if isinstance(body, list):
            observations[0]["response"]["rawBody"] = json.dumps(list(reversed(body)))

    # when / then
    compare_http.compare_documents(document, candidate)


@pytest.mark.parametrize(
    ("mutation", "message"),
    [
        ("precision", "response JSON differs"),
        ("presence", "response JSON differs"),
        ("nested-order", "response JSON differs"),
        ("status", "status differs"),
        ("missing", "scenario set differs"),
    ],
)
def test_comparator_rejects_contract_drift(
    document: dict[str, object], mutation: str, message: str
) -> None:
    # given
    candidate = copy.deepcopy(document)
    observations = candidate["observations"]
    assert isinstance(observations, list)
    if mutation == "precision":
        selected = next(item for item in observations if "200000}" in item["response"]["rawBody"])
        selected["response"]["rawBody"] = selected["response"]["rawBody"].replace(
            "200000}", "200000.0}", 1
        )
    elif mutation == "presence":
        body = json.loads(observations[0]["response"]["rawBody"])
        body["name"] = None
        observations[0]["response"]["rawBody"] = json.dumps(body)
    elif mutation == "nested-order":
        selected = next(
            item for item in observations if item["scenarioId"] == "HTTP-DIRECT-GET-FAI-001"
        )
        body = json.loads(selected["response"]["rawBody"])
        body["covers"].reverse()
        selected["response"]["rawBody"] = json.dumps(body)
    elif mutation == "status":
        observations[0]["response"]["status"] = 201
    else:
        observations.pop()

    # when / then
    with pytest.raises(compare_http.ComparisonError, match=message):
        compare_http.compare_documents(document, candidate)
