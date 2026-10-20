#!/usr/bin/env python3
"""Validate accepted Product baseline fixtures with native JSON handling."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
from collections.abc import Iterable, Sequence
from pathlib import Path
from typing import Any

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
FIXTURE_ROOT = REPOSITORY_ROOT / "legacy/product-service/src/test/resources/product-read-baseline"
ENVIRONMENTS = ("local-dev", "qa")
PARTS = ("inventory", "catalog", "http", "data-edges", "failures", "access", "db-permissions")
HEX_32 = re.compile(r"[0-9a-f]{32}")
HEX_40 = re.compile(r"[0-9a-f]{40}")
HEX_64 = re.compile(r"[0-9a-f]{64}")
TIMESTAMP = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z")
BEARER = re.compile(r"Bearer\s+[A-Za-z0-9._-]+")
MANIFEST = FIXTURE_ROOT / "manifest.json"
EXPECTED_CASE_IDS = {
    "data-edges": {
        "DATA-EDGES-ABSENT-001",
        "DATA-EDGES-CHOICE-PRESENCE-001",
        "DATA-EDGES-EMPTY-001",
        "DATA-EDGES-NULL-COVERS-001",
        "DATA-EDGES-NULL-QUESTIONS-001",
        "DATA-EDGES-PRIMITIVE-NULL-001",
        "DATA-EDGES-PRODUCT-ORDER-001",
        "DATA-EDGES-RICH-001",
        "DATA-EDGES-SQL-NULL-CODE-001",
        "DATA-EDGES-SQL-NULL-DEFINITION-001",
        "DATA-EDGES-SUBTYPE-ABSENT-001",
        "DATA-EDGES-SUBTYPE-NULL-001",
        "DATA-EDGES-SUBTYPE-UNKNOWN-001",
        "DATA-EDGES-UNKNOWN-CHOICE-001",
        "DATA-EDGES-UNKNOWN-COVER-001",
        "DATA-EDGES-UNKNOWN-QUESTION-001",
    },
    "failures": {
        "FAIL-GATEWAY-DATABASE-GET-001",
        "FAIL-GATEWAY-DATABASE-LIST-001",
        "FAIL-GATEWAY-DECODE-STRUCTURE-001",
        "FAIL-GATEWAY-DECODE-TYPE-001",
        "FAIL-GATEWAY-EMPTY-LIST-001",
        "FAIL-GATEWAY-GET-ERROR-001",
        "FAIL-GATEWAY-GET-UNAVAILABLE-001",
        "FAIL-GATEWAY-LIST-ERROR-001",
        "FAIL-GATEWAY-LIST-UNAVAILABLE-001",
        "FAIL-GATEWAY-LOOKUP-BAD-ENCODING-001",
        "FAIL-GATEWAY-LOOKUP-EMPTY-SEGMENT-001",
        "FAIL-GATEWAY-LOOKUP-ENCODED-SLASH-001",
        "FAIL-GATEWAY-LOOKUP-LOWERCASE-001",
        "FAIL-GATEWAY-LOOKUP-WHITESPACE-001",
        "FAIL-GATEWAY-MISSING-001",
        "FAIL-GATEWAY-SINGLE-GET-001",
        "FAIL-GATEWAY-SINGLE-LIST-001",
        "FAIL-PRODUCT-DATABASE-GET-001",
        "FAIL-PRODUCT-DATABASE-LIST-001",
        "FAIL-PRODUCT-DECODE-STRUCTURE-001",
        "FAIL-PRODUCT-DECODE-TYPE-001",
        "FAIL-PRODUCT-EMPTY-LIST-001",
        "FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001",
        "FAIL-PRODUCT-LOOKUP-EMPTY-SEGMENT-001",
        "FAIL-PRODUCT-LOOKUP-ENCODED-SLASH-001",
        "FAIL-PRODUCT-LOOKUP-LOWERCASE-001",
        "FAIL-PRODUCT-LOOKUP-WHITESPACE-001",
        "FAIL-PRODUCT-MISSING-001",
        "FAIL-PRODUCT-SINGLE-GET-001",
        "FAIL-PRODUCT-SINGLE-LIST-001",
    },
    "db-permissions": {
        "DB-DENY-ALTER-001",
        "DB-DENY-CREATE-001",
        "DB-DENY-DELETE-001",
        "DB-DENY-DROP-001",
        "DB-DENY-INSERT-001",
        "DB-DENY-TRUNCATE-001",
        "DB-DENY-UPDATE-001",
        "DB-READ-CONNECT-001",
        "DB-READ-GET-001",
        "DB-READ-LIST-001",
    },
}


class FixtureError(ValueError):
    """A Product baseline fixture violates its recorded schema or safety contract."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise FixtureError(message)


