"""Merge atomic Go unit and executable-smoke profiles without duplicate statements."""

from __future__ import annotations

import argparse
from pathlib import Path


def merge_profiles(profiles: list[Path]) -> str:
    blocks: dict[str, tuple[int, int]] = {}
    for profile in profiles:
        lines = profile.read_text().splitlines()
        if not lines or lines[0] != "mode: atomic":
            raise ValueError(f"expected atomic coverage: {profile}")
        for line in lines[1:]:
            coordinate, statements_text, count_text = line.split()
            statements, count = int(statements_text), int(count_text)
            previous_statements, previous_count = blocks.get(coordinate, (statements, 0))
            if previous_statements != statements or statements < 0 or count < 0:
                raise ValueError(f"incompatible coverage block: {coordinate}")
            blocks[coordinate] = (statements, previous_count + count)
    if not blocks:
        raise ValueError("coverage profiles contain no statements")
    return "mode: atomic\n" + "".join(
        f"{coordinate} {statements} {count}\n"
        for coordinate, (statements, count) in sorted(blocks.items())
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path, help="existing unit profile to update")
    parser.add_argument("integration", type=Path, help="executable smoke profile")
    args = parser.parse_args()
    try:
        merged = merge_profiles([args.output, args.integration])
        args.output.write_text(merged)
    except (OSError, ValueError) as error:
        parser.exit(1, f"coverage merge failed: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
