"""Validated configuration for shared Go-service repository tooling."""

from __future__ import annotations

import json
import re
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path, PurePosixPath

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
NAME = re.compile(r"[a-z][a-z0-9-]*")
ENVIRONMENT_NAME = re.compile(r"[A-Z][A-Z0-9_]*")
PINNED_IMAGE = re.compile(r"[^@\s]+@sha256:[0-9a-f]{64}")
SUPPORTED_TRANSPORTS = frozenset({"https", "kubectl", "stdin"})
SAFE_PATH_PART = re.compile(r"[A-Za-z0-9._-]+")
FILE_MODE = re.compile(r"0[0-7]{3}")


class ToolingConfigError(ValueError):
    """A service tooling configuration is missing or unsafe."""


@dataclass(frozen=True)
class LifecycleConfig:
    container_port: int
    path: str
    expected_body: str
    environment: dict[str, str]
    stop_seconds: int


@dataclass(frozen=True)
class ImageConfig:
    name: str
    title: str
    entrypoint: tuple[str, ...]
    user: str
    platform: str
    lifecycle: LifecycleConfig | None


@dataclass(frozen=True)
class CandidateConfig:
    copy_paths: tuple[str, ...]
    exclude_paths: tuple[str, ...]
    phase_targets: dict[str, tuple[str, ...]]
    image_check_target: str


@dataclass(frozen=True)
class SourceCopy:
    source: str
    destination: str


@dataclass(frozen=True)
class BuildOutput:
    package: str
    output: str
    destination: str
    mode: str


@dataclass(frozen=True)
class RuntimeCopy:
    source: str
    destination: str
    mode: str | None


@dataclass(frozen=True)
class DockerfileConfig:
    source_copies: tuple[SourceCopy, ...]
    build_outputs: tuple[BuildOutput, ...]
    runtime_copies: tuple[RuntimeCopy, ...]
    ports: tuple[int, ...]


@dataclass(frozen=True)
class RunnerConfig:
    target: str
    suite_path: str
    timeout_seconds: int


@dataclass(frozen=True)
class GatewayConfig:
    generated_path: str
    contract_path: str


@dataclass(frozen=True)
class DependencyConfig:
    name: str
    kind: str
    image: str
    ports: tuple[int, ...]


@dataclass(frozen=True)
class DeploymentConfig:
    port_forward_seconds: int


@dataclass(frozen=True)
class AdministrationConfig:
    transports: tuple[str, ...]
    secret_environment: tuple[str, ...]


@dataclass(frozen=True)
class ToolingContracts:
    runner: RunnerConfig
    gateway: GatewayConfig
    dependencies: tuple[DependencyConfig, ...]
    deployment: DeploymentConfig
    administration: AdministrationConfig


@dataclass(frozen=True)
class ServiceTooling:
    schema_version: int
    service: str
    module_path: str
    module_root: Path
    image: ImageConfig
    candidate: CandidateConfig
    dockerfile: DockerfileConfig | None
    contracts: ToolingContracts | None


def require(condition: object, message: str) -> None:
    if not condition:
        raise ToolingConfigError(message)


def require_keys(value: Mapping[str, object], expected: set[str], field: str) -> None:
    missing = sorted(expected - set(value))
    unknown = sorted(set(value) - expected)
    require(not missing, f"{field} is missing keys: {', '.join(missing)}")
    require(not unknown, f"{field} has unknown keys: {', '.join(unknown)}")


def relative_path(value: object, field: str) -> str:
    require(isinstance(value, str) and bool(value), f"{field} must be a non-empty string")
    path = PurePosixPath(value)
    require(not path.is_absolute() and ".." not in path.parts, f"{field} must be relative")
    return value


def docker_relative_path(value: object, field: str) -> str:
    relative = relative_path(value, field)
    require(relative not in {".", "./"}, f"{field} must name a path")
    require(
        all(SAFE_PATH_PART.fullmatch(part) for part in PurePosixPath(relative).parts),
        f"{field} contains unsupported characters",
    )
    return relative


def docker_absolute_path(value: object, field: str) -> str:
    require(isinstance(value, str) and bool(value), f"{field} must be a non-empty string")
    path = PurePosixPath(value)
    require(path.is_absolute() and ".." not in path.parts, f"{field} must be absolute")
    require(
        all(part == "/" or SAFE_PATH_PART.fullmatch(part) for part in path.parts),
        f"{field} contains unsupported characters",
    )
    return value


