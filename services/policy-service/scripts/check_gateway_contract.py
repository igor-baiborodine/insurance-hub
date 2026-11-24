#!/usr/bin/env python3
"""Verify the generated Policy Swagger against the independent HTTP contract fixture."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


class ContractError(ValueError):
    """The fixture or generated Swagger violates the owned Policy HTTP contract."""


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


def require_object(value: Any, message: str) -> dict[str, Any]:
    require(isinstance(value, dict), message)
    assert isinstance(value, dict)
    return value


def require_schema(
    definitions: dict[str, Any], name: str, expected_properties: dict[str, Any]
) -> None:
    schema = require_object(definitions.get(name), f"Swagger definition is missing: {name}")
    require(schema.get("type") == "object", f"Swagger definition {name} must be an object")
    properties = require_object(
        schema.get("properties"), f"Swagger definition {name} lacks properties"
    )
    require(
        set(properties) == set(expected_properties),
        f"Swagger definition {name} properties differ from the contract",
    )
    for field, expected in expected_properties.items():
        require(
            properties[field] == expected,
            f"Swagger property {name}.{field} differs from the contract",
        )


def definition_name(reference: Any) -> str:
    prefix = "#/definitions/"
    require(
        isinstance(reference, str) and reference.startswith(prefix),
        f"HTTP contract schema reference is invalid: {reference}",
    )
    return reference.removeprefix(prefix)


def check_request_contract(
    operation_id: str,
    request_contract: dict[str, Any],
    request_schema: str,
    schemas: dict[str, Any],
    definitions: dict[str, Any],
) -> None:
    body_schema = request_schema
    http_reference = request_contract.get("http_request_schema")
    if http_reference is not None:
        # The published body must be the documented HTTP shape, never the RPC message.
        require(
            definitions.get(request_schema) == {"$ref": http_reference},
            f"{operation_id} request body does not reference the HTTP request schema",
        )
        body_schema = definition_name(http_reference)
    require(
        request_contract.get("json_members") == list(schemas.get(body_schema, {})),
        f"{operation_id} HTTP request members differ from the published request schema",
    )
    if "answer_members" in request_contract:
        answers = require_object(
            schemas.get(body_schema, {}).get("answers"),
            f"{operation_id} published request schema lacks answers",
        )
        answer_schema = definition_name(answers.get("items", {}).get("$ref"))
        require(
            request_contract["answer_members"] == list(schemas.get(answer_schema, {})),
            f"{operation_id} HTTP answer members differ",
        )


def check_operation(
    operation_contract: dict[str, Any],
    paths: dict[str, Any],
    schemas: dict[str, Any],
    definitions: dict[str, Any],
) -> None:
    path = operation_contract.get("path")
    method = operation_contract.get("method")
    require(isinstance(path, str) and path.startswith("/"), "HTTP contract path is invalid")
    require(isinstance(method, str) and method.islower(), "HTTP contract method is invalid")
    path_item = require_object(paths.get(path), f"Swagger path {path} must be an object")
    require(set(path_item) == {method}, f"Swagger path {path} must expose only {method}")
    operation = require_object(path_item[method], f"Swagger {path} operation must be an object")
    operation_id = operation_contract.get("operation_id")
    require(operation.get("operationId") == operation_id, f"Swagger {path} operationId differs")
    for media_field in ("consumes", "produces"):
        expected = operation_contract.get(media_field)
        require(expected == ["application/json"], f"HTTP contract {media_field} must be JSON only")
        require(operation.get(media_field) == expected, f"Swagger {path} {media_field} differs")

    parameters = operation.get("parameters")
    require(isinstance(parameters, list) and len(parameters) == 1, "Swagger must have one body")
    assert isinstance(parameters, list)
    body = require_object(parameters[0], "Swagger body parameter must be an object")
    require(
        body.get("name") == "body" and body.get("in") == "body" and body.get("required") is True,
        f"Swagger {path} body parameter differs",
    )
    require(
        body.get("schema") == {"$ref": operation_contract.get("request_schema")},
        f"Swagger {path} request schema differs",
    )
    request_contract = require_object(
        operation_contract.get("request_contract"),
        f"HTTP contract {path} request contract must be an object",
    )
    check_request_contract(
        str(operation_id),
        request_contract,
        definition_name(operation_contract.get("request_schema")),
        schemas,
        definitions,
    )

    responses_contract = require_object(
        operation_contract.get("responses"), f"HTTP contract {path} responses must be an object"
    )
    responses = require_object(operation.get("responses"), "Swagger responses must be an object")
    require(
        set(responses) == set(responses_contract), f"Swagger {path} response status set differs"
    )
    for status, expected in responses_contract.items():
        require(isinstance(expected, dict), f"HTTP contract response {status} must be an object")
        actual = require_object(responses[status], f"Swagger response {status} must be an object")
        expected_schema = (
            operation_contract.get("success_schema")
            if status == "200"
            else operation_contract.get("error_schema")
        )
        require(
            actual.get("schema") == {"$ref": expected_schema},
            f"Swagger {path} response {status} schema differs",
        )
        require(
            actual.get("examples", {}).get("application/json") == expected,
            f"Swagger {path} response {status} example differs",
        )


def check_contract(contract_path: Path, swagger_path: Path) -> None:
    contract = load_object(contract_path, "HTTP contract")
    swagger = load_object(swagger_path, "generated Swagger")
    operations = contract.get("operations")
    require(
        isinstance(operations, list) and bool(operations),
        "HTTP contract operations must be a non-empty list",
    )
    assert isinstance(operations, list)
    schemas = require_object(contract.get("schemas"), "HTTP contract schemas must be an object")

    require(swagger.get("swagger") == contract.get("swagger"), "Swagger version differs")
    paths = require_object(swagger.get("paths"), "generated Swagger paths must be an object")
    expected_paths = sorted(
        operation.get("path") for operation in operations if isinstance(operation, dict)
    )
    require(
        sorted(paths) == expected_paths,
        f"Swagger paths must contain only {', '.join(map(str, expected_paths))}",
    )
    definitions = require_object(
        swagger.get("definitions"), "generated Swagger definitions must be an object"
    )
    for operation in operations:
        check_operation(
            require_object(operation, "HTTP contract operation must be an object"),
            paths,
            schemas,
            definitions,
        )
    for name, expected_properties in schemas.items():
        require_schema(
            definitions,
            name,
            require_object(expected_properties, f"HTTP contract schema {name} must be an object"),
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
    print("check-gateway-contract: generated Swagger matches the independent Policy contract")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
