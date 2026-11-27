#!/usr/bin/env python3
"""Verify generated Payment Swagger against the independent HTTP contract fixture."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


class ContractError(ValueError):
    """The fixture or generated Swagger violates the owned HTTP contract."""


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
    definitions: dict[str, Any], name: str, expected_properties: dict[str, dict[str, Any]]
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
    for field, expected_shape in expected_properties.items():
        value = properties[field]
        require(isinstance(value, dict), f"Swagger property {name}.{field} must be an object")
        require(isinstance(expected_shape, dict), f"HTTP property {name}.{field} must be an object")
        for key, expected in expected_shape.items():
            require(
                value.get(key) == expected,
                f"Swagger property {name}.{field} {key} differs from the contract",
            )


def check_contract(contract_path: Path, swagger_path: Path) -> None:
    contract = load_object(contract_path, "HTTP contract")
    swagger = load_object(swagger_path, "generated Swagger")
    operations_contract = contract.get("operations")
    schemas_contract = contract.get("schemas")
    require(isinstance(operations_contract, list), "HTTP contract operations must be an array")
    require(isinstance(schemas_contract, dict), "HTTP contract schemas must be an object")
    assert isinstance(operations_contract, list)
    assert isinstance(schemas_contract, dict)

    require(swagger.get("swagger") == contract.get("swagger"), "Swagger version differs")
    paths = swagger.get("paths")
    require(isinstance(paths, dict), "generated Swagger paths must be an object")
    assert isinstance(paths, dict)
    expected_paths: set[str] = set()
    for operation_contract in operations_contract:
        require(isinstance(operation_contract, dict), "HTTP contract operation must be an object")
        path = operation_contract.get("path")
        require(isinstance(path, str) and path.startswith("/"), "HTTP contract path is invalid")
        expected_paths.add(path)
    require(
        len(expected_paths) == len(operations_contract),
        "HTTP contract operation paths must be unique",
    )
    require(set(paths) == expected_paths, "Swagger paths differ from the contract")

    for operation_contract in operations_contract:
        path = operation_contract.get("path")
        method = operation_contract.get("method")
        require(isinstance(path, str) and path.startswith("/"), "HTTP contract path is invalid")
        require(isinstance(method, str) and method.islower(), "HTTP contract method is invalid")
        path_item = paths[path]
        require(isinstance(path_item, dict), f"Swagger path {path} must be an object")
        require(set(path_item) == {method}, f"Swagger path {path} must expose only {method}")
        operation = path_item[method]
        require(isinstance(operation, dict), f"Swagger operation {path} must be an object")
        assert isinstance(operation, dict)
        require(
            operation.get("operationId") == operation_contract.get("operation_id"),
            f"Swagger operationId differs for {path}",
        )
        for media_field in ("consumes", "produces"):
            if media_field in operation_contract:
                expected = operation_contract[media_field]
                require(
                    expected == ["application/json"],
                    f"HTTP contract {path} {media_field} must be JSON only",
                )
                require(
                    operation.get(media_field) == expected,
                    f"Swagger {path} {media_field} differs",
                )
        expected_parameters = operation_contract.get("parameters", [])
        require(
            operation.get("parameters", []) == expected_parameters,
            f"Swagger {path} parameters differ",
        )
        responses_contract = operation_contract.get("responses")
        require(isinstance(responses_contract, dict), f"HTTP contract {path} responses invalid")
        responses = operation.get("responses")
        require(isinstance(responses, dict), f"Swagger {path} responses must be an object")
        assert isinstance(responses, dict)
        require(
            set(responses) == set(responses_contract),
            f"Swagger {path} response status set differs",
        )
        for status, expected in responses_contract.items():
            require(
                isinstance(expected, dict), f"HTTP contract response {status} must be an object"
            )
            actual = responses[status]
            require(isinstance(actual, dict), f"Swagger response {status} must be an object")
            require(
                actual.get("schema") == expected.get("schema"),
                f"Swagger {path} response {status} schema differs",
            )
            require(
                actual.get("examples", {}).get("application/json") == expected.get("example"),
                f"Swagger {path} response {status} example differs",
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
    print("check-gateway-contract: generated Swagger matches the independent HTTP contract")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
