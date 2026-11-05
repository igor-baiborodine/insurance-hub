#!/usr/bin/env python3
"""Verify generated total-sales Swagger against the independent HTTP contract fixture."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


class ContractError(ValueError):
    """The fixture or generated Swagger violates the owned total-sales contract."""


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


def require_schema(
    definitions: dict[str, Any], name: str, expected_properties: dict[str, str]
) -> None:
    schema = definitions.get(name)
    require(isinstance(schema, dict), f"Swagger definition is missing: {name}")
    assert isinstance(schema, dict)
    require(schema.get("type") == "object", f"Swagger definition {name} must be an object")
    properties = schema.get("properties")
    require(isinstance(properties, dict), f"Swagger definition {name} lacks properties")
    assert isinstance(properties, dict)
    require(
        set(properties) == set(expected_properties),
        f"Swagger definition {name} properties differ from the contract",
    )
    for field, expected_type in expected_properties.items():
        value = properties[field]
        require(isinstance(value, dict), f"Swagger property {name}.{field} must be an object")
        actual_type = value.get("type")
        if actual_type is None and isinstance(value.get("$ref"), str):
            actual_type = "object"
        require(
            actual_type == expected_type,
            f"Swagger property {name}.{field} must have type {expected_type}",
        )


def check_contract(contract_path: Path, swagger_path: Path) -> None:
    contract = load_object(contract_path, "HTTP contract")
    swagger = load_object(swagger_path, "generated Swagger")
    operation_contract = contract.get("operation")
    responses_contract = contract.get("responses")
    schemas_contract = contract.get("schemas")
    require(isinstance(operation_contract, dict), "HTTP contract operation must be an object")
    require(isinstance(responses_contract, dict), "HTTP contract responses must be an object")
    require(isinstance(schemas_contract, dict), "HTTP contract schemas must be an object")
    assert isinstance(operation_contract, dict)
    assert isinstance(responses_contract, dict)
    assert isinstance(schemas_contract, dict)

    require(swagger.get("swagger") == contract.get("swagger"), "Swagger version differs")
    path = operation_contract.get("path")
    method = operation_contract.get("method")
    require(isinstance(path, str) and path.startswith("/"), "HTTP contract path is invalid")
    require(isinstance(method, str) and method.islower(), "HTTP contract method is invalid")
    paths = swagger.get("paths")
    require(isinstance(paths, dict), "generated Swagger paths must be an object")
    assert isinstance(paths, dict)
    require(set(paths) == {path}, f"Swagger paths must contain only {path}")
    path_item = paths[path]
    require(isinstance(path_item, dict), f"Swagger path {path} must be an object")
    require(set(path_item) == {method}, f"Swagger path {path} must expose only {method}")
    operation = path_item[method]
    require(isinstance(operation, dict), "Swagger total-sales operation must be an object")
    assert isinstance(operation, dict)

    require(
        operation.get("operationId") == operation_contract.get("operation_id"),
        "Swagger operationId differs",
    )
    for media_field in ("consumes", "produces"):
        expected = operation_contract.get(media_field)
        require(expected == ["application/json"], f"HTTP contract {media_field} must be JSON only")
        require(operation.get(media_field) == expected, f"Swagger {media_field} differs")

    parameters = operation.get("parameters")
    require(isinstance(parameters, list) and len(parameters) == 1, "Swagger must have one body")
    body = parameters[0]
    require(isinstance(body, dict), "Swagger body parameter must be an object")
    require(
        body.get("name") == "body" and body.get("in") == "body" and body.get("required") is True,
        "Swagger body parameter differs",
    )
    require(
        body.get("schema") == {"$ref": operation_contract.get("request_schema")},
        "Swagger request schema differs",
    )

    responses = operation.get("responses")
    require(isinstance(responses, dict), "Swagger responses must be an object")
    assert isinstance(responses, dict)
    require(set(responses) == set(responses_contract), "Swagger response status set differs")
    for status, expected in responses_contract.items():
        require(isinstance(expected, dict), f"HTTP contract response {status} must be an object")
        actual = responses[status]
        require(isinstance(actual, dict), f"Swagger response {status} must be an object")
        expected_schema = (
            operation_contract.get("success_schema")
            if status == "200"
            else operation_contract.get("error_schema")
        )
        require(
            actual.get("schema") == {"$ref": expected_schema},
            f"Swagger response {status} schema differs",
        )
        require(
            actual.get("examples", {}).get("application/json") == expected,
            f"Swagger response {status} example differs",
        )

    definitions = swagger.get("definitions")
    require(isinstance(definitions, dict), "generated Swagger definitions must be an object")
    assert isinstance(definitions, dict)
    for name, expected_properties in schemas_contract.items():
        require(
            isinstance(expected_properties, dict),
            f"HTTP contract schema {name} must be an object",
        )
        require_schema(definitions, name, expected_properties)


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
    print("check-gateway-contract: generated Swagger matches the independent total-sales contract")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
