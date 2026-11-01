"""Tests for shared Go-service tooling configuration."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from scripts.go.service.tooling_config import ToolingConfigError, load_tooling_config


def configured_module(tmp_path: Path) -> Path:
    module = tmp_path / "services/example-service"
    module.mkdir(parents=True)
    (module / "Makefile").write_text("help:\n\t@true\n", encoding="utf-8")
    (module / "Dockerfile").write_text("FROM scratch\n", encoding="utf-8")
    (module / "cmd").mkdir()
    return module


def configuration(module_path: str = "services/example-service") -> dict[str, object]:
    return {
        "schemaVersion": 1,
        "service": "example",
        "modulePath": module_path,
        "image": {
            "name": "example-api",
            "title": "Example API",
            "localTagPrefix": "1.0.0-local.",
            "entrypoint": ["/example-service"],
            "user": "65532:65532",
            "platform": "linux/amd64",
            "lifecycle": None,
        },
        "candidate": {
            "copyPaths": ["cmd"],
            "excludePaths": [],
            "phaseTargets": {
                "generated": ["check-generated"],
                "breaking": ["check-breaking"],
                "integration": ["test-integration"],
            },
            "imageCheckTarget": "_image-check-existing",
        },
    }


def test_load_tooling_config_binds_owned_module(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(configuration()), encoding="utf-8")

    # when
    config = load_tooling_config(path, repository_root=tmp_path)

    # then
    assert config.module_root == module
    assert config.image.name == "example-api"
    assert config.image.local_tag_prefix == "1.0.0-local."
    assert config.candidate.phase_targets["integration"] == ("test-integration",)


@pytest.mark.parametrize(
    ("field", "value", "message"),
    (
        ("modulePath", "../outside", "modulePath must be relative"),
        ("service", "Example", "service name is invalid"),
    ),
)
def test_load_tooling_config_rejects_invalid_identity(
    tmp_path: Path, field: str, value: str, message: str
) -> None:
    # given
    module = configured_module(tmp_path)
    data = configuration()
    data[field] = value
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match=message):
        load_tooling_config(path, repository_root=tmp_path)


def test_load_tooling_config_rejects_copy_path_symlink_escape(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    outside = tmp_path / "outside"
    outside.mkdir()
    (module / "escaped").symlink_to(outside, target_is_directory=True)
    data = configuration()
    data["candidate"]["copyPaths"] = ["escaped"]  # type: ignore[index]
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match="escapes the module"):
        load_tooling_config(path, repository_root=tmp_path)