def module_file(module_root: Path, value: object, field: str) -> str:
    relative = relative_path(value, field)
    try:
        resolved = (module_root / relative).resolve(strict=True)
    except OSError as error:
        raise ToolingConfigError(f"{field} is missing") from error
    require(
        resolved.is_relative_to(module_root) and resolved.is_file(),
        f"{field} escapes the module",
    )
    return relative


def string_list(value: object, field: str, *, allow_empty: bool = False) -> tuple[str, ...]:
    require(
        isinstance(value, list) and (allow_empty or bool(value)),
        f"{field} must be {'a' if allow_empty else 'a non-empty'} list",
    )
    result = tuple(relative_path(item, field) for item in value)
    require(len(result) == len(set(result)), f"{field} contains duplicates")
    return result


def _load_image(value: object, schema_version: int) -> ImageConfig:
    require(isinstance(value, dict), "image configuration is missing")
    expected_keys = {"name", "title", "entrypoint", "user", "platform", "lifecycle"}
    if schema_version == 1:
        expected_keys.add("localTagPrefix")
    require_keys(value, expected_keys, "image")
    name = value["name"]
    title = value["title"]
    user = value["user"]
    platform = value["platform"]
    entrypoint = value["entrypoint"]
    require(
        isinstance(name, str) and bool(re.fullmatch(r"[a-z0-9][a-z0-9.-]*", name)),
        "image.name is invalid",
    )
    require(
        isinstance(title, str)
        and bool(title)
        and all(character.isprintable() for character in title),
        "image.title is invalid",
    )
    if schema_version == 1:
        local_tag_prefix = value["localTagPrefix"]
        require(
            isinstance(local_tag_prefix, str)
            and bool(re.fullmatch(r"[a-z0-9][a-z0-9.-]*\.", local_tag_prefix)),
            "image.localTagPrefix is invalid",
        )
    require(
        isinstance(user, str) and bool(re.fullmatch(r"[0-9]+:[0-9]+", user)),
        "image.user is invalid",
    )
    require(platform == "linux/amd64", "image.platform must be linux/amd64")
    require(
        isinstance(entrypoint, list)
        and bool(entrypoint)
        and all(
            isinstance(item, str)
            and item.startswith("/")
            and all(character.isprintable() and not character.isspace() for character in item)
            for item in entrypoint
        ),
        "image.entrypoint is invalid",
    )

    lifecycle_value = value["lifecycle"]
    lifecycle = None
    if lifecycle_value is not None:
        require(isinstance(lifecycle_value, dict), "image.lifecycle is invalid")
        require_keys(
            lifecycle_value,
            {"containerPort", "path", "expectedBody", "environment", "stopSeconds"},
            "image.lifecycle",
        )
        port = lifecycle_value["containerPort"]
        health_path = lifecycle_value["path"]
        expected_body = lifecycle_value["expectedBody"]
        environment = lifecycle_value["environment"]
        stop_seconds = lifecycle_value["stopSeconds"]
        require(isinstance(port, int) and 1 <= port <= 65535, "lifecycle port is invalid")
        require(
            isinstance(health_path, str) and health_path.startswith("/"),
            "lifecycle path is invalid",
        )
        require(isinstance(expected_body, str), "lifecycle expected body is invalid")
        require(
            isinstance(environment, dict)
            and all(
                isinstance(key, str) and bool(key) and isinstance(item, str)
                for key, item in environment.items()
            ),
            "lifecycle environment is invalid",
        )
        require(
            isinstance(stop_seconds, int) and stop_seconds > 0,
            "lifecycle stopSeconds is invalid",
        )
        lifecycle = LifecycleConfig(
            container_port=port,
            path=health_path,
            expected_body=expected_body,
            environment=dict(environment),
            stop_seconds=stop_seconds,
        )
    return ImageConfig(
        name=name,
        title=title,
        entrypoint=tuple(entrypoint),
        user=user,
        platform=platform,
        lifecycle=lifecycle,
    )


