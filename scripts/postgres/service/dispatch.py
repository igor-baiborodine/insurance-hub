#!/usr/bin/env python3
"""Fail-closed dispatch for service-owned PostgreSQL role profiles."""

import json
import os
import re
import sys
from collections.abc import Sequence
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
ACTIONS = {"inventory", "snapshot", "provision", "verify", "rotate", "revoke"}


class DispatchError(ValueError):
    """The requested role profile is invalid or unsafe."""


def resolve(action: str, service: str, profile: str) -> Path:
    if action not in ACTIONS:
        raise DispatchError("unsupported action")
    for label, value in (("SVC_NAME", service), ("ROLE_PROFILE", profile)):
        if not re.fullmatch(r"[a-z][a-z0-9-]*", value):
            raise DispatchError(f"{label} is required and must be a service identifier")
    catalog = json.loads((Path(__file__).parent / "catalog.json").read_text())
    if catalog.get("schemaVersion") != 1 or not isinstance(catalog.get("profiles"), dict):
        raise DispatchError("invalid role-profile catalog")
    path = catalog["profiles"].get(service, {}).get(profile)
    if not isinstance(path, str):
        raise DispatchError("unsupported service/profile pair")
    root = ROOT.resolve()
    selected = (root / path).resolve()
    if not selected.is_relative_to(root / "scripts") or not selected.is_file():
        raise DispatchError("profile path escapes scripts or is missing")
    return selected


def main(argv: Sequence[str] | None = None) -> int:
    arguments = list(sys.argv[1:] if argv is None else argv)
    try:
        if len(arguments) != 1:
            raise DispatchError("exactly one action is required")
        action = arguments[0]
        script = resolve(action, os.environ.get("SVC_NAME", ""), os.environ.get("ROLE_PROFILE", ""))
    except (DispatchError, OSError, json.JSONDecodeError) as error:
        print(f"postgres-service-role: {error}", file=sys.stderr)
        return 2
    os.execv(sys.executable, [sys.executable, str(script), action])
    return 1


if __name__ == "__main__":
    sys.exit(main())
