#!/usr/bin/env python3
"""Verify generated Policy Search Swagger against the independent HTTP fixture."""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path
from typing import Any

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from scripts.go.service.tooling_config import ToolingConfigError  # noqa: E402
from scripts.go.service.tooling_contracts import load_json_object  # noqa: E402


class ContractError(ValueError):
    """The fixture or generated Swagger violates the owned search contract."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def load_object(path: Path, label: str) -> dict[str, Any]:
    try:
        value = load_json_object(path.parent, path.name)
    except ToolingConfigError as error:
        if "must contain a JSON object" in str(error):
            raise ContractError(f"{label} must be a JSON object") from error
        raise ContractError(f"{label} cannot be read as JSON: {path}: {error}") from error
    except (OSError, UnicodeError) as error:
        raise ContractError(f"{label} cannot be read as JSON: {path}: {error}") from error
    return value


def check_contract(contract_path: Path, swagger_path: Path) -> None:
    contract = load_object(contract_path, "HTTP contract")
    swagger = load_object(swagger_path, "generated Swagger")
    operation_contract = contract.get("operation")
    responses_contract = contract.get("responses")
    schemas_contract = contract.get("schemas")
    required_contract = contract.get("required_fields")
    require(isinstance(operation_contract, dict), "HTTP contract operation must be an object")
    require(isinstance(responses_contract, dict), "HTTP contract responses must be an object")
    require(isinstance(schemas_contract, dict), "HTTP contract schemas must be an object")
    require(isinstance(required_contract, dict), "HTTP contract required_fields must be an object")

    require(swagger.get("swagger") == contract.get("swagger"), "Swagger version differs")
    path = operation_contract.get("path")
    method = operation_contract.get("method")
    paths = swagger.get("paths")
    require(isinstance(paths, dict), "generated Swagger paths must be an object")
    require(set(paths) == {path}, f"Swagger paths must contain only {path}")
    path_item = paths[path]
    require(isinstance(path_item, dict), f"Swagger path {path} must be an object")
    require(set(path_item) == {method}, f"Swagger path {path} must expose only {method}")
    operation = path_item[method]
    require(isinstance(operation, dict), "Swagger search operation must be an object")
    require(
        operation.get("operationId") == operation_contract.get("operation_id"),
        "Swagger operationId differs",
    )
    require(
        operation.get("produces") == operation_contract.get("produces"), "Swagger produces differs"
    )
    require("consumes" not in operation, "GET search operation must not consume a body")

    parameters = operation.get("parameters")
    require(
        isinstance(parameters, list) and len(parameters) == 1,
        "Swagger must have one query parameter",
    )
    parameter = parameters[0]
    require(isinstance(parameter, dict), "Swagger query parameter must be an object")
    expected_parameter = operation_contract.get("parameter")
    require(isinstance(expected_parameter, dict), "HTTP contract parameter must be an object")
    for key, expected in expected_parameter.items():
        require(parameter.get(key) == expected, f"Swagger query parameter {key} differs")

    responses = operation.get("responses")
    require(isinstance(responses, dict), "Swagger responses must be an object")
    require(set(responses) == set(responses_contract), "Swagger response status set differs")
    for response_status, expected_example in responses_contract.items():
        actual = responses[response_status]
        require(isinstance(actual, dict), f"Swagger response {response_status} must be an object")
        expected_schema = (
            operation_contract.get("success_schema")
            if response_status == "200"
            else operation_contract.get("error_schema")
        )
        require(
            actual.get("schema") == {"$ref": expected_schema},
            f"Swagger response {response_status} schema differs",
        )
        require(
            actual.get("examples", {}).get("application/json") == expected_example,
            f"Swagger response {response_status} example differs",
        )
    require(
        responses["405"].get("headers", {}).get("Allow") == {"type": "string", "default": "GET"},
        "Swagger response 405 Allow header differs",
    )

    definitions = swagger.get("definitions")
    require(isinstance(definitions, dict), "generated Swagger definitions must be an object")
    require(set(definitions) == set(schemas_contract), "Swagger definition set differs")
    for name, expected_properties in schemas_contract.items():
        actual = definitions[name]
        require(actual.get("type") == "object", f"Swagger definition {name} must be an object")
        require(
            actual.get("properties") == expected_properties,
            f"Swagger definition {name} properties differ from the contract",
        )
        expected_required = required_contract.get(name)
        if expected_required is None:
            require(
                "required" not in actual,
                f"Swagger definition {name} has unexpected required fields",
            )
        else:
            require(
                actual.get("required") == expected_required,
                f"Swagger definition {name} required fields differ",
            )


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract", type=Path, required=True)
    parser.add_argument("--swagger", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    arguments = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        check_contract(arguments.contract, arguments.swagger)
    except ContractError as error:
        print(f"check-gateway-contract: {error}", file=sys.stderr)
        return 1
    print("check-gateway-contract: generated Swagger matches the independent search contract")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