def _load_candidate(value: object, module_root: Path) -> CandidateConfig:
    require(isinstance(value, dict), "candidate configuration is missing")
    require_keys(
        value,
        {"copyPaths", "excludePaths", "phaseTargets", "imageCheckTarget"},
        "candidate",
    )
    copy_paths = string_list(value["copyPaths"], "candidate.copyPaths")
    exclude_paths = string_list(value["excludePaths"], "candidate.excludePaths", allow_empty=True)
    targets_value = value["phaseTargets"]
    require(isinstance(targets_value, dict), "candidate.phaseTargets is missing")
    phases = set(targets_value)
    require(
        {"generated", "breaking", "integration"}.issubset(phases)
        and phases.issubset({"generated", "breaking", "integration", "copy"}),
        "candidate.phaseTargets has unsupported phases",
    )
    phase_targets: dict[str, tuple[str, ...]] = {}
    for phase, targets in targets_value.items():
        require(
            isinstance(targets, list)
            and bool(targets)
            and all(isinstance(target, str) and NAME.fullmatch(target) for target in targets),
            f"candidate phase {phase} targets are invalid",
        )
        phase_targets[phase] = tuple(targets)
    image_check_target = value["imageCheckTarget"]
    require(
        isinstance(image_check_target, str)
        and bool(re.fullmatch(r"_[a-z][a-z0-9-]*", image_check_target)),
        "candidate.imageCheckTarget is invalid",
    )
    for copy_path in copy_paths:
        try:
            resolved_copy_path = (module_root / copy_path).resolve(strict=True)
        except OSError as error:
            raise ToolingConfigError(f"candidate copy path is missing: {copy_path}") from error
        require(
            resolved_copy_path.is_relative_to(module_root) and resolved_copy_path.is_dir(),
            f"candidate copy path escapes the module: {copy_path}",
        )
    return CandidateConfig(
        copy_paths=copy_paths,
        exclude_paths=exclude_paths,
        phase_targets=phase_targets,
        image_check_target=image_check_target,
    )


def _load_dockerfile(value: object, module_root: Path, image: ImageConfig) -> DockerfileConfig:
    require(isinstance(value, dict), "dockerfile configuration is missing")
    require_keys(
        value,
        {"sourceCopies", "buildOutputs", "runtimeCopies", "ports"},
        "dockerfile",
    )

    source_values = value["sourceCopies"]
    require(
        isinstance(source_values, list) and bool(source_values),
        "dockerfile.sourceCopies must be a non-empty list",
    )
    source_copies: list[SourceCopy] = []
    for index, item in enumerate(source_values):
        field = f"dockerfile.sourceCopies[{index}]"
        require(isinstance(item, dict), f"{field} is invalid")
        require_keys(item, {"source", "destination"}, field)
        source = docker_relative_path(item["source"], f"{field}.source")
        destination = docker_relative_path(item["destination"], f"{field}.destination")
        try:
            resolved = (module_root / source).resolve(strict=True)
        except OSError as error:
            raise ToolingConfigError(f"{field}.source is missing") from error
        require(
            resolved.is_relative_to(module_root) and resolved.is_dir(),
            f"{field}.source escapes the module",
        )
        source_copies.append(SourceCopy(source=source, destination=destination))
    require(
        len({item.source for item in source_copies}) == len(source_copies),
        "dockerfile.sourceCopies contains duplicate sources",
    )
    require(
        len({item.destination for item in source_copies}) == len(source_copies),
        "dockerfile.sourceCopies contains duplicate destinations",
    )

    output_values = value["buildOutputs"]
    require(
        isinstance(output_values, list) and bool(output_values),
        "dockerfile.buildOutputs must be a non-empty list",
    )
    build_outputs: list[BuildOutput] = []
    for index, item in enumerate(output_values):
        field = f"dockerfile.buildOutputs[{index}]"
        require(isinstance(item, dict), f"{field} is invalid")
        require_keys(item, {"package", "output", "destination", "mode"}, field)
        package = item["package"]
        require(
            isinstance(package, str)
            and package.startswith("./")
            and docker_relative_path(package[2:], f"{field}.package"),
            f"{field}.package is invalid",
        )
        package_path = module_root / package[2:]
        require(package_path.is_dir(), f"{field}.package is missing")
        output = docker_absolute_path(item["output"], f"{field}.output")
        require(output.startswith("/out/"), f"{field}.output must be under /out")
        destination = docker_absolute_path(item["destination"], f"{field}.destination")
        mode = item["mode"]
        require(isinstance(mode, str) and FILE_MODE.fullmatch(mode), f"{field}.mode is invalid")
        build_outputs.append(
            BuildOutput(
                package=package,
                output=output,
                destination=destination,
                mode=mode,
            )
        )
    require(
        len({item.output for item in build_outputs}) == len(build_outputs),
        "dockerfile.buildOutputs contains duplicate outputs",
    )

    runtime_values = value["runtimeCopies"]
    require(isinstance(runtime_values, list), "dockerfile.runtimeCopies must be a list")
    runtime_copies: list[RuntimeCopy] = []
    for index, item in enumerate(runtime_values):
        field = f"dockerfile.runtimeCopies[{index}]"
        require(isinstance(item, dict), f"{field} is invalid")
        require_keys(item, {"source", "destination", "mode"}, field)
        source = docker_absolute_path(item["source"], f"{field}.source")
        require(source.startswith("/out/"), f"{field}.source must be under /out")
        require(
            source in {output.output for output in build_outputs},
            f"{field}.source is not a configured build output",
        )
        destination = docker_absolute_path(item["destination"], f"{field}.destination")
        mode = item["mode"]
        require(
            mode is None or (isinstance(mode, str) and FILE_MODE.fullmatch(mode)),
            f"{field}.mode is invalid",
        )
        runtime_copies.append(RuntimeCopy(source=source, destination=destination, mode=mode))
    destinations = [item.destination for item in build_outputs] + [
        item.destination for item in runtime_copies
    ]
    require(
        len(destinations) == len(set(destinations)),
        "dockerfile runtime destinations contain duplicates",
    )

    ports = value["ports"]
    require(
        isinstance(ports, list)
        and bool(ports)
        and all(type(port) is int and 1 <= port <= 65535 for port in ports),
        "dockerfile.ports is invalid",
    )
    require(len(ports) == len(set(ports)), "dockerfile.ports contains duplicates")
    require(
        image.entrypoint[0] in destinations,
        "image entrypoint is not a configured runtime destination",
    )
    return DockerfileConfig(
        source_copies=tuple(source_copies),
        build_outputs=tuple(build_outputs),
        runtime_copies=tuple(runtime_copies),
        ports=tuple(ports),
    )


