#!/usr/bin/env python3
"""Reject incomplete or altered disposable Product access case sets."""

import argparse
import json
import sys
from collections.abc import Sequence
from pathlib import Path

GATEWAY_CREDENTIALS = {
    "MISSING": ("missing", 401),
    "MALFORMED": ("malformed", 401),
    "SIGNATURE": ("invalid-signature", 401),
    "EXPIRED": ("expired", 401),
    "SUBJECT": ("missing-subject", 401),
    "VALID": ("valid", 200),
    "NBF": ("not-before", 200),
    "ISSUER": ("issuer-variation", 200),
    "AUDIENCE": ("audience-variation", 200),
    "ROLE": ("role-variation", 200),
}
DIRECT_CREDENTIALS = {
    "NONE": "missing",
    "VALID": "valid",
    "MALFORMED": "malformed",
    "SIGNATURE": "invalid-signature",
    "EXPIRED": "expired",
    "NBF": "not-before",
    "ISSUER": "issuer-variation",
    "AUDIENCE": "audience-variation",
    "ROLE": "role-variation",
}


def expected_cases() -> dict[str, tuple[str, str, str, int]]:
    result = {}
    for route, gateway_path, product_path in (
        ("LIST", "/api/products", "/products"),
        ("GET", "/api/products/TRI", "/products/TRI"),
    ):
        for suffix, (credential, status) in GATEWAY_CREDENTIALS.items():
            result[f"ACCESS-GATEWAY-{route}-{suffix}-001"] = (
                "gateway",
                gateway_path,
                credential,
                status,
            )
        for suffix, credential in DIRECT_CREDENTIALS.items():
            result[f"ACCESS-DIRECT-{route}-{suffix}-001"] = (
                "product",
                product_path,
                credential,
                200,
            )
    return result


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("access_cases", type=Path)
    args = parser.parse_args(argv)
    try:
        cases = json.loads(args.access_cases.read_text(encoding="utf-8"))["cases"]
        expected = expected_cases()
        actual = {}
        for case in cases:
            identity = case["id"]
            if identity in actual:
                raise ValueError("duplicate case")
            actual[identity] = (
                case["target"],
                case["path"],
                case["credential"],
                case["expected"]["status"],
            )
        if actual != expected:
            raise ValueError("missing or altered case")
    except (OSError, ValueError, KeyError, TypeError) as failure:
        print("SECURITY_CASE_SET_MISMATCH: " + str(failure), file=sys.stderr)
        return 1
    print("security access cases: 20 gateway and 18 direct IDs verified")
    return 0


if __name__ == "__main__":
    sys.exit(main())
