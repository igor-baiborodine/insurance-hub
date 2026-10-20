#!/usr/bin/env python3
"""Provision pinned Product documentation renderer tools explicitly."""

from __future__ import annotations

import os
import sys
from collections.abc import Sequence
from pathlib import Path

from product_process import ProductCommandError, run

MODULE_ROOT = Path(__file__).resolve().parent.parent
TOOLS_ROOT = MODULE_ROOT / ".tools"
TOOLS_BIN = TOOLS_ROOT / "bin"
MMDC = TOOLS_BIN / "mmdc"
MERMAID_VERSION = "11.14.0"
PUPPETEER_VERSION = "24.43.1"


def captured(arguments: list[str], activity: str) -> str:
    return run(
        arguments,
        cwd=MODULE_ROOT,
        env=os.environ,
        timeout_seconds=30,
        teardown_seconds=5,
        activity=activity,
        capture=True,
    ).strip()


def main(_argv: Sequence[str] | None = None) -> int:
    node = os.environ.get("NODE", "node")
    npm = os.environ.get("NPM", "npm")
    try:
        major = captured(
            [node, "-p", 'process.versions.node.split(".")[0]'],
            "inspect Product documentation Node runtime",
        )
        if not major.isdigit() or int(major) < 20:
            raise ValueError("Node.js >= 20 is required for Mermaid CLI")
        installed_mermaid = ""
        installed_puppeteer = ""
        try:
            installed_mermaid = captured([str(MMDC), "--version"], "inspect Mermaid CLI")
            installed_puppeteer = captured(
                [
                    node,
                    "-p",
                    f"require('{TOOLS_ROOT}/node_modules/puppeteer/package.json').version",
                ],
                "inspect Puppeteer",
            )
        except ProductCommandError:
            pass
        environment = dict(os.environ, PUPPETEER_CACHE_DIR=str(TOOLS_ROOT / "puppeteer"))
        if installed_mermaid != MERMAID_VERSION or installed_puppeteer != PUPPETEER_VERSION:
            TOOLS_BIN.mkdir(parents=True, exist_ok=True)
            (TOOLS_ROOT / "puppeteer").mkdir(parents=True, exist_ok=True)
            run(
                [
                    npm,
                    "install",
                    "--prefix",
                    str(TOOLS_ROOT),
                    "--no-save",
                    "--no-package-lock",
                    f"@mermaid-js/mermaid-cli@{MERMAID_VERSION}",
                    f"puppeteer@{PUPPETEER_VERSION}",
                ],
                cwd=MODULE_ROOT,
                env=environment,
                timeout_seconds=900,
                teardown_seconds=20,
                activity="install Product documentation tools",
            )
            MMDC.unlink(missing_ok=True)
            MMDC.symlink_to(Path("../node_modules/.bin/mmdc"))
        browser = next(
            (
                path
                for path in (TOOLS_ROOT / "puppeteer").rglob("chrome-headless-shell")
                if path.is_file() and os.access(path, os.X_OK)
            ),
            None,
        )
        if browser is None:
            run(
                [npm, "rebuild", "--prefix", str(TOOLS_ROOT), "puppeteer"],
                cwd=MODULE_ROOT,
                env=environment,
                timeout_seconds=900,
                teardown_seconds=20,
                activity="install Product documentation browser",
            )
        if captured([str(MMDC), "--version"], "verify Mermaid CLI") != MERMAID_VERSION:
            raise ValueError("Mermaid CLI verification failed")
        if (
            captured(
                [
                    node,
                    "-p",
                    f"require('{TOOLS_ROOT}/node_modules/puppeteer/package.json').version",
                ],
                "verify Puppeteer",
            )
            != PUPPETEER_VERSION
        ):
            raise ValueError("Puppeteer verification failed")
    except (ValueError, OSError, ProductCommandError) as error:
        print(f"bootstrap-doc-tools: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