def _load_contracts(value: object, module_root: Path) -> ToolingContracts:
    require(isinstance(value, dict), "contracts configuration is missing")
    require_keys(
        value,
        {"runner", "gateway", "dependencies", "deployment", "administration"},
        "contracts",
    )
    runner = value["runner"]
    require(isinstance(runner, dict), "contracts.runner is invalid")
    require_keys(runner, {"target", "suitePath", "timeoutSeconds"}, "contracts.runner")
    target = runner["target"]
    suite_path = module_file(module_root, runner["suitePath"], "contracts.runner.suitePath")
    timeout_seconds = runner["timeoutSeconds"]
    require(
        isinstance(target, str) and NAME.fullmatch(target), "contracts.runner.target is invalid"
    )
    require(
        isinstance(timeout_seconds, int) and 1 <= timeout_seconds <= 3600,
        "contracts.runner.timeoutSeconds is invalid",
    )

    gateway = value["gateway"]
    require(isinstance(gateway, dict), "contracts.gateway is invalid")
    require_keys(gateway, {"generatedPath", "contractPath"}, "contracts.gateway")
    generated_path = module_file(
        module_root, gateway["generatedPath"], "contracts.gateway.generatedPath"
    )
    contract_path = module_file(
        module_root, gateway["contractPath"], "contracts.gateway.contractPath"
    )

    dependencies_value = value["dependencies"]
    require(isinstance(dependencies_value, list), "contracts.dependencies must be a list")
    dependencies: list[DependencyConfig] = []
    for index, dependency in enumerate(dependencies_value):
        field = f"contracts.dependencies[{index}]"
        require(isinstance(dependency, dict), f"{field} is invalid")
        require_keys(dependency, {"name", "kind", "image", "ports"}, field)
        name = dependency["name"]
        kind = dependency["kind"]
        image = dependency["image"]
        ports = dependency["ports"]
        require(isinstance(name, str) and NAME.fullmatch(name), f"{field}.name is invalid")
        require(isinstance(kind, str) and NAME.fullmatch(kind), f"{field}.kind is invalid")
        require(
            isinstance(image, str) and PINNED_IMAGE.fullmatch(image),
            f"{field}.image is not pinned",
        )
        require(
            isinstance(ports, list)
            and bool(ports)
            and all(isinstance(port, int) and 1 <= port <= 65535 for port in ports),
            f"{field}.ports is invalid",
        )
        require(len(ports) == len(set(ports)), f"{field}.ports contains duplicates")
        dependencies.append(DependencyConfig(name=name, kind=kind, image=image, ports=tuple(ports)))
    require(
        len({dependency.name for dependency in dependencies}) == len(dependencies),
        "contracts.dependencies contains duplicate names",
    )

    deployment = value["deployment"]
    require(isinstance(deployment, dict), "contracts.deployment is invalid")
    require_keys(deployment, {"portForwardSeconds"}, "contracts.deployment")
    port_forward_seconds = deployment["portForwardSeconds"]
    require(
        isinstance(port_forward_seconds, int) and 1 <= port_forward_seconds <= 300,
        "contracts.deployment.portForwardSeconds is invalid",
    )

    administration = value["administration"]
    require(isinstance(administration, dict), "contracts.administration is invalid")
    require_keys(
        administration,
        {"transports", "secretEnvironment"},
        "contracts.administration",
    )
    transports = administration["transports"]
    secret_environment = administration["secretEnvironment"]
    require(
        isinstance(transports, list)
        and all(isinstance(item, str) and item in SUPPORTED_TRANSPORTS for item in transports),
        "contracts.administration.transports is invalid",
    )
    require(
        len(transports) == len(set(transports)),
        "contracts.administration.transports has duplicates",
    )
    require(
        isinstance(secret_environment, list)
        and all(
            isinstance(item, str) and ENVIRONMENT_NAME.fullmatch(item)
            for item in secret_environment
        ),
        "contracts.administration.secretEnvironment is invalid",
    )
    require(
        len(secret_environment) == len(set(secret_environment)),
        "contracts.administration.secretEnvironment has duplicates",
    )
    return ToolingContracts(
        runner=RunnerConfig(
            target=target,
            suite_path=suite_path,
            timeout_seconds=timeout_seconds,
        ),
        gateway=GatewayConfig(generated_path=generated_path, contract_path=contract_path),
        dependencies=tuple(dependencies),
        deployment=DeploymentConfig(port_forward_seconds=port_forward_seconds),
        administration=AdministrationConfig(
            transports=tuple(transports),
            secret_environment=tuple(secret_environment),
        ),
    )


