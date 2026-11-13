#!/usr/bin/env python3
"""Verify generated Document lookup Swagger against the independent HTTP contract fixture."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


class ContractError(ValueError):
    """The fixture or generated Swagger violates the owned lookup contract."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ContractError(message)


def load_object(path: Path, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ContractError(f"{label} cannot be read as JSON: {path}: {error}") from error
    require(isinstance(value, dict), f"{label} must be a JSON object")
    return value


def object_value(value: object, label: str) -> dict[str, Any]:
    require(isinstance(value, dict), f"{label} must be an object")
    assert isinstance(value, dict)
    return value


def check_contract(contract_path: Path, swagger_path: Path) -> None:
    contract = load_object(contract_path, "HTTP contract")
    swagger = load_object(swagger_path, "generated Swagger")
    operation_contract = object_value(contract.get("operation"), "HTTP contract operation")
    responses_contract = object_value(contract.get("responses"), "HTTP contract responses")
    schemas_contract = object_value(contract.get("schemas"), "HTTP contract schemas")

    require(swagger.get("swagger") == contract.get("swagger"), "Swagger version differs")
    path = operation_contract.get("path")
    method = operation_contract.get("method")
    require(isinstance(path, str) and path.startswith("/"), "HTTP contract path is invalid")
    require(isinstance(method, str) and method.islower(), "HTTP contract method is invalid")
    paths = object_value(swagger.get("paths"), "generated Swagger paths")
    require(set(paths) == {path}, f"Swagger paths must contain only {path}")
    path_item = object_value(paths[path], f"Swagger path {path}")
    require(set(path_item) == {method}, f"Swagger path {path} must expose only {method}")
    operation = object_value(path_item[method], "Swagger lookup operation")
    require(
        operation.get("operationId") == operation_contract.get("operation_id"),
        "Swagger operationId differs",
    )
    require(operation.get("produces") == ["application/json"], "Swagger produces differs")
    require("consumes" not in operation, "GET lookup must not consume a request body")

    parameters = operation.get("parameters")
    require(
        isinstance(parameters, list) and len(parameters) == 1,
        "Swagger must have one path parameter",
    )
    require(parameters[0] == operation_contract.get("parameter"), "Swagger path parameter differs")

    responses = object_value(operation.get("responses"), "Swagger responses")
    require(set(responses) == set(responses_contract), "Swagger response status set differs")
    for response_status, expected in responses_contract.items():
        actual = object_value(responses[response_status], f"Swagger response {response_status}")
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
            actual.get("examples", {}).get("application/json") == expected,
            f"Swagger response {response_status} example differs",
        )

    definitions = object_value(swagger.get("definitions"), "Swagger definitions")
    document = object_value(definitions.get(schemas_contract.get("document")), "Document schema")
    require(
        document.get("properties")
        == {
            "policyNumber": {"type": "string"},
            "content": {"type": "string", "format": "byte"},
        },
        "Document schema differs",
    )
    response = object_value(definitions.get(schemas_contract.get("response")), "response schema")
    require(
        response.get("properties")
        == {
            "documents": {
                "type": "array",
                "items": {"type": "object", "$ref": "#/definitions/v1Document"},
            }
        },
        "response envelope schema differs",
    )
    status_schema = object_value(definitions.get(schemas_contract.get("status")), "status schema")
    require(
        set(object_value(status_schema.get("properties"), "status properties"))
        == {"code", "message", "details"},
        "status schema differs",
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
    print("check-gateway-contract: generated Swagger matches the independent Document contract")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
