"""Prepare a local legacy development host and its local jsreport template."""

from __future__ import annotations

import argparse
import json
import os
import re
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

from legacy_process import process_group_exists, terminate_process_group

PORTS = (80, 8081, 8090)
TEMPLATE_URL = "http://localhost:5488/odata/templates"
TEMPLATE = {"name": "POLICY", "recipe": "chrome-pdf", "engine": "handlebars", "content": "POLICY"}
WORK_TIMEOUT_SECONDS = 120
COMMAND_TIMEOUT_SECONDS = 10
TEARDOWN_TIMEOUT_SECONDS = 5


class PrepareTerminated(Exception):
    """The host preparation CLI received SIGTERM."""


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


def remaining_timeout(deadline: float | None) -> float:
    if deadline is None:
        return COMMAND_TIMEOUT_SECONDS
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("host preparation exceeded its 120-second work deadline")
    return min(COMMAND_TIMEOUT_SECONDS, remaining)


def run_iptables(port: int, action: str, *, deadline: float | None = None) -> bool:
    timeout = remaining_timeout(deadline)
    process = subprocess.Popen(
        ["iptables", action, "INPUT", "-p", "tcp", "--dport", str(port), "-j", "ACCEPT"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    try:
        _stdout, stderr = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired as error:
        cleaned = terminate_process_group(process, teardown_seconds=TEARDOWN_TIMEOUT_SECONDS)
        detail = "" if cleaned else "; cleanup also exceeded its deadline"
        raise RuntimeError(
            f"iptables {action} timed out; firewall state is unknown{detail}"
        ) from error
    except BaseException:
        terminate_process_group(process, teardown_seconds=TEARDOWN_TIMEOUT_SECONDS)
        raise
    if process_group_exists(process) and not terminate_process_group(
        process, teardown_seconds=TEARDOWN_TIMEOUT_SECONDS
    ):
        raise RuntimeError(f"iptables {action} cleanup exceeded its deadline")
    if action == "-C":
        if process.returncode == 0:
            return True
        if process.returncode == 1:
            return False
        raise RuntimeError(
            f"iptables inspection failed for port {port} (status {process.returncode}): "
            f"{stderr.strip() or 'no diagnostic'}"
        )
    if process.returncode:
        raise RuntimeError(f"iptables update failed for port {port} (status {process.returncode})")
    return True


def template_exists(*, deadline: float | None = None) -> bool:
    with urllib.request.urlopen(
        TEMPLATE_URL + "?$filter=name%20eq%20%27POLICY%27",
        timeout=remaining_timeout(deadline),
    ) as response:
        payload = json.load(response)
    values = payload.get("value") if isinstance(payload, dict) else None
    if not isinstance(values, list):
        raise ValueError("jsreport returned an invalid template list")
    return any(isinstance(value, dict) and value.get("name") == "POLICY" for value in values)


def create_template(*, deadline: float | None = None) -> None:
    request = urllib.request.Request(
        TEMPLATE_URL,
        data=json.dumps(TEMPLATE).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=remaining_timeout(deadline)):
        pass


def prepare(
    *, dry_run: bool, hosts: Path = Path("/etc/hosts"), deadline: float | None = None
) -> None:
    deadline = deadline if deadline is not None else time.monotonic() + WORK_TIMEOUT_SECONDS
    original = hosts.read_text(encoding="utf-8")
    replacement = updated_hosts(original)
    missing_ports = [port for port in PORTS if not run_iptables(port, "-C", deadline=deadline)]
    missing_template = not template_exists(deadline=deadline)
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
        run_iptables(port, "-A", deadline=deadline)
        if not run_iptables(port, "-C", deadline=deadline):
            raise RuntimeError(f"iptables postcondition failed for port {port}")
    if missing_template:
        create_template(deadline=deadline)
        if not template_exists(deadline=deadline):
            raise RuntimeError("jsreport POLICY template was not found after creation")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", required=True, choices=("localhost",))
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    previous_term = signal.getsignal(signal.SIGTERM)

    def interrupted(_signal: int, _frame: object) -> None:
        raise PrepareTerminated

    signal.signal(signal.SIGTERM, interrupted)
    try:
        prepare(dry_run=args.dry_run)
    except KeyboardInterrupt:
        print(
            "Host preparation interrupted; inspect partial state before retrying", file=sys.stderr
        )
        return 130
    except PrepareTerminated:
        print("Host preparation terminated; inspect partial state before retrying", file=sys.stderr)
        return 143
    except (
        OSError,
        ValueError,
        RuntimeError,
        TimeoutError,
        subprocess.TimeoutExpired,
        urllib.error.URLError,
    ) as error:
        print(
            f"ERROR: {error}; inspect hosts, iptables and jsreport before retrying", file=sys.stderr
        )
        return 1
    finally:
        signal.signal(signal.SIGTERM, previous_term)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