def load_tooling_config(path: Path, *, repository_root: Path = REPOSITORY_ROOT) -> ServiceTooling:
    """Load one service configuration and bind it to its declared module directory."""
    try:
        root = repository_root.resolve(strict=True)
        config_path = path.resolve(strict=True)
        value = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ToolingConfigError("service tooling configuration is unreadable") from error
    require(config_path.is_relative_to(root), "service tooling configuration escapes the checkout")
    require(isinstance(value, dict), "service tooling configuration must be an object")
    schema_version = value.get("schemaVersion")
    require(schema_version in {1, 2}, "unsupported schema")
    expected_keys = {"schemaVersion", "service", "modulePath", "image", "candidate"}
    if schema_version == 2:
        expected_keys.update({"contracts", "dockerfile"})
    require_keys(value, expected_keys, "configuration")

    service = value["service"]
    require(isinstance(service, str) and NAME.fullmatch(service), "service name is invalid")
    module_path = relative_path(value["modulePath"], "modulePath")
    try:
        module_root = (root / module_path).resolve(strict=True)
    except OSError as error:
        raise ToolingConfigError("declared module path is missing") from error
    require(module_root.is_relative_to(root), "module path escapes the checkout")
    require(config_path.parent == module_root, "configuration must be owned by its module root")
    require((module_root / "Makefile").is_file(), "declared module does not own a Makefile")
    if schema_version == 1:
        require((module_root / "Dockerfile").is_file(), "declared module does not own a Dockerfile")

    image = _load_image(value["image"], schema_version)
    candidate = _load_candidate(value["candidate"], module_root)
    dockerfile = (
        _load_dockerfile(value["dockerfile"], module_root, image) if schema_version == 2 else None
    )
    contracts = _load_contracts(value["contracts"], module_root) if schema_version == 2 else None
    return ServiceTooling(
        schema_version=schema_version,
        service=service,
        module_path=module_path,
        module_root=module_root,
        image=image,
        candidate=candidate,
        dockerfile=dockerfile,
        contracts=contracts,
    )


def validate_catalog_identity(
    config: ServiceTooling, service: str, catalog_record: Mapping[str, object]
) -> None:
    """Reject duplicated catalog facts that disagree with validated module configuration."""
    require(
        service == config.service, "catalog service identity conflicts with tooling configuration"
    )
    require(
        catalog_record.get("modulePath") == config.module_path,
        "catalog modulePath conflicts with tooling configuration",
    )
    require(
        catalog_record.get("localImage") == config.image.name,
        "catalog localImage conflicts with tooling configuration",
    )
