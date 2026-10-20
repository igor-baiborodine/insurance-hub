#!/usr/bin/env python3
"""Require the direct Product access matrix in Go's JSON test reporter output."""

import argparse
import json
import sys
from collections import Counter
from collections.abc import Sequence
from pathlib import Path

CATEGORIES = (
    "NONE",
    "VALID",
    "MALFORMED",
    "SIGNATURE",
    "EXPIRED",
    "NBF",
    "ISSUER",
    "AUDIENCE",
    "ROLE",
)
PREFIXES = (
    "TestProductAccess/accepted_HTTP_credential_matrix_remains_anonymous/",
    "TestProductAccess/generated_gRPC_client_ignores_identity_metadata/",
)
EXPECTED = {
    prefix + f"ACCESS-DIRECT-{route}-{category}-001"
    for prefix in PREFIXES
    for route in ("LIST", "GET")
    for category in CATEGORIES
}


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("go_test_json_log", type=Path)
    args = parser.parse_args(argv)
    try:
        actions = Counter()
        case_actions = Counter()
        for line in args.go_test_json_log.read_text(encoding="utf-8").splitlines():
            record = json.loads(line)
            action = record.get("Action")
            name = record.get("Test", "")
            if action in ("fail", "skip") and (name.startswith("TestProductAccess") or not name):
                raise ValueError("failed or skipped test")
            if name == "TestProductAccess":
                actions[action] += 1
            if name in EXPECTED or any(name.startswith(prefix) for prefix in PREFIXES):
                case_actions[(name, action)] += 1
        if actions[("run")] != 1 or actions[("pass")] != 1:
            raise ValueError("Product access test did not run and pass exactly once")
        if {name for name, _ in case_actions} != EXPECTED:
            raise ValueError("direct case set is incomplete or altered")
        if any(
            case_actions[(name, "run")] != 1 or case_actions[(name, "pass")] != 1
            for name in EXPECTED
        ):
            raise ValueError("direct case did not run and pass exactly once")
    except (OSError, ValueError, TypeError, KeyError) as failure:
        print("PRODUCT_ACCESS_RESULT_MISMATCH: " + str(failure), file=sys.stderr)
        return 1
    print("Product access results: 18 direct IDs passed on each transport without skips")
    return 0


if __name__ == "__main__":
    sys.exit(main())
