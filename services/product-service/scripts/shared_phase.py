"""Run Product-owned candidate phases under a live servicekit controller."""

from __future__ import annotations

import argparse
import os
import shutil
import sys
import tempfile
from pathlib import Path

from product_process import ProductCommandError, run, run_cli

from shared_candidate import context_request

MODULE_ROOT = Path(__file__).resolve().parent.parent
REPOSITORY_ROOT = MODULE_ROOT.parents[1]
PHASE_TARGETS = {
    "generated": ("check-proto-drift", "check-sql-drift"),
    "breaking": ("check-proto-breaking",),
    "integration": ("test-integration", "test-telemetry-continuity"),
}


def candidate_image(context: dict, environment: dict[str, str]) -> None:
    """Build the verified candidate into a disposable image, then run Product's image contract."""
    candidate = REPOSITORY_ROOT / "libs/servicekit"
    if not candidate.is_dir():
        raise ValueError("candidate library directory is missing")
    with tempfile.TemporaryDirectory(prefix="product-candidate-image-") as temporary:
        build_root = Path(temporary)
        for name in ("cmd", "gen", "internal"):
            shutil.copytree(
                MODULE_ROOT / name, build_root / name, ignore=shutil.ignore_patterns("*_test.go")
            )
        shutil.copytree(
            candidate,
            build_root / "servicekit",
            ignore=shutil.ignore_patterns("*_test.go", ".tools", "coverage.txt"),
        )
        alternate = Path(context["alternate"])
        manifest = alternate.read_text(encoding="utf-8")
        replacement = f"{candidate}"
        if manifest.count(replacement) != 1:
            raise ValueError("candidate replacement is missing or ambiguous")
        (build_root / "go.mod").write_text(
            manifest.replace(replacement, "/servicekit"), encoding="utf-8"
        )
        shutil.copy2(alternate.with_suffix(".sum"), build_root / "go.sum")
        dockerfile = (MODULE_ROOT / "Dockerfile").read_text(encoding="utf-8")
        needle = "COPY go.mod go.sum ./\n"
        if dockerfile.count(needle) != 1:
            raise ValueError("Product Dockerfile module-copy contract changed")
        dockerfile = dockerfile.replace(needle, needle + "COPY servicekit /servicekit\n")
        if dockerfile.count('ENTRYPOINT ["/product-service"]') != 1:
            raise ValueError("Product Dockerfile entrypoint contract changed")
        dockerfile = dockerfile.replace(
            'ENTRYPOINT ["/product-service"]',
            "ARG SERVICEKIT_CANDIDATE_SHA\n"
            'LABEL insurance-hub.servicekit.candidate="${SERVICEKIT_CANDIDATE_SHA}"\n'
            'ENTRYPOINT ["/product-service"]',
        )
        (build_root / "Dockerfile").write_text(dockerfile, encoding="utf-8")
        (build_root / ".dockerignore").write_text(
            "*\n!go.mod\n!go.sum\n!Dockerfile\n!servicekit/\n!servicekit/**\n"
            "!cmd/\n!cmd/**\n!gen/\n!gen/**\n!internal/\n!internal/**\n"
            "**/*_test.go\ninternal/testing/\ninternal/postgres/sql/\n",
            encoding="utf-8",
        )
        image = f"insurance-hub-product-api:candidate-{context['head'][:12]}-{os.getpid()}"
        try:
            run(
                [
                    "docker",
                    "buildx",
                    "build",
                    "--load",
                    "--platform",
                    "linux/amd64",
                    "--provenance=false",
                    "--tag",
                    image,
                    "--build-arg",
                    f"SOURCE_CREATED={environment['SOURCE_CREATED']}",
                    "--build-arg",
                    f"SOURCE_REPOSITORY={environment['SOURCE_REPOSITORY']}",
                    "--build-arg",
                    f"SOURCE_REVISION={context['head']}",
                    "--build-arg",
                    f"SOURCE_VERSION={environment['SOURCE_VERSION']}",
                    "--build-arg",
                    f"SERVICEKIT_CANDIDATE_SHA={context['head']}",
                    "--file",
                    str(build_root / "Dockerfile"),
                    str(build_root),
                ],
                cwd=REPOSITORY_ROOT,
                env=environment,
                timeout_seconds=1_200,
                teardown_seconds=20,
                activity="build Product candidate image",
            )
            label = run(
                [
                    "docker",
                    "image",
                    "inspect",
                    "--format",
                    '{{index .Config.Labels "insurance-hub.servicekit.candidate"}}',
                    image,
                ],
                cwd=REPOSITORY_ROOT,
                env=environment,
                timeout_seconds=30,
                activity="verify Product candidate image source",
                capture=True,
            ).strip()
            if label != context["head"]:
                raise ValueError("Product image lacks the verified candidate source identity")
            run(
                [sys.executable, str(MODULE_ROOT / "scripts/image_check.py")],
                cwd=MODULE_ROOT,
                env={**environment, "IMAGE_REF": image},
                timeout_seconds=900,
                teardown_seconds=20,
                activity="validate Product candidate image",
            )
        finally:
            previous_failure = sys.exc_info()[0] is not None
            try:
                run(
                    ["docker", "image", "rm", "-f", image],
                    cwd=REPOSITORY_ROOT,
                    env=environment,
                    timeout_seconds=60,
                    teardown_seconds=10,
                    activity="remove Product candidate image",
                )
            except ProductCommandError:
                if not previous_failure:
                    raise
                print("shared-phase: candidate image cleanup failed", file=sys.stderr)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=sorted((*PHASE_TARGETS, "image")))
    args = parser.parse_args(argv)
    try:
        context = context_request("begin", args.phase)
        environment = {**os.environ, **context["environment"]}
        environment["SERVICEKIT_CANDIDATE_LIBRARY"] = str(REPOSITORY_ROOT / "libs/servicekit")
        run(
            ["make", "--no-print-directory", "check-candidate-servicekit"],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=120,
            activity="verify Product candidate selection",
        )
        if args.phase == "image":
            candidate_image(context, environment)
        else:
            for target in PHASE_TARGETS[args.phase]:
                run(
                    ["make", "--no-print-directory", target],
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=2_100,
                    teardown_seconds=20,
                    activity=f"candidate Product {args.phase}: {target}",
                )
        context_request("end", args.phase)
    except (RuntimeError, ValueError, OSError, ProductCommandError) as error:
        print(f"shared-phase: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="shared-phase"))
