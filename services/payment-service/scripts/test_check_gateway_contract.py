"""Independent fixtures for the generated Payment Swagger contract check."""

from __future__ import annotations

import json
from collections.abc import Callable
from pathlib import Path
from typing import Any

import check_gateway_contract
import pytest

MODULE_ROOT = Path(__file__).resolve().parent.parent
CONTRACT = MODULE_ROOT / "testdata" / "http-contract.json"
SWAGGER = MODULE_ROOT / "gen/openapiv2/payment/v1/payment_service.swagger.json"


def test_current_generated_swagger_matches_contract() -> None:
    # when / then
    check_gateway_contract.check_contract(CONTRACT, SWAGGER)


def change_path(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/payment/changed"] = swagger["paths"].pop("/payment/accounts")


def change_status_example(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/payment/accounts/{accountNumber}"]["get"]["responses"]["500"]["examples"][
        "application/json"
    ]["message"] = "raw upstream failure"


def add_response(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/payment/accounts"]["get"]["responses"]["418"] = {"description": "unexpected"}


def remove_required_field(swagger: dict[str, Any]) -> None:
    swagger["definitions"]["v1LegacyError"]["required"].remove("message")


@pytest.mark.parametrize(
    ("mutation", "message"),
    [
        (change_path, "paths differ from the contract"),
        (change_status_example, "response 500 example differs"),
        (add_response, "response status set differs"),
        (remove_required_field, "required fields differ from the contract"),
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
