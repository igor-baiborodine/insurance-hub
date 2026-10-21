"""Prepare a local legacy development host and its local jsreport template."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

PORTS = (80, 8081, 8090)
TEMPLATE_URL = "http://localhost:5488/odata/templates"
TEMPLATE = {"name": "POLICY", "recipe": "chrome-pdf", "engine": "handlebars", "content": "POLICY"}


def updated_hosts(text: str) -> str:
    lines: list[str] = []
    found = False
    for original_line in text.splitlines(keepends=True):
        line = original_line.rstrip("\r\n")
        ending = original_line[len(line) :]
        address, separator, comment = line.partition("#")
        parts = address.split()
        if not parts:
            lines.append(original_line)
            continue
        if parts[0] != "127.0.0.1" and "kafkaserver" not in parts[1:]:
            lines.append(original_line)
            continue
        revised = re.sub(r"(?<=\s)kafkaserver(?=\s|$)", "", address).rstrip()
        if parts[0] == "127.0.0.1":
            revised += " kafkaserver"
            found = True
        if separator:
            revised += (" " if not revised.endswith(" ") else "") + separator + comment
        lines.append(revised + ending if revised != line else original_line)
    if not found:
        raise ValueError("/etc/hosts has no 127.0.0.1 entry")
    return "".join(lines)


def run_iptables(port: int, action: str) -> bool:
    result = subprocess.run(
        ["iptables", action, "INPUT", "-p", "tcp", "--dport", str(port), "-j", "ACCEPT"],
        timeout=10,
        check=False,
        capture_output=True,
        text=True,
    )
    if action == "-C":
        return result.returncode == 0
    if result.returncode:
        raise RuntimeError(f"iptables update failed for port {port} (status {result.returncode})")
    return True


def template_exists() -> bool:
    with urllib.request.urlopen(
        TEMPLATE_URL + "?$filter=name%20eq%20%27POLICY%27", timeout=10
    ) as response:
        payload = json.load(response)
    values = payload.get("value") if isinstance(payload, dict) else None
    if not isinstance(values, list):
        raise ValueError("jsreport returned an invalid template list")
    return any(isinstance(value, dict) and value.get("name") == "POLICY" for value in values)


def create_template() -> None:
    request = urllib.request.Request(
        TEMPLATE_URL,
        data=json.dumps(TEMPLATE).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=10):
        pass


def prepare(*, dry_run: bool, hosts: Path = Path("/etc/hosts")) -> None:
    original = hosts.read_text(encoding="utf-8")
    replacement = updated_hosts(original)
    missing_ports = [port for port in PORTS if not run_iptables(port, "-C")]
    missing_template = not template_exists()
    if dry_run:
        print(
            f"Would update hosts: {replacement != original}; open ports: {missing_ports}; "
            f"create POLICY template: {missing_template}"
        )
        return
    if os.geteuid() != 0:
        raise PermissionError("root privilege is required to update hosts and iptables")
    if replacement != original:
        stat = hosts.stat()
        temp_path = None
        try:
            with tempfile.NamedTemporaryFile(
                mode="w", encoding="utf-8", dir=hosts.parent, prefix=".hosts-", delete=False
            ) as stream:
                temp_path = Path(stream.name)
                os.fchmod(stream.fileno(), stat.st_mode & 0o777)
                os.fchown(stream.fileno(), stat.st_uid, stat.st_gid)
                stream.write(replacement)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temp_path, hosts)
        finally:
            if temp_path is not None:
                temp_path.unlink(missing_ok=True)
    for port in missing_ports:
        run_iptables(port, "-A")
        if not run_iptables(port, "-C"):
            raise RuntimeError(f"iptables postcondition failed for port {port}")
    if missing_template:
        create_template()
        if not template_exists():
            raise RuntimeError("jsreport POLICY template was not found after creation")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", required=True, choices=("localhost",))
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    try:
        prepare(dry_run=args.dry_run)
    except (OSError, ValueError, RuntimeError, urllib.error.URLError) as error:
        print(
            f"ERROR: {error}; inspect hosts, iptables and jsreport before retrying", file=sys.stderr
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
