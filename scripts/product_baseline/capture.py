#!/usr/bin/env python3
"""Capture reviewed Product catalog, HTTP, and access evidence without retaining secrets."""

from __future__ import annotations

import argparse
import contextlib
import hashlib
import json
import os
import sys
import tempfile
from collections.abc import Iterator, Mapping, Sequence
from datetime import UTC, datetime
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from scripts.product_baseline import fixtures, preflight

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
RUN_ROOT = REPOSITORY_ROOT / "ai/artifacts/epic-4.2/issue-131"
CONTEXTS = {"local-dev": "kind-local-dev-insurance-hub", "qa": "qa-insurance-hub"}


class CaptureError(RuntimeError):
    """Product baseline capture could not preserve its read-only contract."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise CaptureError(message)


@contextlib.contextmanager
def environment(values: Mapping[str, str | None]) -> Iterator[None]:
    previous = {name: os.environ.get(name) for name in values}
    try:
        for name, value in values.items():
            if value is None:
                os.environ.pop(name, None)
            else:
                os.environ[name] = value
        yield
    finally:
        for name, value in previous.items():
            if value is None:
                os.environ.pop(name, None)
            else:
                os.environ[name] = value


def run_preflight(output: Path, *, catalog: bool, port: str | None = None) -> dict[str, object]:
    variables = {
        "BASELINE_PREFLIGHT_CATALOG_OUTPUT": str(output) if catalog else None,
        "BASELINE_PREFLIGHT_INVENTORY_OUTPUT": None if catalog else str(output),
    }
    if port is not None:
        variables["BASELINE_PRODUCT_DB_PORT"] = port
    with (
        environment(variables),
        open(os.devnull, "w", encoding="utf-8") as sink,
        contextlib.redirect_stdout(sink),
    ):
        require(preflight.main([]) == 0, "Product baseline preflight failed")
    return fixtures.load_document(output)


def identity_file(part: str, selected_environment: str) -> tuple[str, dict[str, object]]:
    relative = f"legacy/product-service/src/test/resources/product-read-baseline/{part}/{selected_environment}.json"
    path = REPOSITORY_ROOT / relative
    require(path.is_file(), f"Missing accepted {selected_environment} {part} fixture.")
    return relative, fixtures.load_document(path)


def digest(relative: str) -> str:
    return hashlib.sha256((REPOSITORY_ROOT / relative).read_bytes()).hexdigest()


def source_revision() -> str:
    return preflight.command(["git", "rev-parse", "HEAD"])


def output_path(part: str, selected_environment: str, captured_at: str) -> Path:
    try:
        stamp = datetime.fromisoformat(captured_at.replace("Z", "+00:00")).strftime("%Y%m%d-%H%M%S")
    except ValueError as error:
        raise CaptureError("capture timestamp is malformed") from error
    path = RUN_ROOT / f"{part}-runs" / f"{selected_environment}-{stamp}.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    require(
        not path.exists(), f"{part.title()} run already exists at {path}; no capture was replaced."
    )
    return path


def store(path: Path, document: dict[str, object]) -> None:
    descriptor, name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(document, output, indent=2, ensure_ascii=False)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, 0o644)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def capture_catalog(selected_environment: str, temporary: Path) -> Path:
    database = run_preflight(temporary / "database.json", catalog=True)
    require(
        database.get("transactionIsolation") == "repeatable read"
        and database.get("transactionReadOnly") is True
        and database.get("database") == "product"
        and database.get("schema") == "public"
        and database.get("table") == "public.product"
        and database.get("dataIdentity", {}).get("codes") == ["CAR", "FAI", "HSI", "TRI"],
        "Catalog capture did not contain exactly CAR, FAI, HSI, and TRI.",
    )
    inventory_path, _ = identity_file("inventory", selected_environment)
    captured_at = str(database["observedAt"])
    endpoint = f"{os.environ.get('BASELINE_PRODUCT_DB_HOST', '127.0.0.1')}:{os.environ.get('BASELINE_PRODUCT_DB_PORT', '5492')}"
    document = {
        "schemaVersion": 1,
        "scenarioId": "DATA-LOCAL-001" if selected_environment == "local-dev" else "DATA-QA-001",
        "status": "captured",
        "captureProfile": "stored-catalog",
        "provenance": "captured-representative",
        "credentialCategory": "database-capture",
        "capturedAt": captured_at,
        "captureClock": "Target PostgreSQL statement clock inside a repeatable-read, read-only transaction",
        "captureCommand": f"make product-baseline-capture BASELINE_ENV={selected_environment} BASELINE_PART=catalog",
        "environment": selected_environment,
        "sourceRevision": source_revision(),
        "kubernetesContext": CONTEXTS[selected_environment],
        "connection": {"endpoint": endpoint, "transactionReadOnly": True},
        "inventoryIdentity": {
            "path": inventory_path,
            "algorithm": "SHA-256",
            "value": digest(inventory_path),
        },
        "database": {
            "name": database["database"],
            "schema": database["schema"],
            "table": database["table"],
            "tableOwner": database["tableOwner"],
        },
        "schemaIdentity": database["schemaIdentity"],
        "dataIdentity": database["dataIdentity"],
        "transactionSnapshot": database["transactionSnapshot"],
        "rows": database["rows"],
        "coverage": database["coverage"],
        "sanitizationRecord": {
            "sourceContainsOnlyAgreedDemoCatalog": True,
            "credentialsCaptured": False,
            "bearerTokensCaptured": False,
            "transformation": "PostgreSQL definition::text retained as a JSON string; no typed or floating-point conversion",
        },
    }
    destination = output_path("catalog", selected_environment, captured_at)
    store(destination, document)
    fixtures.validate_part("catalog", explicit_file=str(destination))
    return destination


def request_json(base_url: str, path: str, token: str | None) -> tuple[int, str, str, int]:
    headers = {"Accept": "application/json"}
    if token is not None:
        headers["Authorization"] = f"Bearer {token}"
    request = Request(base_url + path, headers=headers, method="GET")
    try:
        with urlopen(request, timeout=15) as response:
            status = response.status
            body_bytes = response.read()
            content_type = response.headers.get("Content-Type", "")
            content_length = int(response.headers.get("Content-Length", len(body_bytes)))
    except HTTPError as error:
        status = error.code
        body_bytes = error.read()
        content_type = error.headers.get("Content-Type", "")
        content_length = int(error.headers.get("Content-Length", len(body_bytes)))
    except (OSError, URLError, TimeoutError) as error:
        raise CaptureError(f"HTTP capture failed for {path}: {error}") from error
    try:
        body = body_bytes.decode("utf-8")
    except UnicodeDecodeError as error:
        raise CaptureError(f"HTTP response is not UTF-8 for {path}") from error
    return status, body, content_type, content_length


def observation(
    boundary: str,
    scenario_id: str,
    path: str,
    token: str | None,
    credential: str,
    *,
    require_success: bool,
) -> dict[str, object]:
    base = (
        os.environ.get("BASELINE_PRODUCT_URL", "http://127.0.0.1:19081")
        if boundary == "direct"
        else os.environ.get("BASELINE_GATEWAY_URL", "http://127.0.0.1:19082")
    )
    status, body, content_type, content_length = request_json(base, path, token)
    if require_success:
        require(status == 200, f"{scenario_id} returned HTTP {status}.")
    if body:
        try:
            json.loads(body)
        except json.JSONDecodeError as error:
            raise CaptureError(f"{scenario_id} did not return JSON.") from error
    request_record: dict[str, object] = {
        "method": "GET",
        "path": path,
        "query": "",
        "credentialCategory": credential,
    }
    authorization = None if token is None else "Bearer <redacted>"
    if scenario_id.startswith("HTTP-"):
        request_record["headers"] = {
            "accept": "application/json",
            "authorization": authorization,
        }
    else:
        request_record["authorization"] = authorization
    return {
        "scenarioId": scenario_id,
        "boundary": boundary,
        "request": request_record,
        "response": {
            "status": status,
            "headers": {"contentType": content_type, "contentLength": content_length},
            "rawBody": body,
            "rawBodySha256": hashlib.sha256(body.encode()).hexdigest(),
        },
    }


def token_file() -> str:
    path = Path(os.environ.get("BASELINE_GATEWAY_TOKEN_FILE", ""))
    require(
        path.is_file() and not path.is_symlink(),
        "BASELINE_GATEWAY_TOKEN_FILE must name a regular short-lived token file.",
    )
    metadata = path.stat()
    require(
        metadata.st_uid == os.getuid() and metadata.st_mode & 0o077 == 0,
        "BASELINE_GATEWAY_TOKEN_FILE must be owned by the current user and inaccessible to group and other users.",
    )
    token = path.read_text(encoding="utf-8").replace("\r", "").replace("\n", "")
    require(bool(token), "The gateway token file is empty.")
    return token


def identities(
    selected_environment: str,
) -> tuple[dict[str, object], dict[str, object], dict[str, object]]:
    inventory_path, inventory = identity_file("inventory", selected_environment)
    catalog_path, catalog = identity_file("catalog", selected_environment)
    return (
        inventory,
        catalog,
        {
            "inventory": {
                "path": inventory_path,
                "algorithm": "SHA-256",
                "value": digest(inventory_path),
            },
            "catalog": {
                "path": catalog_path,
                "algorithm": "SHA-256",
                "value": digest(catalog_path),
            },
            "schema": catalog["schemaIdentity"],
            "data": catalog["dataIdentity"],
        },
    )


def preflight_pair(temporary: Path, catalog: dict[str, object]) -> tuple[dict[str, object], str]:
    before = run_preflight(temporary / "before.json", catalog=True)
    after_port = os.environ.get(
        "BASELINE_PRODUCT_DB_AFTER_PORT", os.environ.get("BASELINE_PRODUCT_DB_PORT", "5492")
    )
    return before, after_port


def validate_after(
    temporary: Path, before: dict[str, object], accepted: dict[str, object], after_port: str
) -> dict[str, object]:
    after = run_preflight(temporary / "after.json", catalog=True, port=after_port)
    require(
        before.get("schemaIdentity") == after.get("schemaIdentity")
        and before.get("dataIdentity") == after.get("dataIdentity"),
        "Schema or catalog identity changed during capture.",
    )
    require(
        after.get("schemaIdentity") == accepted.get("schemaIdentity")
        and after.get("dataIdentity") == accepted.get("dataIdentity"),
        "Live catalog identity does not match the accepted fixture.",
    )
    return after


def capture_http(selected_environment: str, temporary: Path) -> Path:
    inventory, catalog, identity = identities(selected_environment)
    token = token_file()
    before, after_port = preflight_pair(temporary, catalog)
    records = [
        observation(
            "direct", "HTTP-DIRECT-LIST-001", "/products", None, "none", require_success=True
        ),
        observation(
            "gateway",
            "HTTP-GATEWAY-LIST-001",
            "/api/products",
            token,
            "gateway-valid",
            require_success=True,
        ),
    ]
    for code in ("CAR", "FAI", "HSI", "TRI"):
        records.append(
            observation(
                "direct",
                f"HTTP-DIRECT-GET-{code}-001",
                f"/products/{code}",
                None,
                "none",
                require_success=True,
            )
        )
        records.append(
            observation(
                "gateway",
                f"HTTP-GATEWAY-GET-{code}-001",
                f"/api/products/{code}",
                token,
                "gateway-valid",
                require_success=True,
            )
        )
    del token
    after = validate_after(temporary, before, catalog, after_port)
    captured_at = str(after["observedAt"])
    document = {
        "schemaVersion": 1,
        "status": "captured",
        "captureProfile": "http-success",
        "provenance": "captured-representative",
        "environment": selected_environment,
        "capturedAt": captured_at,
        "captureCommand": f"make product-baseline-capture BASELINE_ENV={selected_environment} BASELINE_PART=http BASELINE_GATEWAY_TOKEN_FILE=<temporary-token-file>",
        "sourceRevision": source_revision(),
        "kubernetesContext": CONTEXTS[selected_environment],
        "identities": identity,
        "serviceRevisions": {
            "product": {
                key: inventory["kubernetes"]["workloads"]["product"][key]
                for key in ("deployment", "deploymentRevision")
            },
            "gateway": {
                key: inventory["kubernetes"]["workloads"]["gateway"][key]
                for key in ("deployment", "deploymentRevision")
            },
        },
        "observations": records,
        "sanitizationRecord": {
            "bearerTokensCaptured": False,
            "credentialsCaptured": False,
            "requestAuthorizationReplacement": "Bearer <redacted>",
            "responseBodies": "Raw JSON response bytes stored as JSON strings without DTO deserialization",
            "omittedVolatileHeaders": ["Date", "connection"],
        },
    }
    for name, workload in document["serviceRevisions"].items():
        source = inventory["kubernetes"]["workloads"][name]
        workload.update(
            image=source["container"]["image"], imageDigest=source["pods"][0]["imageDigest"]
        )
    destination = output_path("http", selected_environment, captured_at)
    store(destination, document)
    fixtures.validate_part("http", explicit_file=str(destination))
    return destination


def capture_access(selected_environment: str, temporary: Path) -> Path:
    inventory, catalog, identity = identities(selected_environment)
    valid = token_file()
    pieces = valid.split(".")
    require(
        len(pieces) == 3 and all(pieces),
        "The gateway token file does not contain a compact signed JWT.",
    )
    before, after_port = preflight_pair(temporary, catalog)
    observed = datetime.fromisoformat(str(before["observedAt"]).replace("Z", "+00:00"))
    require(
        abs((datetime.now(UTC) - observed).total_seconds()) <= 300,
        "Environment clock differs from the capture host by more than five minutes.",
    )
    signature = ("B" if pieces[2].startswith("A") else "A") + pieces[2][1:]
    tokens = {
        "valid": valid,
        "missing": None,
        "malformed": "not-a-jwt",
        "invalid-signature": f"{pieces[0]}.{pieces[1]}.{signature}",
    }
    records = []
    for boundary in ("direct", "gateway"):
        for route in ("list", "get"):
            path = (
                ("/products" if route == "list" else "/products/TRI")
                if boundary == "direct"
                else ("/api/products" if route == "list" else "/api/products/TRI")
            )
            for credential, supplied in tokens.items():
                scenario_credential = (
                    "NONE"
                    if boundary == "direct" and credential == "missing"
                    else credential.upper().replace("INVALID-SIGNATURE", "SIGNATURE")
                )
                records.append(
                    observation(
                        boundary,
                        f"ACCESS-{boundary.upper()}-{route.upper()}-{scenario_credential}-001",
                        path,
                        supplied,
                        credential,
                        require_success=False,
                    )
                )
    tokens.clear()
    del valid, pieces, signature
    after = validate_after(temporary, before, catalog, after_port)
    captured_at = str(after["observedAt"])
    document = {
        "schemaVersion": 1,
        "status": "captured",
        "captureProfile": "access",
        "provenance": "captured-representative",
        "environment": selected_environment,
        "capturedAt": captured_at,
        "captureCommand": f"make product-baseline-capture BASELINE_ENV={selected_environment} BASELINE_PART=access BASELINE_GATEWAY_TOKEN_FILE=<temporary-token-file>",
        "sourceRevision": source_revision(),
        "kubernetesContext": CONTEXTS[selected_environment],
        "identities": identity,
        "serviceRevisions": {
            "product": inventory["kubernetes"]["workloads"]["product"],
            "gateway": inventory["kubernetes"]["workloads"]["gateway"],
        },
        "observations": records,
        "liveCoverage": ["valid", "missing", "malformed", "invalid-signature"],
        "isolatedCoverage": [
            "expired",
            "not-before",
            "issuer",
            "audience",
            "role",
            "identity-propagation",
        ],
        "sanitizationRecord": {
            "bearerTokensCaptured": False,
            "signingMaterialCaptured": False,
            "credentialsCaptured": False,
        },
    }
    destination = output_path("access", selected_environment, captured_at)
    store(destination, document)
    fixtures.validate_part("access", explicit_file=str(destination))
    return destination


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("part", choices=("catalog", "http", "access"))
    arguments = parser.parse_args(argv)
    selected_environment = os.environ.get("BASELINE_ENV", "")
    try:
        require(
            os.environ.get("BASELINE_PART") == arguments.part,
            f"BASELINE_PART must be '{arguments.part}'.",
        )
        require(
            selected_environment in CONTEXTS,
            f"Unsupported BASELINE_ENV '{selected_environment}'; use local-dev or qa.",
        )
        with tempfile.TemporaryDirectory(prefix=f"product-baseline-{arguments.part}-") as directory:
            temporary = Path(directory)
            if arguments.part == "catalog":
                destination = capture_catalog(selected_environment, temporary)
            elif arguments.part == "http":
                destination = capture_http(selected_environment, temporary)
            else:
                destination = capture_access(selected_environment, temporary)
    except (
        CaptureError,
        fixtures.FixtureError,
        preflight.PreflightError,
        OSError,
        KeyError,
        TypeError,
        json.JSONDecodeError,
    ) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    print(
        f"Captured read-only {selected_environment} Product {arguments.part} run at {destination.relative_to(REPOSITORY_ROOT)}."
    )
    print(
        f"Review it, then accept it with: scripts/product-baseline/accept-{arguments.part}.sh {destination.relative_to(REPOSITORY_ROOT)}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
