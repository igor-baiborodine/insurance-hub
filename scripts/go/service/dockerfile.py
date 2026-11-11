#!/usr/bin/env python3
"""Render or check the fixed Dockerfile template for one configured Go service."""

from __future__ import annotations

import argparse
import difflib
import json
import os
import stat
import sys
import tempfile
from pathlib import Path
from string import Template

from scripts.go.service.tooling_config import ServiceTooling, load_tooling_config

TEMPLATE_PATH = Path(__file__).with_suffix(".template")


class DockerfileError(ValueError):
    """A configured Dockerfile cannot be rendered or differs from its generated form."""


def render(config: ServiceTooling, *, template_path: Path = TEMPLATE_PATH) -> str:
    """Render deterministic Dockerfile bytes from validated typed configuration."""
    dockerfile = config.dockerfile
    if dockerfile is None:
        raise DockerfileError("configuration does not declare a generated Dockerfile")
    try:
        template = Template(template_path.read_text(encoding="utf-8"))
    except OSError as error:
        raise DockerfileError("Dockerfile template is unreadable") from error
    source_copies = "\n".join(
        f"COPY {item.source} {item.destination}" for item in dockerfile.source_copies
    )
    build_commands = "\n\n".join(
        "RUN GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \\\n"
        "    go build -mod=readonly -trimpath -buildvcs=false \\\n"
        f"    -ldflags='-s -w -buildid=' -o {item.output} {item.package}"
        for item in dockerfile.build_outputs
    )
    runtime_assets = [item for item in dockerfile.runtime_copies if item.source.startswith("/src/")]
    runtime_asset_commands = ""
    if runtime_assets:
        installs = " && \\\n    ".join(
            f"install -D -m {item.mode} {item.source} /runtime-root{item.destination}"
            for item in runtime_assets
        )
        runtime_asset_commands = f"\n\nRUN {installs}"
    runtime_items = [
        (item.output, item.destination, item.mode) for item in dockerfile.build_outputs
    ] + [
        (item.source, item.destination, item.mode)
        for item in dockerfile.runtime_copies
        if item.source.startswith("/out/")
    ]
    runtime_copy_lines = [
        f"COPY --from=build{f' --chmod={mode}' if mode else ''} {source} {destination}"
        for source, destination, mode in runtime_items
    ]
    if runtime_assets:
        runtime_copy_lines.append("COPY --from=build /runtime-root/ /")
    runtime_copies = "\n".join(runtime_copy_lines)
    try:
        return template.substitute(
            source_copies=source_copies,
            build_commands=build_commands,
            runtime_asset_commands=runtime_asset_commands,
            title=json.dumps(config.image.title),
            runtime_copies=runtime_copies,
            user=config.image.user,
            ports=" ".join(str(port) for port in dockerfile.ports),
            entrypoint=json.dumps(list(config.image.entrypoint)),
        )
    except (KeyError, ValueError) as error:
        raise DockerfileError("Dockerfile template has an unsupported placeholder") from error


def generate(config: ServiceTooling) -> None:
    """Atomically replace the module Dockerfile after a complete successful render."""
    output = config.module_root / "Dockerfile"
    content = render(config)
    output_mode = stat.S_IMODE(output.stat().st_mode) if output.exists() else 0o644
    descriptor, temporary = tempfile.mkstemp(prefix=".Dockerfile.", dir=config.module_root)
    temporary_path = Path(temporary)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="") as stream:
            stream.write(content)
        temporary_path.chmod(output_mode)
        temporary_path.replace(output)
    except BaseException:
        temporary_path.unlink(missing_ok=True)
        raise


def check(config: ServiceTooling) -> None:
    """Reject a missing or drifted Dockerfile without mutating it."""
    output = config.module_root / "Dockerfile"
    expected = render(config)
    try:
        actual = output.read_text(encoding="utf-8")
    except OSError as error:
        raise DockerfileError("generated Dockerfile is missing") from error
    if actual == expected:
        return
    difference = "".join(
        difflib.unified_diff(
            actual.splitlines(keepends=True),
            expected.splitlines(keepends=True),
            fromfile=str(output),
            tofile="generated Dockerfile",
        )
    )
    raise DockerfileError(f"generated Dockerfile differs:\n{difference}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("--repository-root", type=Path)
    parser.add_argument("action", choices=("generate", "check"))
    arguments = parser.parse_args(argv)
    try:
        options = (
            {"repository_root": arguments.repository_root}
            if arguments.repository_root is not None
            else {}
        )
        config = load_tooling_config(arguments.config, **options)
        if arguments.action == "generate":
            generate(config)
        else:
            check(config)
    except (DockerfileError, OSError, ValueError) as error:
        print(f"go-service-dockerfile: {error}", file=sys.stderr)
        return 1
    print(f"go-service-dockerfile: service={config.service} action={arguments.action} passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
