#!/usr/bin/env python3

"""Exercise controlled failures against the Product deployment checker."""

from __future__ import annotations

import argparse
import copy
import json
import subprocess
import sys
import tempfile
from collections.abc import Callable
from pathlib import Path
from typing import Any


def load(path: Path) -> list[dict[str, Any]]:
    return json.loads(path.read_text(encoding="utf-8"))


def find(documents: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    for document in documents:
        if document.get("kind") == kind and document.get("metadata", {}).get("name") == name:
            return document
    raise AssertionError(f"fixture does not contain {kind}/{name}")


def find_prefix(documents: list[dict[str, Any]], kind: str, prefix: str) -> dict[str, Any]:
    matches = [
        document
        for document in documents
        if document.get("kind") == kind
        and document.get("metadata", {}).get("name", "").startswith(prefix)
    ]
    if len(matches) != 1:
        raise AssertionError(f"fixture does not contain exactly one {kind} with prefix {prefix}")
    return matches[0]


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--checker", required=True, type=Path)
    parser.add_argument("--local-go", required=True, type=Path)
    parser.add_argument("--local-legacy", required=True, type=Path)
    parser.add_argument("--qa-go", required=True, type=Path)
    parser.add_argument("--qa-legacy", required=True, type=Path)
    parser.add_argument("--qa-aggregate", required=True, type=Path)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    arguments = parse_args(argv)
    baseline = {
        "local-go": load(arguments.local_go),
        "local-legacy": load(arguments.local_legacy),
        "qa-go": load(arguments.qa_go),
        "qa-legacy": load(arguments.qa_legacy),
        "qa-aggregate": load(arguments.qa_aggregate),
    }

    active = copy.deepcopy(baseline)
    java = find(active["qa-aggregate"], "Deployment", "qa-product-api-legacy")
    java.get("metadata", {}).get("labels", {}).pop("insurance-hub.io/product-go-state", None)
    aggregate_identities = {
        (document.get("kind"), document.get("metadata", {}).get("name"))
        for document in active["qa-aggregate"]
    }
    active["qa-aggregate"].extend(
        copy.deepcopy(document)
        for document in active["qa-go"]
        if (document.get("kind"), document.get("metadata", {}).get("name"))
        not in aggregate_identities
    )

    semantic = copy.deepcopy(active)
    for key in ("qa-go", "qa-aggregate"):
        deployment = find(semantic[key], "Deployment", "qa-product-api")
        deployment["spec"]["template"]["spec"]["containers"][0]["image"] = (
            "ghcr.io/igor-baiborodine/insurance-hub/product-api:1.2.3"
        )

    def bad_selector(values: dict[str, list[dict[str, Any]]]) -> None:
        service = find(values["local-go"], "Service", "local-dev-product-api")
        service["spec"]["selector"]["app.kubernetes.io/name"] = "product-api-legacy"

    def wrong_port(values: dict[str, list[dict[str, Any]]]) -> None:
        deployment = find(values["local-go"], "Deployment", "local-dev-product-api")
        deployment["spec"]["template"]["spec"]["containers"][0]["ports"][0]["containerPort"] = 8082

    def missing_secret(values: dict[str, list[dict[str, Any]]]) -> None:
        deployment = find(values["local-go"], "Deployment", "local-dev-product-api")
        deployment["spec"]["template"]["spec"]["containers"][0]["env"] = []

    def external_exposure(values: dict[str, list[dict[str, Any]]]) -> None:
        service = find(values["local-go"], "Service", "local-dev-product-api")
        service["spec"]["type"] = "LoadBalancer"

    def missing_activation(values: dict[str, list[dict[str, Any]]]) -> None:
        values["qa-aggregate"] = [
            document
            for document in values["qa-aggregate"]
            if document.get("metadata", {}).get("name")
            not in {
                "qa-product-api",
                "qa-product-api-management",
            }
            and not (
                document.get("kind") == "ConfigMap"
                and document.get("metadata", {}).get("name", "").startswith("qa-product-config-")
            )
        ]

    def mark_stopped(values: dict[str, list[dict[str, Any]]]) -> None:
        deployment = find(values["qa-aggregate"], "Deployment", "qa-product-api-legacy")
        deployment.setdefault("metadata", {}).setdefault("labels", {})[
            "insurance-hub.io/product-go-state"
        ] = "stopped"

    def stopped_with_go(values: dict[str, list[dict[str, Any]]]) -> None:
        mark_stopped(values)

    def unreleased_qa_tag(values: dict[str, list[dict[str, Any]]]) -> None:
        deployment = find(values["qa-go"], "Deployment", "qa-product-api")
        deployment["spec"]["template"]["spec"]["containers"][0]["image"] = (
            "ghcr.io/igor-baiborodine/insurance-hub/product-api:sha-unpublished"
        )

    def invalid_qa_tag(values: dict[str, list[dict[str, Any]]]) -> None:
        deployment = find(values["qa-go"], "Deployment", "qa-product-api")
        deployment["spec"]["template"]["spec"]["containers"][0]["image"] = (
            "ghcr.io/igor-baiborodine/insurance-hub/product-api:latest"
        )

    def disabled_qa_telemetry(values: dict[str, list[dict[str, Any]]]) -> None:
        config = find_prefix(values["qa-go"], "ConfigMap", "qa-product-config-")
        config["data"]["OTEL_ENABLED"] = "false"

    def missing_qa_instance(values: dict[str, list[dict[str, Any]]]) -> None:
        deployment = find(values["qa-go"], "Deployment", "qa-product-api")
        deployment["spec"]["template"]["spec"]["containers"][0]["env"] = [
            item
            for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
            if item.get("name") != "SERVICE_INSTANCE_ID"
        ]

    def missing_java_sampler(values: dict[str, list[dict[str, Any]]]) -> None:
        deployment = find(values["qa-legacy"], "Deployment", "qa-product-api-legacy")
        container = deployment["spec"]["template"]["spec"]["containers"][0]
        container["env"] = [
            item
            for item in container["env"]
            if item.get("name") != "TRACING_ZIPKIN_SAMPLER_PROBABILITY"
        ]

    cases: list[tuple[str, Callable[[dict[str, list[dict[str, Any]]]], None], str]] = [
        ("bad selector", bad_selector, "selector"),
        ("wrong port", wrong_port, "named container ports"),
        ("missing Secret reference", missing_secret, "PRODUCT_DATABASE_URL"),
        ("external exposure", external_exposure, "must remain ClusterIP"),
        ("missing QA activation", missing_activation, "does not contain Go Product"),
        ("stopped marker with Go objects", stopped_with_go, "still contains Go Product objects"),
        ("unreleased QA image tag", unreleased_qa_tag, "qa image reference is invalid"),
        ("invalid QA image tag", invalid_qa_tag, "qa image reference is invalid"),
        ("disabled QA telemetry", disabled_qa_telemetry, "qa Product ConfigMap"),
        ("missing QA Pod identity", missing_qa_instance, "PRODUCT_DATABASE_URL"),
        ("missing Java sampler", missing_java_sampler, "bind the QA Zipkin sampler"),
    ]

    with tempfile.TemporaryDirectory(prefix="product-deployment-checks.") as directory:
        fixture_root = Path(directory)
        semantic_paths: dict[str, Path] = {}
        for key, documents in semantic.items():
            path = fixture_root / f"semantic-{key}.json"
            path.write_text(json.dumps(documents), encoding="utf-8")
            semantic_paths[key] = path
        semantic_command = [sys.executable, str(arguments.checker)]
        for key in ("local-go", "local-legacy", "qa-go", "qa-legacy", "qa-aggregate"):
            semantic_command.extend((f"--{key}", str(semantic_paths[key])))
        semantic_result = subprocess.run(
            semantic_command, text=True, capture_output=True, check=False, timeout=30
        )
        if semantic_result.returncode != 0:
            print(
                "product-deploy-check-test: semantic QA tag was rejected: "
                f"{semantic_result.stderr.strip()}",
                file=sys.stderr,
            )
            return 1
        stopped = copy.deepcopy(active)
        missing_activation(stopped)
        mark_stopped(stopped)
        stopped_paths: dict[str, Path] = {}
        for key, documents in stopped.items():
            path = fixture_root / f"stopped-{key}.json"
            path.write_text(json.dumps(documents), encoding="utf-8")
            stopped_paths[key] = path
        stopped_command = [sys.executable, str(arguments.checker)]
        for key in ("local-go", "local-legacy", "qa-go", "qa-legacy", "qa-aggregate"):
            stopped_command.extend((f"--{key}", str(stopped_paths[key])))
        stopped_result = subprocess.run(
            stopped_command, text=True, capture_output=True, check=False, timeout=30
        )
        if stopped_result.returncode != 0:
            print(
                "product-deploy-check-test: explicit stopped QA state was rejected: "
                f"{stopped_result.stderr.strip()}",
                file=sys.stderr,
            )
            return 1
        for index, (description, mutation, expected_error) in enumerate(cases):
            values = copy.deepcopy(active)
            mutation(values)
            paths: dict[str, Path] = {}
            for key, documents in values.items():
                path = fixture_root / f"{index}-{key}.json"
                path.write_text(json.dumps(documents), encoding="utf-8")
                paths[key] = path
            command = [
                sys.executable,
                str(arguments.checker),
                "--local-go",
                str(paths["local-go"]),
                "--local-legacy",
                str(paths["local-legacy"]),
                "--qa-go",
                str(paths["qa-go"]),
                "--qa-legacy",
                str(paths["qa-legacy"]),
                "--qa-aggregate",
                str(paths["qa-aggregate"]),
            ]
            result = subprocess.run(
                command, text=True, capture_output=True, check=False, timeout=30
            )
            if result.returncode == 0:
                print(
                    f"product-deploy-check-test: expected failure for {description}",
                    file=sys.stderr,
                )
                return 1
            if expected_error not in result.stderr:
                print(
                    "product-deploy-check-test: "
                    f"{description} returned an unexpected diagnostic: {result.stderr.strip()}",
                    file=sys.stderr,
                )
                return 1

    print(
        "product-deploy-check-test: passed semantic QA tag plus selector, port, Secret, "
        "exposure, active/stopped-state, image-tag, telemetry identity, and Java sampler scenarios"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
