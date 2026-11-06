"""Independent fixtures for the generated Dashboard Swagger contract check."""

from __future__ import annotations

import json
from collections.abc import Callable
from pathlib import Path
from typing import Any

import check_gateway_contract
import pytest

MODULE_ROOT = Path(__file__).resolve().parent.parent
CONTRACT = MODULE_ROOT / "testdata" / "http-contract.json"
SWAGGER = MODULE_ROOT / "gen/openapiv2/dashboard/v1/dashboard_service.swagger.json"


def test_current_generated_swagger_matches_contract() -> None:
    # when / then
    check_gateway_contract.check_contract(CONTRACT, SWAGGER)


def remove_agent_path(swagger: dict[str, Any]) -> None:
    del swagger["paths"]["/dashboard/agentssales"]


def change_status_example(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/dashboard/agentssales"]["post"]["responses"]["503"]["examples"][
        "application/json"
    ]["message"] = "raw upstream failure"


def add_response(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/dashboard/totalsales"]["post"]["responses"]["418"] = {
        "description": "unexpected"
    }


def quote_policy_count(swagger: dict[str, Any]) -> None:
    swagger["definitions"]["v1TotalSalesHTTPValue"]["properties"]["policiesCount"]["type"] = (
        "string"
    )


def quote_agent_money(swagger: dict[str, Any]) -> None:
    swagger["definitions"]["v1AgentSalesHTTPValue"]["properties"]["premiumAmount"]["type"] = (
        "string"
    )


@pytest.mark.parametrize(
    ("mutation", "message"),
    [
        (remove_agent_path, "paths differ from the contract"),
        (change_status_example, "agent_sales response 503 example differs"),
        (add_response, "total_sales response status set differs"),
        (quote_policy_count, "v1TotalSalesHTTPValue.policiesCount must have type integer"),
        (quote_agent_money, "v1AgentSalesHTTPValue.premiumAmount must have type number"),
    ],
)
def test_contract_check_rejects_controlled_swagger_drift(
    tmp_path: Path,
    mutation: Callable[[dict[str, Any]], None],
    message: str,
) -> None:
    # given
    swagger = json.loads(SWAGGER.read_text(encoding="utf-8"))
    mutation(swagger)
    changed = tmp_path / "changed.swagger.json"
    changed.write_text(json.dumps(swagger), encoding="utf-8")

    # when / then
    with pytest.raises(check_gateway_contract.ContractError, match=message):
        check_gateway_contract.check_contract(CONTRACT, changed)


def test_main_reports_malformed_contract_without_traceback(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    malformed = tmp_path / "contract.json"
    malformed.write_text("[]\n", encoding="utf-8")

    # when
    status = check_gateway_contract.main(["--contract", str(malformed), "--swagger", str(SWAGGER)])

    # then
    assert status == 1
    assert "HTTP contract must be a JSON object" in capsys.readouterr().err
