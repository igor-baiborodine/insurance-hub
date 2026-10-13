#!/usr/bin/env python3
"""Fail-closed dispatch for service-owned PostgreSQL role profiles."""

import json
import os
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[3]
ACTIONS = {"inventory", "snapshot", "provision", "verify", "rotate", "revoke"}


def fail(message: str) -> None:
    print(f"postgres-service-role: {message}", file=sys.stderr)
    raise SystemExit(2)


def resolve(action: str, service: str, profile: str) -> Path:
    if action not in ACTIONS:
        fail("unsupported action")
    for label, value in (("SVC_NAME", service), ("ROLE_PROFILE", profile)):
        if not re.fullmatch(r"[a-z][a-z0-9-]*", value):
            fail(f"{label} is required and must be a service identifier")
    catalog = json.loads((Path(__file__).parent / "catalog.json").read_text())
    if catalog.get("schemaVersion") != 1 or not isinstance(catalog.get("profiles"), dict):
        fail("invalid role-profile catalog")
    path = catalog["profiles"].get(service, {}).get(profile)
    if not isinstance(path, str):
        fail("unsupported service/profile pair")
    root = ROOT.resolve()
    selected = (root / path).resolve()
    if not selected.is_relative_to(root / "scripts") or not selected.is_file():
        fail("profile path escapes scripts or is missing")
    return selected


if __name__ == "__main__":
    if len(sys.argv) != 2:
        fail("exactly one action is required")
    script = resolve(sys.argv[1], os.environ.get("SVC_NAME", ""), os.environ.get("ROLE_PROFILE", ""))
    os.execv(sys.executable, [sys.executable, str(script), sys.argv[1]])