def load_document(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise FixtureError(f"cannot read JSON fixture {path}: {error}") from error
    require(isinstance(value, dict), f"fixture must contain a JSON object: {path}")
    return value


def strings(value: Any) -> Iterable[str]:
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for key, item in value.items():
            yield str(key)
            yield from strings(item)


def string_values(value: Any) -> Iterable[str]:
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from string_values(item)
    elif isinstance(value, list):
        for item in value:
            yield from string_values(item)
    elif isinstance(value, list):
        for item in value:
            yield from strings(item)


def fixture_files(part: str, environment: str | None, explicit_file: str | None) -> list[Path]:
    if explicit_file:
        return [Path(explicit_file)]
    if part in {"data-edges", "failures", "db-permissions"}:
        return [FIXTURE_ROOT / part / "cases.json"]
    if environment:
        require(
            environment in ENVIRONMENTS,
            f"Unsupported BASELINE_ENV '{environment}'; use local-dev or qa.",
        )
        return [FIXTURE_ROOT / part / f"{environment}.json"]
    return [FIXTURE_ROOT / part / f"{name}.json" for name in ENVIRONMENTS]


def common_captured(document: dict[str, Any], part: str, path: Path) -> str:
    environment = document.get("environment")
    require(environment in ENVIRONMENTS, f"unsupported fixture environment in {path}")
    require(document.get("schemaVersion") == 1, f"invalid schemaVersion in {path}")
    require(document.get("status") == "captured", f"invalid status in {path}")
    require(document.get("captureProfile") == part, f"invalid captureProfile in {path}")
    require(
        document.get("provenance") == "captured-representative", f"invalid provenance in {path}"
    )
    require(
        bool(TIMESTAMP.fullmatch(str(document.get("capturedAt", "")))),
        f"invalid capture timestamp in {path}",
    )
    require(
        bool(HEX_40.fullmatch(str(document.get("sourceRevision", "")))),
        f"invalid source revision in {path}",
    )
    return str(environment)


def validate_inventory(document: dict[str, Any], path: Path) -> str:
    environment = document.get("environment")
    require(environment in ENVIRONMENTS, f"unsupported fixture environment in {path}")
    expected_scenario = "INV-LOCAL-001" if environment == "local-dev" else "INV-QA-001"
    require(document.get("schemaVersion") == 1, f"invalid schemaVersion in {path}")
    require(document.get("scenarioId") == expected_scenario, f"invalid scenario identity in {path}")
    require(document.get("status") == "captured", f"invalid status in {path}")
    require(document.get("captureProfile") == "inventory", f"invalid captureProfile in {path}")
    require(document.get("provenance") == "captured-observation", f"invalid provenance in {path}")
    require(
        document.get("credentialCategory") == "database-capture",
        f"invalid credential category in {path}",
    )
    require(
        bool(TIMESTAMP.fullmatch(str(document.get("capturedAt", "")))),
        f"invalid capture timestamp in {path}",
    )
    require(
        bool(HEX_40.fullmatch(str(document.get("sourceRevision", "")))),
        f"invalid source revision in {path}",
    )
    kubernetes = document.get("kubernetes")
    postgres = document.get("postgres")
    require(
        isinstance(kubernetes, dict) and isinstance(postgres, dict),
        f"missing inventory records in {path}",
    )
    expected_target = (
        ("kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all")
        if environment == "local-dev"
        else ("qa-insurance-hub", "qa-svc", "qa-data")
    )
    require(
        (
            kubernetes.get("context"),
            kubernetes.get("namespaces", {}).get("service"),
            kubernetes.get("namespaces", {}).get("data"),
        )
        == expected_target,
        f"wrong Kubernetes target in {path}",
    )
    workloads = kubernetes.get("workloads", {})
    services = kubernetes.get("services", {})
    for name in ("product", "gateway"):
        workload_record = workloads.get(name, {})
        service = services.get(name, {})
        require(
            isinstance(workload_record.get("readyReplicas"), int)
            and workload_record["readyReplicas"] > 0
            and any(pod.get("ready") == "True" for pod in workload_record.get("pods", [])),
            f"{name} workload is not ready in {path}",
        )
        require(
            bool(service.get("readyEndpointAddresses")),
            f"{name} service has no ready endpoint in {path}",
        )
    require(
        kubernetes.get("postgresCluster", {}).get("readyInstances", 0) > 0,
        f"PostgreSQL cluster is not ready in {path}",
    )
    require(
        document.get("connection", {}).get("forwardVerified") is True
        and document.get("connection", {}).get("transactionReadOnly") is True,
        f"inventory connection was not verified read-only in {path}",
    )
    require(postgres.get("database") == "product", f"wrong database identity in {path}")
    require(postgres.get("schema") == "public", f"wrong schema identity in {path}")
    table = postgres.get("table")
    require(
        isinstance(table, dict) and table.get("qualifiedName") == "public.product",
        f"wrong table identity in {path}",
    )
    require(table.get("owner") == "product", f"wrong table owner in {path}")
    expected_columns = [
        {
            "name": "code",
            "underlyingType": "varchar",
            "characterMaximumLength": 255,
            "nullable": False,
        },
        {
            "name": "definition",
            "underlyingType": "jsonb",
            "characterMaximumLength": None,
            "nullable": False,
        },
    ]
    columns = [
        {
            key: column.get(key)
            for key in ("name", "underlyingType", "characterMaximumLength", "nullable")
        }
        for column in table.get("columns", [])
    ]
    require(columns == expected_columns, f"wrong Product table columns in {path}")
    require(
        any(
            item.get("type") == "p" and item.get("definition") == "PRIMARY KEY (code)"
            for item in table.get("constraints", [])
        )
        and bool(table.get("indexes")),
        f"wrong Product table constraints in {path}",
    )
    privileges = postgres.get("effectivePrivileges", {})
    require(
        bool(postgres.get("roleMemberships"))
        and privileges.get("database", {}).get("connect") is True
        and privileges.get("schema", {}).get("usage") is True
        and privileges.get("table", {}).get("select") is True,
        f"wrong Product role privileges in {path}",
    )
    provenance = postgres.get("catalogProvenance", {})
    require(
        provenance.get("rowCount") == 4 and provenance.get("codes") == ["CAR", "FAI", "HSI", "TRI"],
        f"wrong Product catalog provenance in {path}",
    )
    ownership = document.get("ownership", {})
    require(
        bool(ownership.get("secretReferences")) and len(ownership.get("writers", [])) == 3,
        f"incomplete Product ownership record in {path}",
    )
    sanitization = document.get("sanitizationRecord")
    require(isinstance(sanitization, dict), f"missing sanitization record in {path}")
    require(
        sanitization.get("secretValuesCaptured") is False,
        f"inventory retained secret values in {path}",
    )
    require(
        sanitization.get("bearerTokensCaptured") is False,
        f"inventory retained bearer tokens in {path}",
    )
    require(
        sanitization.get("catalogDefinitionsCaptured") is False,
        f"inventory retained catalog definitions in {path}",
    )
    for workload_record in workloads.values():
        for variable in workload_record.get("container", {}).get("environment", []):
            if SENSITIVE_NAME.search(str(variable.get("name", ""))) and "value" in variable:
                require(
                    variable["value"] == "<redacted>",
                    f"inventory contains an unredacted sensitive environment value in {path}",
                )
    require(
        not any(BEARER.search(value) for value in strings(document)),
        f"inventory contains bearer token-shaped data in {path}",
    )
    return str(environment)


def validate_catalog(document: dict[str, Any], path: Path) -> str:
    environment = common_captured(document, "stored-catalog", path)
    expected_scenario = "DATA-LOCAL-001" if environment == "local-dev" else "DATA-QA-001"
    require(document.get("scenarioId") == expected_scenario, f"invalid scenario identity in {path}")
    require(
        document.get("credentialCategory") == "database-capture",
        f"invalid credential category in {path}",
    )
    require(
        document.get("connection", {}).get("transactionReadOnly") is True,
        f"catalog capture was not read-only in {path}",
    )
    inventory = document.get("inventoryIdentity", {})
    expected_inventory_path = f"legacy/product-service/src/test/resources/product-read-baseline/inventory/{environment}.json"
    require(
        inventory.get("path") == expected_inventory_path
        and inventory.get("algorithm") == "SHA-256"
        and HEX_64.fullmatch(str(inventory.get("value", ""))) is not None,
        f"invalid inventory identity in {path}",
    )
    require(
        document.get("database")
        == {
            "name": "product",
            "schema": "public",
            "table": "public.product",
            "tableOwner": "product",
        },
        f"wrong database identity in {path}",
    )
    rows = document.get("rows")
    require(
        isinstance(rows, list)
        and [row.get("code") for row in rows] == ["CAR", "FAI", "HSI", "TRI"],
        f"invalid Product rows in {path}",
    )
    identities: list[str] = []
    for row in rows:
        raw = row.get("rawLosslessDefinitionJson")
        checksum = row.get("checksum", {}).get("value")
        require(
            isinstance(raw, str) and isinstance(json.loads(raw), dict),
            f"invalid definition in {path}",
        )
        require(
            hashlib.md5(raw.encode(), usedforsecurity=False).hexdigest() == checksum,
            f"raw JSON checksum mismatch for {environment}/{row.get('code')}",
        )
        identities.append(f"{row['code']}:{checksum}")
    data_identity = document.get("dataIdentity", {})
    schema_identity = document.get("schemaIdentity", {})
    require(
        schema_identity.get("algorithm") == "MD5"
        and HEX_32.fullmatch(str(schema_identity.get("value", ""))) is not None
        and data_identity.get("algorithm") == "MD5"
        and data_identity.get("rowCount") == 4
        and data_identity.get("codes") == ["CAR", "FAI", "HSI", "TRI"],
        f"invalid schema or data identity in {path}",
    )
    expected_identity = hashlib.md5(
        ",".join(identities).encode(), usedforsecurity=False
    ).hexdigest()
    require(data_identity.get("value") == expected_identity, f"catalog checksum mismatch in {path}")
    inventory_path = FIXTURE_ROOT / "inventory" / f"{environment}.json"
    inventory_digest = hashlib.sha256(inventory_path.read_bytes()).hexdigest()
    require(
        document.get("inventoryIdentity", {}).get("value") == inventory_digest,
        f"inventory checksum mismatch in {path}",
    )
    coverage = document.get("coverage", {})
    require(
        sorted(coverage.get("questionTypes", [])) == ["choice", "numeric"]
        and len(coverage.get("products", [])) == 4,
        f"invalid catalog coverage in {path}",
    )
    sanitization = document.get("sanitizationRecord", {})
    require(
        sanitization.get("credentialsCaptured") is False
        and sanitization.get("bearerTokensCaptured") is False,
        f"catalog sanitization record is invalid in {path}",
    )
    require(
        not any(BEARER.search(value) for value in strings(document)),
        f"catalog contains bearer token-shaped data in {path}",
    )
    return environment


def observation_ids(document: dict[str, Any], path: Path) -> set[str]:
    observations = document.get("observations")
    require(isinstance(observations, list), f"observations must be an array in {path}")
    identifiers = [item.get("scenarioId") for item in observations if isinstance(item, dict)]
    require(len(identifiers) == len(set(identifiers)), f"duplicate scenario identity in {path}")
    require(
        all(isinstance(item, str) for item in identifiers), f"missing scenario identity in {path}"
    )
    return set(identifiers)


def validate_http(document: dict[str, Any], path: Path) -> str:
    environment = common_captured(document, "http-success", path)
    expected = {
        f"HTTP-{boundary}-{route}-{code}-001"
        for boundary in ("DIRECT", "GATEWAY")
        for route, codes in (("LIST", ("",)), ("GET", ("CAR", "FAI", "HSI", "TRI")))
        for code in codes
    }
    expected = {item.replace("--", "-") for item in expected}
    require(observation_ids(document, path) == expected, f"invalid HTTP scenario set in {path}")
    for observation in document["observations"]:
        request = observation.get("request", {})
        response = observation.get("response", {})
        require(
            request.get("method") == "GET" and request.get("query") == "",
            f"invalid HTTP request in {path}",
        )
        require(
            response.get("status") == 200
            and response.get("headers", {}).get("contentType") == "application/json",
            f"unexpected HTTP response in {path}",
        )
        raw = response.get("rawBody")
        require(isinstance(raw, str), f"missing raw HTTP body in {path}")
        require(isinstance(json.loads(raw), list | dict), f"invalid raw HTTP body in {path}")
        require(
            hashlib.sha256(raw.encode()).hexdigest() == response.get("rawBodySha256"),
            f"raw HTTP body checksum mismatch in {path}",
        )
        expected_credential = "none" if observation.get("boundary") == "direct" else "gateway-valid"
        require(
            request.get("credentialCategory") == expected_credential,
            f"invalid HTTP credential category in {path}",
        )
        if observation.get("boundary") == "gateway":
            authorization = request.get("headers", {}).get(
                "authorization", request.get("authorization")
            )
            require(
                authorization == "Bearer <redacted>",
                f"gateway authorization was not redacted in {path}",
            )
    validate_linked_identities(document, environment, path)
    require(
        document.get("sanitizationRecord", {}).get("bearerTokensCaptured") is False,
        f"HTTP fixture retained a bearer token in {path}",
    )
    return environment


def validate_access(document: dict[str, Any], path: Path) -> str:
    environment = common_captured(document, "access", path)
    expected = {
        f"ACCESS-{boundary}-{route}-{credential}-001"
        for boundary, credentials in (
            ("DIRECT", ("NONE", "VALID", "MALFORMED", "SIGNATURE")),
            ("GATEWAY", ("MISSING", "VALID", "MALFORMED", "SIGNATURE")),
        )
        for route in ("LIST", "GET")
        for credential in credentials
    }
    require(observation_ids(document, path) == expected, f"invalid access scenario set in {path}")
    for observation in document["observations"]:
        response = observation.get("response", {})
        boundary = observation.get("boundary")
        credential = observation.get("request", {}).get("credentialCategory")
        expected_status = 200 if boundary == "direct" or credential == "valid" else 401
        require(response.get("status") == expected_status, f"unexpected access status in {path}")
        raw = response.get("rawBody")
        digest = response.get("rawBodySha256")
        require(
            isinstance(raw, str) and HEX_64.fullmatch(str(digest)) is not None,
            f"invalid access body in {path}",
        )
        require(
            hashlib.sha256(raw.encode()).hexdigest() == digest,
            f"raw access body checksum mismatch in {path}",
        )
        require(
            (response.get("status") == 200 and isinstance(json.loads(raw), list | dict))
            or (response.get("status") == 401 and raw == ""),
            f"invalid access response body in {path}",
        )
    validate_linked_identities(document, environment, path)
    require(
        document.get("sanitizationRecord", {}).get("bearerTokensCaptured") is False,
        f"access fixture retained a bearer token in {path}",
    )
    return environment


SENSITIVE_NAME = re.compile(r"password|secret|token|credential|private|api[_-]?key", re.IGNORECASE)


def validate_linked_identities(document: dict[str, Any], environment: str, path: Path) -> None:
    for part in ("inventory", "catalog"):
        identity = document.get("identities", {}).get(part, {})
        linked = REPOSITORY_ROOT / str(identity.get("path", ""))
        require(linked.is_file(), f"missing linked {part} fixture for {environment}")
        require(
            identity.get("algorithm") == "SHA-256"
            and hashlib.sha256(linked.read_bytes()).hexdigest() == identity.get("value"),
            f"linked {part} checksum mismatch for {environment}",
        )
    catalog = load_document(FIXTURE_ROOT / "catalog" / f"{environment}.json")
    require(
        document.get("identities", {}).get("schema") == catalog.get("schemaIdentity")
        and document.get("identities", {}).get("data") == catalog.get("dataIdentity"),
        f"fixture identity does not match accepted {environment} catalog in {path}",
    )


def validate_access_cases(path: Path) -> None:
    document = load_document(path)
    cases = document.get("cases", [])
    require(
        document.get("schemaVersion") == 1
        and document.get("fixtureId") == "JAVA-ACCESS-001"
        and document.get("provenance") == "synthetic-isolated"
        and len(cases) == 38,
        f"invalid isolated access fixture in {path}",
    )
    require(
        sum(case.get("target") == "gateway" for case in cases) == 20
        and sum(case.get("target") == "product" for case in cases) == 18,
        f"invalid isolated access target coverage in {path}",
    )
    rejected = {"missing", "malformed", "invalid-signature", "expired", "missing-subject"}
    accepted = {"valid", "not-before", "issuer-variation", "audience-variation", "role-variation"}
    for case in cases:
        expected = case.get("expected", {})
        if case.get("target") == "product":
            require(expected.get("status") == 200, f"invalid Product access case in {path}")
        else:
            credential = case.get("credential")
            require(
                (credential in rejected and expected.get("status") == 401)
                or (credential in accepted and expected.get("status") == 200),
                f"invalid gateway access case in {path}",
            )
            require(
                expected.get("downstreamAuthorizationPresent") is False
                and expected.get("downstreamIdentityHeaderNames") == [],
                f"gateway access fixture forwards credentials in {path}",
            )
    require(
        document.get("sanitization", {}).get("tokensRetained") is False
        and document.get("sanitization", {}).get("signingMaterialRetained") is False,
        f"invalid isolated access sanitization in {path}",
    )


def validate_case_fixture(document: dict[str, Any], part: str, path: Path) -> str:
    expected = {
        "data-edges": ("DATA-EDGES-001", 16),
        "failures": ("HTTP-FAILURES-001", 30),
        "db-permissions": ("DB-PERMISSIONS-001", 10),
    }
    fixture_id, count = expected[part]
    require(document.get("schemaVersion") == 1, f"invalid schemaVersion in {path}")
    require(document.get("fixtureId") == fixture_id, f"invalid fixture identity in {path}")
    require(document.get("provenance") == "synthetic-isolated", f"invalid provenance in {path}")
    cases = document.get("cases")
    require(isinstance(cases, list) and len(cases) == count, f"invalid case count in {path}")
    identifiers = [case.get("id") for case in cases if isinstance(case, dict)]
    require(
        len(identifiers) == len(set(identifiers)) == count
        and set(identifiers) == EXPECTED_CASE_IDS[part],
        f"missing, unexpected, or duplicate cases in {path}",
    )
    scenario_ids = {
        scenario
        for case in cases
        for scenario in case.get("scenarioIds", [])
        if isinstance(scenario, str)
    }
    if part == "data-edges":
        require(len(scenario_ids) == 25, f"invalid data-edge scenario coverage in {path}")
        for case in cases:
            expected_case = case.get("expected", {})
            kind = case.get("kind")
            outcome = expected_case.get("outcome")
            require(
                kind in {"http", "database-constraint"}
                and outcome in {"http-success", "http-failure", "constraint-rejected"}
                and bool(case.get("rows"))
                and case.get("repetitions", 1) >= 1,
                f"invalid data-edge case in {path}",
            )
            if kind == "http":
                require(
                    str(case.get("requestPath", "")).startswith("/products")
                    and expected_case.get("contentType") == "application/json",
                    f"invalid data-edge HTTP case in {path}",
                )
            if outcome == "http-success":
                require(
                    expected_case.get("status") == 200
                    and isinstance(json.loads(expected_case.get("rawBody", "")), list | dict),
                    f"invalid successful data-edge case in {path}",
                )
            elif outcome == "http-failure":
                require(
                    expected_case.get("status") == 500
                    and isinstance(json.loads(expected_case.get("rawBody", "")), dict),
                    f"invalid failing data-edge case in {path}",
                )
            else:
                require(
                    expected_case.get("sqlState") == "23502",
                    f"invalid data-edge constraint case in {path}",
                )
        repeated = {
            case.get("id"): case.get("repetitions")
            for case in cases
            if case.get("id") in {"DATA-EDGES-RICH-001", "DATA-EDGES-PRODUCT-ORDER-001"}
        }
        require(
            repeated == {"DATA-EDGES-RICH-001": 3, "DATA-EDGES-PRODUCT-ORDER-001": 3},
            f"invalid repeated data-edge coverage in {path}",
        )
        rich = next(case for case in cases if case.get("id") == "DATA-EDGES-RICH-001")
        body = rich.get("expected", {}).get("rawBody", "")
        require(
            all(
                value in body
                for value in (
                    '"sumInsured":12.3400',
                    '"sumInsured":12345678901234567890.123456789',
                    '"sumInsured":0.00',
                )
            ),
            f"data-edge precision contract is missing in {path}",
        )
    elif part == "failures":
        require(
            len(scenario_ids) == 21
            and sum(case.get("target") == "product" for case in cases) == 13
            and sum(case.get("target") == "gateway" for case in cases) == 17,
            f"invalid failure target coverage in {path}",
        )
        for case in cases:
            expected_case = case.get("expected", {})
            require(
                case.get("target") in {"product", "gateway"}
                and str(case.get("rawPath", "")).startswith(("/products", "/api/products"))
                and expected_case.get("contentType") == "application/json"
                and expected_case.get("status") in {200, 400, 404, 500}
                and isinstance(json.loads(expected_case.get("rawBody", "")), list | dict),
                f"invalid Product failure case in {path}",
            )
            if case.get("target") == "gateway":
                require(
                    expected_case.get("downstreamAttempts") in {0, 1, 3}
                    and expected_case.get("fallbackInvocations") in {0, 1},
                    f"invalid gateway failure retry contract in {path}",
                )
                if expected_case.get("fallbackInvocations") == 1:
                    require(
                        expected_case.get("downstreamAttempts") == 3,
                        f"invalid gateway fallback contract in {path}",
                    )
            if case.get("backendUnavailable") is True:
                require(
                    expected_case.get("downstreamAttempts") == 3
                    and expected_case.get("downstreamRawPaths") == [],
                    f"invalid unavailable-backend contract in {path}",
                )
        require(
            sum(
                case.get("expected", {}).get("fallbackInvocations") == 1
                for case in cases
                if case.get("target") == "gateway"
            )
            == 8,
            f"invalid gateway fallback coverage in {path}",
        )
        bad_encoding = next(
            case for case in cases if case.get("id") == "FAIL-GATEWAY-LOOKUP-BAD-ENCODING-001"
        )
        require(
            bad_encoding.get("expected", {}).get("downstreamAttempts") == 0,
            f"invalid bad-encoding boundary in {path}",
        )
        for identifier in ("FAIL-PRODUCT-DECODE-TYPE-001", "FAIL-PRODUCT-DECODE-STRUCTURE-001"):
            case = next(item for item in cases if item.get("id") == identifier)
            require(
                "org.hibernate.HibernateException" in case.get("expected", {}).get("rawBody", ""),
                f"missing decode failure identity in {path}",
            )
    if part == "db-permissions":
        require(
            sum(case.get("operationClass") == "read" for case in cases) == 3
            and sum(case.get("operationClass") == "dml" for case in cases) == 4
            and sum(case.get("operationClass") == "ddl" for case in cases) == 3,
            f"invalid database-permission operation coverage in {path}",
        )
        observations = document.get("expectedObservations", [])
        require(
            {item.get("id") for item in observations} == {"DB-ROLE-BASELINE-001", *identifiers},
            f"invalid database-permission observation set in {path}",
        )
        denied = [item for item in observations if item.get("status") == "denied"]
        require(
            len(denied) == 7
            and sum(item.get("status") == "succeeded" for item in observations) == 3
            and all(item.get("sqlState") == "42501" for item in denied),
            f"invalid denial contract in {path}",
        )
        role = document.get("roleSpecification", {})
        require(
            role.get("attributes")
            == {
                "login": True,
                "superuser": False,
                "createDatabase": False,
                "createRole": False,
                "inherit": False,
                "replication": False,
                "bypassRls": False,
            }
            and role.get("databasePrivileges") == ["CONNECT"]
            and role.get("schemaPrivileges") == ["USAGE"]
            and role.get("tablePrivileges") == ["SELECT"]
            and role.get("memberOfRoles") == [],
            f"reader role has unexpected table grants in {path}",
        )
        baseline = next(item for item in observations if item.get("id") == "DB-ROLE-BASELINE-001")
        require(
            all(
                baseline.get(name) == value
                for name, value in {
                    "ownsDatabase": False,
                    "ownsSchema": False,
                    "ownsTable": False,
                    "memberOfRoleCount": 0,
                    "publicTableGrantCount": 0,
                    "publicDefaultTableGrantCount": 0,
                    "candidateDefaultTableGrantCount": 0,
                    "sequenceCount": 0,
                }.items()
            ),
            f"reader role baseline is unsafe in {path}",
        )
        safety = document.get("safety", {})
        require(
            safety.get("setupIdentityDistinctFromCandidate") is True
            and safety.get("sharedEnvironmentMutation") is False
            and safety.get("dataAndSchemaIdentityRequiredUnchanged") is True
            and safety.get("credentialRetained") is False,
            f"permission fixture permits shared mutation in {path}",
        )
        credential_values = [
            value for value in string_values(document) if SENSITIVE_NAME.search(value)
        ]
        require(
            all(
                value == "runtime-generated by isolated admin fixture; no value retained"
                for value in credential_values
            ),
            f"permission fixture contains credential-shaped metadata in {path}",
        )
    require(
        not any(BEARER.search(value) for value in strings(document)),
        f"fixture contains bearer token-shaped data in {path}",
    )
    return "isolated"


VALIDATORS = {
    "inventory": validate_inventory,
    "catalog": validate_catalog,
    "http": validate_http,
    "access": validate_access,
}


def validate_part(
    part: str, environment: str | None = None, explicit_file: str | None = None
) -> list[str]:
    if part == "access":
        validate_access_cases(FIXTURE_ROOT / "access/cases.json")
    results = []
    for path in fixture_files(part, environment, explicit_file):
        require(path.is_file(), f"missing {part} fixture: {path}")
        document = load_document(path)
        if part in VALIDATORS:
            results.append(VALIDATORS[part](document, path))
        else:
            results.append(validate_case_fixture(document, part, path))
    validate_manifest(part, results, explicit_file is not None)
    return results


def validate_manifest(part: str, environments: list[str], explicit: bool) -> None:
    manifest = load_document(MANIFEST)
    scenarios = manifest.get("scenarios", [])
    require(isinstance(scenarios, list), "baseline manifest scenarios must be an array")
    if part == "inventory":
        for environment in environments:
            identifier = "INV-LOCAL-001" if environment == "local-dev" else "INV-QA-001"
            matches = [
                item
                for item in scenarios
                if item.get("id") == identifier
                and item.get("group") == "inventory"
                and item.get("captureProfile") == "inventory"
                and item.get("provenance") == "captured-observation"
            ]
            require(len(matches) == 1, f"manifest does not register {identifier}")
    elif part == "catalog":
        for environment in environments:
            identifier = "DATA-LOCAL-001" if environment == "local-dev" else "DATA-QA-001"
            expected_fixture = f"catalog/{environment}.json"
            matches = [
                item
                for item in scenarios
                if item.get("id") == identifier
                and item.get("group") == "representative-data"
                and item.get("captureProfile") == "stored-catalog"
                and item.get("provenance") == "captured-representative"
                and item.get("status") == "captured"
                and item.get("fixture") == expected_fixture
            ]
            require(len(matches) == 1, f"manifest does not register {identifier}")
    elif part == "http":
        expected = {
            f"HTTP-{boundary}-{route}-{code}-001".replace("--", "-")
            for boundary in ("DIRECT", "GATEWAY")
            for route, codes in (("LIST", ("",)), ("GET", ("CAR", "FAI", "HSI", "TRI")))
            for code in codes
        }
        for environment in environments:
            found = {
                item.get("id")
                for item in scenarios
                if item.get("id") in expected
                and item.get("status") == "captured"
                and item.get("fixtures", {}).get(environment) == f"http/{environment}.json"
            }
            require(found == expected, f"manifest HTTP coverage is incomplete for {environment}")
    elif part == "access" and not explicit:
        matches = [
            item
            for item in scenarios
            if item.get("group") == "access"
            and item.get("status") == "captured"
            and item.get("fixture") == "access/cases.json"
            and item.get("fixtures", {}).get("local-dev") == "access/local-dev.json"
            and item.get("fixtures", {}).get("qa") == "access/qa.json"
        ]
        require(len(matches) == 5, "manifest access coverage is incomplete")
    elif part in {"data-edges", "failures"}:
        groups = {
            "data-edges": (
                "data-semantics",
                {
                    "DATA-DECIMAL-001",
                    "DATA-QUESTION-VARIANTS-001",
                    "DATA-PRESENCE-DEFAULTS-001",
                    "DATA-UNKNOWN-FIELDS-001",
                    "DATA-UNKNOWN-SUBTYPE-001",
                    "DATA-ORDERING-001",
                },
            ),
            "failures": (
                "http-failure-and-boundary",
                {
                    "HTTP-EMPTY-CATALOG-001",
                    "HTTP-SINGLE-CATALOG-001",
                    "HTTP-MISSING-001",
                    "HTTP-LOOKUP-PATHS-001",
                    "FAIL-DECODE-001",
                    "FAIL-DATABASE-001",
                    "FAIL-GATEWAY-BACKEND-001",
                },
            ),
        }
        group, identifiers = groups[part]
        expected_variants = {
            variant.get("id")
            for item in scenarios
            if item.get("id") in identifiers
            and item.get("group") == group
            and item.get("status") == "captured"
            and item.get("fixture") == f"{part}/cases.json"
            for variant in item.get("variants", [])
        }
        fixture = load_document(FIXTURE_ROOT / part / "cases.json")
        actual_variants = {
            identifier
            for case in fixture.get("cases", [])
            for identifier in case.get("scenarioIds", [])
        }
        require(expected_variants == actual_variants, f"manifest {part} coverage is incomplete")
    elif part == "db-permissions":
        identifiers = {"DB-READ-001", "DB-DML-DENY-001", "DB-DDL-DENY-001"}
        found = {
            item.get("id")
            for item in scenarios
            if item.get("id") in identifiers
            and item.get("status") == "captured"
            and item.get("fixture") == "db-permissions/cases.json"
        }
        require(found == identifiers, "manifest database-permission coverage is incomplete")


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("part", choices=(*PARTS, "all"))
    args = parser.parse_args(argv)
    try:
        parts = PARTS if args.part == "all" else (args.part,)
        for part in parts:
            explicit = os.environ.get(f"BASELINE_{part.replace('-', '_').upper()}_FILE")
            for label in validate_part(part, os.environ.get("BASELINE_ENV"), explicit):
                print(f"Validated {label} Product {part} fixture.")
    except (FixtureError, OSError, json.JSONDecodeError, TypeError, KeyError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
