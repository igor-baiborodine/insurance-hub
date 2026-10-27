"""Reject shared version drift between the source scaffold and Product without mutation."""

import argparse
import re
import sys
from pathlib import Path

SHARED_PINS = (
    "GO_VERSION",
    "BUF_VERSION",
    "PROTOC_GEN_GO_VERSION",
    "PROTOC_GEN_GO_GRPC_VERSION",
    "GOLANGCI_LINT_VERSION",
    "GOVULNCHECK_VERSION",
    "PROTOBUF_GO_VERSION",
    "GRPC_GO_VERSION",
    "PROTOVALIDATE_GO_VERSION",
    "PROTOVALIDATE_SCHEMA_GO_VERSION",
    "OTEL_VERSION",
    "OTEL_GRPC_VERSION",
    "X_NET_VERSION",
)


def module_versions(module: Path) -> dict[str, str]:
    content = (module / "go.mod").read_text(encoding="utf-8")
    versions = dict(re.findall(r"(?m)^\s*([^\s()]+)\s+(v[^\s]+)(?:\s+// indirect)?$", content))
    go = re.search(r"(?m)^go (\S+)$", content)
    if go is None:
        raise ValueError("go.mod lacks its Go version")
    versions["go"] = go[1]
    return versions


def check(scaffold: Path, reference: Path) -> None:
    pins = []
    for module in (scaffold, reference):
        values = dict(
            re.findall(
                r"(?m)^(\w+)\s*:=\s*(\S+)\s*$", (module / "Makefile").read_text(encoding="utf-8")
            )
        )
        missing = set(SHARED_PINS) - values.keys()
        if missing:
            raise ValueError("missing shared pins: " + ", ".join(sorted(missing)))
        pins.append(values)
    for name in SHARED_PINS:
        if pins[0][name] != pins[1][name]:
            raise ValueError(f"shared pin drift: {name}")
    scaffold_versions, reference_versions = module_versions(scaffold), module_versions(reference)
    for name in scaffold_versions.keys() & reference_versions.keys():
        if scaffold_versions[name] != reference_versions[name]:
            raise ValueError(f"shared dependency drift: {name}")
    if any(
        values["go"] != pins[index]["GO_VERSION"]
        for index, values in enumerate((scaffold_versions, reference_versions))
    ):
        raise ValueError("go.mod and Makefile Go versions differ")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scaffold", type=Path, required=True)
    parser.add_argument("--reference", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        check(args.scaffold, args.reference)
    except (OSError, UnicodeError, ValueError) as error:
        print(f"check-foundations: {error}", file=sys.stderr)
        return 1
    print("check-foundations: shared Go, tool and dependency versions match Product")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
