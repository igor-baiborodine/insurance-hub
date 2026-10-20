#!/usr/bin/env python3
"""Compare a live read-only Product HTTP surface with its accepted baseline."""

from __future__ import annotations

import os
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

from scripts.product_baseline import capture, compare_http, fixtures


def main(_argv: Sequence[str] | None = None) -> int:
    selected_environment = os.environ.get("BASELINE_ENV", "")
    try:
        capture.require(
            selected_environment in fixtures.ENVIRONMENTS,
            "BASELINE_ENV is required; use local-dev or qa.",
        )
        token = capture.token_file()
        _, catalog, _ = capture.identities(selected_environment)
        accepted = fixtures.load_document(
            fixtures.FIXTURE_ROOT / "http" / f"{selected_environment}.json"
        )
        with tempfile.TemporaryDirectory(prefix="product-baseline-verify-") as directory:
            temporary = Path(directory)
            before, after_port = capture.preflight_pair(temporary, catalog)
            observations = [
                capture.observation(
                    "direct",
                    "HTTP-DIRECT-LIST-001",
                    "/products",
                    None,
                    "none",
                    require_success=True,
                ),
                capture.observation(
                    "gateway",
                    "HTTP-GATEWAY-LIST-001",
                    "/api/products",
                    token,
                    "gateway-valid",
                    require_success=True,
                ),
            ]
            for code in ("CAR", "FAI", "HSI", "TRI"):
                observations.append(
                    capture.observation(
                        "direct",
                        f"HTTP-DIRECT-GET-{code}-001",
                        f"/products/{code}",
                        None,
                        "none",
                        require_success=True,
                    )
                )
                observations.append(
                    capture.observation(
                        "gateway",
                        f"HTTP-GATEWAY-GET-{code}-001",
                        f"/api/products/{code}",
                        token,
                        "gateway-valid",
                        require_success=True,
                    )
                )
            del token
            capture.validate_after(temporary, before, catalog, after_port)
        compare_http.compare_documents(
            accepted, {"environment": selected_environment, "observations": observations}
        )
    except (
        capture.CaptureError,
        compare_http.ComparisonError,
        fixtures.FixtureError,
        OSError,
        KeyError,
        TypeError,
    ) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    print(
        f"Read-only live verification passed for {selected_environment}; "
        "schema and catalog identities were unchanged."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
