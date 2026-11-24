"""Independent fixtures for the generated Policy Swagger contract check."""

from __future__ import annotations

import json
from collections.abc import Callable
from pathlib import Path
from typing import Any

import check_gateway_contract
import pytest

MODULE_ROOT = Path(__file__).resolve().parent.parent
CONTRACT = MODULE_ROOT / "testdata" / "http-contract.json"
SWAGGER = MODULE_ROOT / "gen/openapiv2/policy/v1/policy_service.swagger.json"


def test_current_generated_swagger_matches_contract() -> None:
    # when / then
    check_gateway_contract.check_contract(CONTRACT, SWAGGER)


def change_path(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/offers/changed"] = swagger["paths"].pop("/offers")


def change_status_example(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/offers"]["post"]["responses"]["503"]["examples"]["application/json"][
        "message"
    ] = "raw upstream failure"


def add_response(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/offers"]["post"]["responses"]["418"] = {"description": "unexpected"}


def add_path(swagger: dict[str, Any]) -> None:
    swagger["paths"]["/unexpected"] = {"post": {}}


def type_price_as_string(swagger: dict[str, Any]) -> None:
    swagger["definitions"]["v1CreateOfferHTTPResponse"]["properties"]["totalPrice"] = {
        "type": "string"
    }


def publish_rpc_request(swagger: dict[str, Any]) -> None:
    swagger["definitions"]["v1CreateOfferRequest"] = {
        "type": "object",
        "properties": {
            "productCode": {"type": "string"},
            "answers": {"type": "array", "items": {"$ref": "#/definitions/v1OfferAnswer"}},
        },
    }


def drop_answer_type(swagger: dict[str, Any]) -> None:
    del swagger["definitions"]["v1OfferHTTPAnswer"]["properties"]["type"]


def type_date_as_string(swagger: dict[str, Any]) -> None:
    swagger["definitions"]["v1CreateOfferHTTPRequest"]["properties"]["policyFrom"] = {
        "type": "string"
    }


def drop_not_found_response(swagger: dict[str, Any]) -> None:
    del swagger["paths"]["/offers"]["post"]["responses"]["404"]


@pytest.mark.parametrize(
    ("mutation", "message"),
    [
        (change_path, "paths must contain only"),
        (change_status_example, "response 503 example differs"),
        (add_response, "response status set differs"),
        (add_path, "paths must contain only /offers"),
        (type_price_as_string, "v1CreateOfferHTTPResponse.totalPrice differs"),
        (publish_rpc_request, "request body does not reference the HTTP request schema"),
        (drop_answer_type, "v1OfferHTTPAnswer properties differ"),
        (type_date_as_string, "v1CreateOfferHTTPRequest.policyFrom differs"),
        (drop_not_found_response, "/offers response status set differs"),
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


def test_contract_check_rejects_inconsistent_offer_answer_members(tmp_path: Path) -> None:
    # given
    contract = json.loads(CONTRACT.read_text(encoding="utf-8"))
    offer = next(item for item in contract["operations"] if item["path"] == "/offers")
    offer["request_contract"]["answer_members"] = ["questionCode", "answer"]
    changed = tmp_path / "contract.json"
    changed.write_text(json.dumps(contract), encoding="utf-8")

    # when / then
    with pytest.raises(check_gateway_contract.ContractError, match="HTTP answer members differ"):
        check_gateway_contract.check_contract(changed, SWAGGER)
