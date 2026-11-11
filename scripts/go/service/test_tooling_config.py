"""Tests for shared Go-service tooling configuration."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from scripts.go.service.tooling_config import (
    ToolingConfigError,
    load_tooling_config,
    validate_catalog_identity,
)


def configured_module(tmp_path: Path) -> Path:
    module = tmp_path / "services/example-service"
    module.mkdir(parents=True)
    (module / "Makefile").write_text("help:\n\t@true\n", encoding="utf-8")
    (module / "Dockerfile").write_text("FROM scratch\n", encoding="utf-8")
    (module / "cmd/server").mkdir(parents=True)
    (module / "gen").mkdir()
    (module / "scripts").mkdir()
    (module / "scripts/test_tooling.py").write_text("", encoding="utf-8")
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


def version_two_configuration(module: Path) -> dict[str, object]:
    (module / "generated.json").write_text('{"paths": {}}\n', encoding="utf-8")
    (module / "contract.json").write_text('{"paths": {}}\n', encoding="utf-8")
    value = configuration()
    value["schemaVersion"] = 2
    del value["image"]["localTagPrefix"]  # type: ignore[index]
    value["dockerfile"] = {
        "sourceCopies": [{"source": "cmd", "destination": "cmd"}],
        "buildOutputs": [
            {
                "package": "./cmd/server",
                "output": "/out/example-service",
                "destination": "/example-service",
                "mode": "0555",
            }
        ],
        "runtimeCopies": [],
        "ports": [8080, 8081, 9090],
    }
    value["contracts"] = {
        "runner": {
            "target": "test-tooling",
            "suitePath": "scripts/test_tooling.py",
            "timeoutSeconds": 300,
        },
        "gateway": {
            "generatedPath": "generated.json",
            "contractPath": "contract.json",
        },
        "dependencies": [
            {
                "name": "search",
                "kind": "elasticsearch",
                "image": f"example.org/search@sha256:{'a' * 64}",
                "ports": [9200],
            }
        ],
        "deployment": {"portForwardSeconds": 30},
        "administration": {
            "transports": ["https", "kubectl", "stdin"],
            "secretEnvironment": ["ADMIN_PASSWORD"],
        },
    }
    return value


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
    assert config.candidate.phase_targets["integration"] == ("test-integration",)
    assert config.contracts is None


def test_load_version_two_contracts(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(version_two_configuration(module)), encoding="utf-8")

    # when
    config = load_tooling_config(path, repository_root=tmp_path)

    # then
    assert config.schema_version == 2
    assert config.contracts is not None
    assert config.contracts.runner.target == "test-tooling"
    assert config.contracts.runner.suite_path == "scripts/test_tooling.py"
    assert config.dockerfile is not None
    assert config.dockerfile.build_outputs[0].destination == "/example-service"
    assert config.contracts.dependencies[0].ports == (9200,)
    assert config.contracts.administration.secret_environment == ("ADMIN_PASSWORD",)


def test_load_version_two_rejects_legacy_image_tag_prefix(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    data = version_two_configuration(module)
    data["image"]["localTagPrefix"] = "1.0.0-local."  # type: ignore[index]
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match="unknown keys: localTagPrefix"):
        load_tooling_config(path, repository_root=tmp_path)


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


def test_load_tooling_config_rejects_unknown_instruction(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    data = version_two_configuration(module)
    data["contracts"]["runner"]["command"] = ["sh", "-c", "arbitrary"]  # type: ignore[index]
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match="unknown keys: command"):
        load_tooling_config(path, repository_root=tmp_path)


@pytest.mark.parametrize(
    ("mutate", "message"),
    (
        (
            lambda data: data["dockerfile"]["sourceCopies"][0].update({"source": "../outside"}),
            "must be relative",
        ),
        (
            lambda data: data["dockerfile"]["buildOutputs"][0].update({"mode": "u+s"}),
            "mode is invalid",
        ),
        (
            lambda data: data["dockerfile"].update({"ports": [8080, 8080]}),
            "ports contains duplicates",
        ),
        (
            lambda data: data["dockerfile"].update({"buildOutputs": []}),
            "buildOutputs must be a non-empty list",
        ),
        (
            lambda data: data["dockerfile"].update({"RUN": "arbitrary"}),
            "unknown keys: RUN",
        ),
    ),
)
def test_load_tooling_config_rejects_unsafe_dockerfile_data(
    tmp_path: Path, mutate: object, message: str
) -> None:
    # given
    module = configured_module(tmp_path)
    data = version_two_configuration(module)
    mutate(data)  # type: ignore[operator]
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match=message):
        load_tooling_config(path, repository_root=tmp_path)


def test_load_tooling_config_rejects_duplicate_dockerfile_destination(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    data = version_two_configuration(module)
    data["dockerfile"]["sourceCopies"].append(  # type: ignore[index,union-attr]
        {"source": "gen", "destination": "cmd"}
    )
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match="duplicate destinations"):
        load_tooling_config(path, repository_root=tmp_path)


def test_load_tooling_config_rejects_gateway_symlink_escape(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    outside = tmp_path / "outside.json"
    outside.write_text("{}\n", encoding="utf-8")
    data = version_two_configuration(module)
    (module / "escaped.json").symlink_to(outside)
    data["contracts"]["gateway"]["generatedPath"] = "escaped.json"  # type: ignore[index]
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match="escapes the module"):
        load_tooling_config(path, repository_root=tmp_path)


def test_load_tooling_config_rejects_unpinned_dependency(tmp_path: Path) -> None:
    # given
    module = configured_module(tmp_path)
    data = version_two_configuration(module)
    data["contracts"]["dependencies"][0]["image"] = "example.org/search:latest"  # type: ignore[index]
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(data), encoding="utf-8")

    # when / then
    with pytest.raises(ToolingConfigError, match="image is not pinned"):
        load_tooling_config(path, repository_root=tmp_path)


@pytest.mark.parametrize(
    ("service", "record", "message"),
    (
        (
            "other",
            {"modulePath": "services/example-service", "localImage": "example-api"},
            "service identity conflicts",
        ),
        (
            "example",
            {"modulePath": "services/other", "localImage": "example-api"},
            "modulePath conflicts",
        ),
        (
            "example",
            {"modulePath": "services/example-service", "localImage": "other-api"},
            "localImage conflicts",
        ),
    ),
)
def test_catalog_identity_conflict_fails_before_dispatch(
    tmp_path: Path, service: str, record: dict[str, object], message: str
) -> None:
    # given
    module = configured_module(tmp_path)
    path = module / "go-service-tooling.json"
    path.write_text(json.dumps(configuration()), encoding="utf-8")
    config = load_tooling_config(path, repository_root=tmp_path)

    # when / then
    with pytest.raises(ToolingConfigError, match=message):
        validate_catalog_identity(config, service, record)
