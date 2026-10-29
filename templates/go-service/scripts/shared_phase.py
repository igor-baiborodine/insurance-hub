"""Run scaffold-owned generated, breaking, or copy checks under candidate ownership."""

from __future__ import annotations

import argparse
import os
import signal
import sys
from pathlib import Path

from scaffold_process import run_command

TARGETS = {
    "generated": "check-proto-drift",
    "breaking": "check-proto-breaking",
    "copy": "check-copy",
}


class Terminated(BaseException):
    """A handled termination signal interrupted the owned phase."""


def _terminate(_signum: int, _frame: object) -> None:
    raise Terminated


def context_request(operation: str, phase: str) -> dict:
    from shared_candidate import context_request as request

    return request(operation, phase)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=sorted(TARGETS))
    args = parser.parse_args(argv)
    previous = signal.signal(signal.SIGTERM, _terminate)
    try:
        context = context_request("begin", args.phase)
        environment = {**os.environ, **context["environment"]}
        if args.phase == "copy":
            environment["SERVICEKIT_CANDIDATE_LIBRARY"] = str(
                Path.cwd().parents[1] / "libs" / "servicekit"
            )
        run_command(
            ["make", "--no-print-directory", TARGETS[args.phase]],
            cwd=Path.cwd(),
            env=environment,
            timeout_seconds=2100,
            teardown_seconds=15,
            activity=f"candidate scaffold {args.phase}",
        )
        context_request("end", args.phase)
    except (RuntimeError, OSError, ValueError) as error:
        print(f"shared-phase: {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("shared-phase: interrupted", file=sys.stderr)
        return 130
    except Terminated:
        print("shared-phase: terminated", file=sys.stderr)
        return 143
    finally:
        signal.signal(signal.SIGTERM, previous)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
