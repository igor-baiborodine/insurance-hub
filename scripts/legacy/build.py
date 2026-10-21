"""Build the fixed legacy Docker images without pruning unrelated images."""

from __future__ import annotations

import argparse
import signal
import subprocess
import sys
import time
from pathlib import Path

from legacy_process import process_group_exists, terminate_process_group

ROOT = Path(__file__).resolve().parents[2]
SERVICES = (
    "agent-portal-gateway",
    "auth",
    "product",
    "policy-search",
    "dashboard",
    "policy",
    "pricing",
    "document",
    "payment",
    "chat",
)


def image_for(service: str) -> tuple[str, str]:
    if service not in SERVICES:
        raise ValueError(f"unknown legacy service: {service}")
    if service == "agent-portal-gateway":
        return "legacy/agent-portal-gateway", "insurance-hub-agent-portal-gateway-legacy:latest"
    folder = "documents-service" if service == "document" else f"{service}-service"
    return f"legacy/{folder}", f"insurance-hub-{service}-api-legacy:latest"


def build(
    folder: str,
    image: str,
    *,
    root: Path = ROOT,
    timeout_seconds: float = 1800,
    teardown_seconds: float = 5,
) -> None:
    dockerfile = root / folder / "Dockerfile"
    if not dockerfile.is_file():
        raise ValueError(f"Dockerfile is missing: {dockerfile}")
    command = [
        "docker",
        "build",
        "--no-cache",
        "-f",
        str(dockerfile),
        str(root / folder),
        "-t",
        image,
    ]
    print(f"Building {image} from {folder}", file=sys.stderr)
    process = subprocess.Popen(command, cwd=root, start_new_session=True)
    started = time.monotonic()
    deadline = started + timeout_seconds
    previous_term = signal.getsignal(signal.SIGTERM)

    def interrupted(_signal: int, _frame: object) -> None:
        raise InterruptedError("build interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    try:
        while True:
            try:
                status = process.wait(timeout=min(30, max(0.1, deadline - time.monotonic())))
                if status:
                    raise RuntimeError(f"Docker build failed for {image} (status {status})")
                break
            except subprocess.TimeoutExpired:
                if time.monotonic() >= deadline:
                    raise RuntimeError(f"Docker build timed out for {image}") from None
                elapsed = time.monotonic() - started
                print(
                    f"Docker build still running for {image} after {elapsed:.0f}s", file=sys.stderr
                )
    except BaseException:
        if process_group_exists(process) and not terminate_process_group(
            process, teardown_seconds=teardown_seconds
        ):
            print(
                f"ERROR: Docker build cleanup exceeded {teardown_seconds:g} seconds for {image}",
                file=sys.stderr,
            )
        raise
    finally:
        signal.signal(signal.SIGTERM, previous_term)
    if process_group_exists(process) and not terminate_process_group(
        process, teardown_seconds=teardown_seconds
    ):
        raise RuntimeError(
            f"Docker build cleanup exceeded {teardown_seconds:g} seconds for {image}"
        )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("service", "all", "frontend"))
    parser.add_argument("--service")
    args = parser.parse_args(argv)
    try:
        if args.kind == "service":
            if not args.service:
                parser.error("--service is required for service builds")
            build(*image_for(args.service))
        elif args.kind == "all":
            deadline = time.monotonic() + 7200
            for service in SERVICES:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise RuntimeError("legacy build batch exceeded its 7200-second deadline")
                build(*image_for(service), timeout_seconds=min(1800, remaining))
        else:
            build("legacy/web-vue", "insurance-hub-web-vue-legacy:latest")
    except KeyboardInterrupt:
        print("Build interrupted", file=sys.stderr)
        return 130
    except InterruptedError:
        print("Build terminated", file=sys.stderr)
        return 143
    except (OSError, ValueError, RuntimeError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
