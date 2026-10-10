#!/usr/bin/env python3

"""Validate Product documentation links and render every Mermaid diagram."""

from __future__ import annotations

import argparse
import contextlib
import hashlib
import html
import json
import re
import subprocess
import tempfile
import urllib.parse
from pathlib import Path


LINK_PATTERN = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
MERMAID_PATTERN = re.compile(r"```mermaid\s*\n(.*?)```", re.DOTALL)
HEADING_PATTERN = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$", re.MULTILINE)


def fail(message: str) -> None:
    raise SystemExit(f"check-docs: {message}")


def validate_puppeteer_config(path: Path) -> None:
    try:
        configuration = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"invalid Puppeteer configuration {path}: {error}")
    arguments = configuration.get("args")
    if not isinstance(arguments, list) or not all(
        isinstance(argument, str) for argument in arguments
    ):
        fail(f"invalid Puppeteer launch arguments in {path}")
    for required in ("--no-sandbox", "--disable-setuid-sandbox"):
        if required not in arguments:
            fail(f"missing required Chromium launch argument {required} in {path}")


def github_slug(heading: str) -> str:
    value = html.unescape(re.sub(r"<[^>]+>", "", heading)).strip().lower()
    value = re.sub(r"[^\w\- ]", "", value, flags=re.UNICODE)
    return re.sub(r"\s+", "-", value)


def anchors(markdown: str) -> set[str]:
    result: set[str] = set()
    occurrences: dict[str, int] = {}
    for heading in HEADING_PATTERN.findall(markdown):
        base = github_slug(heading)
        suffix = occurrences.get(base, 0)
        occurrences[base] = suffix + 1
        result.add(base if suffix == 0 else f"{base}-{suffix}")
    return result


def parse_destination(raw: str) -> str:
    destination = raw.strip()
    if destination.startswith("<") and ">" in destination:
        return destination[1 : destination.index(">")]
    return destination.split(maxsplit=1)[0]


def validate_links(document: Path, markdown: str) -> int:
    checked = 0
    for match in LINK_PATTERN.finditer(markdown):
        destination = parse_destination(match.group(1))
        parsed = urllib.parse.urlsplit(destination)
        if parsed.scheme or destination.startswith("//"):
            continue
        relative_path = urllib.parse.unquote(parsed.path)
        target = document if relative_path == "" else (document.parent / relative_path).resolve()
        if not target.exists():
            fail(f"{document}: missing local link target: {destination}")
        if parsed.fragment:
            if not target.is_file() or target.suffix.lower() not in {".md", ".markdown"}:
                fail(f"{document}: anchor target is not Markdown: {destination}")
            target_anchors = anchors(target.read_text(encoding="utf-8"))
            fragment = urllib.parse.unquote(parsed.fragment).lower()
            if fragment not in target_anchors:
                fail(f"{document}: missing local anchor: {destination}")
        checked += 1
    return checked


def render_mermaid(
    document: Path,
    markdown: str,
    renderer: Path,
    puppeteer_config: Path,
    output_directory: Path | None,
) -> int:
    diagrams = MERMAID_PATTERN.findall(markdown)
    if output_directory is None:
        workspace = tempfile.TemporaryDirectory(prefix="product-docs-")
    else:
        output_directory.mkdir(parents=True, exist_ok=True)
        workspace = contextlib.nullcontext(str(output_directory))
    with workspace as temporary:
        temporary_root = Path(temporary)
        document_id = hashlib.sha256(str(document).encode()).hexdigest()[:8]
        for index, diagram in enumerate(diagrams, start=1):
            source = temporary_root / f"diagram-{document_id}-{index}.mmd"
            suffix = ".svg" if output_directory is None else ".png"
            output = temporary_root / f"diagram-{document_id}-{index}{suffix}"
            source.write_text(diagram.strip() + "\n", encoding="utf-8")
            result = subprocess.run(
                [
                    str(renderer),
                    "--puppeteerConfigFile",
                    str(puppeteer_config),
                    "--input",
                    str(source),
                    "--output",
                    str(output),
                ],
                capture_output=True,
                check=False,
                text=True,
            )
            if result.returncode != 0:
                details = (result.stderr or result.stdout).strip()
                fail(f"Mermaid render failed for {document} block {index}: {details}")
            if not output.is_file() or output.stat().st_size == 0:
                fail(f"Mermaid renderer produced no output for {document} block {index}")
            if suffix == ".svg" and "<svg" not in output.read_text(encoding="utf-8"):
                fail(f"Mermaid renderer produced an invalid SVG for {document} block {index}")
    return len(diagrams)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--renderer", required=True, type=Path)
    parser.add_argument("--puppeteer-config", required=True, type=Path)
    parser.add_argument("--output-directory", type=Path)
    parser.add_argument("documents", nargs="+", type=Path)
    arguments = parser.parse_args()

    renderer = arguments.renderer.resolve()
    if not renderer.is_file():
        fail(f"missing Mermaid renderer: {renderer}")
    puppeteer_config = arguments.puppeteer_config.resolve()
    if not puppeteer_config.is_file():
        fail(f"missing Puppeteer configuration: {puppeteer_config}")
    validate_puppeteer_config(puppeteer_config)

    link_count = 0
    diagram_count = 0
    for supplied in arguments.documents:
        document = supplied.resolve()
        if not document.is_file():
            fail(f"missing documentation file: {supplied}")
        markdown = document.read_text(encoding="utf-8")
        link_count += validate_links(document, markdown)
        diagram_count += render_mermaid(
            document,
            markdown,
            renderer,
            puppeteer_config,
            arguments.output_directory,
        )

    if diagram_count == 0:
        fail("no Mermaid diagrams were found")
    print(
        f"check-docs: validated {len(arguments.documents)} document(s), "
        f"{link_count} local link(s), and rendered {diagram_count} Mermaid diagram(s)"
    )


if __name__ == "__main__":
    main()
