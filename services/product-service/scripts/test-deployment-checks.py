#!/usr/bin/env python3

"""Exercise controlled failures against the Product deployment checker."""

from __future__ import annotations

import argparse
import copy
import json
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any, Callable


def load(path: Path) -> list[dict[str, Any]]:
    return json.loads(path.read_text(encoding="utf-8"))


def find(documents: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    for document in documents:
        if document.get("kind") == kind and document.get("metadata", {}).get("name") == name:
            return document
    raise AssertionError(f"fixture does not contain {kind}/{name}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--checker", required=True, type=Path)
    parser.add_argument("--local-go", required=True, type=Path)
    parser.add_argument("--local-legacy", required=True, type=Path)
    parser.add_argument("--qa-go", required=True, type=Path)
    parser.add_argument("--qa-legacy", required=True, type=Path)
    parser.add_argument("--qa-aggregate", required=True, type=Path)
    parser.add_argument("--qa-preview", required=True, type=Path)
    return parser.parse_args()


def main() -> int:
    arguments = parse_args()
    baseline = {
        "local-go": load(arguments.local_go),
        "local-legacy": load(arguments.local_legacy),
        "qa-go": load(arguments.qa_go),
        "qa-legacy": load(arguments.qa_legacy),
        "qa-aggregate": load(arguments.qa_aggregate),
        "qa-preview": load(arguments.qa_preview),
    }

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

    def accidental_activation(values: dict[str, list[dict[str, Any]]]) -> None:
        values["qa-aggregate"] = copy.deepcopy(values["qa-preview"])

    cases: list[tuple[str, Callable[[dict[str, list[dict[str, Any]]]], None], str]] = [
        ("bad selector", bad_selector, "selector"),
        ("wrong port", wrong_port, "named container ports"),
        ("missing Secret reference", missing_secret, "PRODUCT_DATABASE_URL"),
        ("external exposure", external_exposure, "must remain ClusterIP"),
        ("accidental QA activation", accidental_activation, "unexpectedly contains Go Product"),
    ]

    with tempfile.TemporaryDirectory(prefix="product-deployment-checks.") as directory:
        fixture_root = Path(directory)
        for index, (description, mutation, expected_error) in enumerate(cases):
            values = copy.deepcopy(baseline)
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
                "--qa-preview",
                str(paths["qa-preview"]),
            ]
            result = subprocess.run(command, text=True, capture_output=True, check=False)
            if result.returncode == 0:
                print(f"product-deploy-check-test: expected failure for {description}", file=sys.stderr)
                return 1
            if expected_error not in result.stderr:
                print(
                    "product-deploy-check-test: "
                    f"{description} returned an unexpected diagnostic: {result.stderr.strip()}",
                    file=sys.stderr,
                )
                return 1

    print(
        "product-deploy-check-test: passed selector, port, Secret, exposure, "
        "and activation failures"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
