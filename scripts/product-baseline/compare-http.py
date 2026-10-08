#!/usr/bin/env python3
"""Compare Product HTTP observations without weakening contract-sensitive JSON values."""

import argparse
import difflib
import json
import sys
from decimal import Decimal
from pathlib import Path


class ComparisonError(Exception):
    pass


def load_json(path):
    with path.open(encoding="utf-8") as source:
        return json.load(source, parse_float=Decimal, parse_int=Decimal)


def canonical(value, top_level=False):
    if isinstance(value, dict):
        return ("object", tuple((key, canonical(value[key])) for key in sorted(value)))
    if isinstance(value, list):
        items = [canonical(item) for item in value]
        if top_level:
            items.sort(key=repr)
        return ("array", tuple(items))
    if isinstance(value, Decimal):
        return ("number", str(value))
    if value is None:
        return ("null",)
    if isinstance(value, bool):
        return ("boolean", value)
    return ("string", value)


def body_value(raw_body, scenario_id):
    try:
        value = json.loads(raw_body, parse_float=Decimal, parse_int=Decimal)
    except json.JSONDecodeError as error:
        raise ComparisonError(f"{scenario_id}: response body is not valid JSON: {error}") from error
    return canonical(value, top_level=isinstance(value, list))


def observations_by_id(document, label):
    observations = document.get("observations")
    if not isinstance(observations, list):
        raise ComparisonError(f"{label}: observations must be an array")
    result = {}
    for observation in observations:
        scenario_id = observation.get("scenarioId")
        if not scenario_id or scenario_id in result:
            raise ComparisonError(f"{label}: missing or duplicate scenarioId {scenario_id!r}")
        result[scenario_id] = observation
    return result


def difference(expected, actual):
    expected_lines = repr(expected).splitlines()
    actual_lines = repr(actual).splitlines()
    return "\n".join(difflib.unified_diff(
        expected_lines, actual_lines, fromfile="accepted", tofile="observed", lineterm=""))


def compare_documents(expected, actual):
    if expected.get("environment") != actual.get("environment"):
        raise ComparisonError(
            f"environment differs: accepted={expected.get('environment')!r}, "
            f"observed={actual.get('environment')!r}")

    expected_observations = observations_by_id(expected, "accepted")
    actual_observations = observations_by_id(actual, "observed")
    expected_ids = set(expected_observations)
    actual_ids = set(actual_observations)
    if expected_ids != actual_ids:
        missing = sorted(expected_ids - actual_ids)
        unexpected = sorted(actual_ids - expected_ids)
        raise ComparisonError(f"scenario set differs: missing={missing}, unexpected={unexpected}")

    for scenario_id in sorted(expected_ids):
        accepted = expected_observations[scenario_id]
        observed = actual_observations[scenario_id]
        fields = (
            ("boundary", accepted.get("boundary"), observed.get("boundary")),
            ("method", accepted.get("request", {}).get("method"), observed.get("request", {}).get("method")),
            ("path", accepted.get("request", {}).get("path"), observed.get("request", {}).get("path")),
            ("query", accepted.get("request", {}).get("query"), observed.get("request", {}).get("query")),
            ("status", accepted.get("response", {}).get("status"), observed.get("response", {}).get("status")),
            ("contentType", accepted.get("response", {}).get("headers", {}).get("contentType"),
             observed.get("response", {}).get("headers", {}).get("contentType")),
        )
        for field, expected_value, actual_value in fields:
            if expected_value != actual_value:
                raise ComparisonError(
                    f"{scenario_id}: {field} differs: accepted={expected_value!r}, observed={actual_value!r}")

        accepted_body = body_value(accepted["response"]["rawBody"], scenario_id)
        observed_body = body_value(observed["response"]["rawBody"], scenario_id)
        if accepted_body != observed_body:
            raise ComparisonError(f"{scenario_id}: response JSON differs\n{difference(accepted_body, observed_body)}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("expected", type=Path)
    parser.add_argument("actual", type=Path)
    args = parser.parse_args()
    try:
        compare_documents(load_json(args.expected), load_json(args.actual))
    except (ComparisonError, KeyError) as error:
        print(f"ERROR: Product HTTP baseline comparison failed: {error}", file=sys.stderr)
        return 2
    print(f"Product HTTP baseline comparison passed: {args.actual}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
