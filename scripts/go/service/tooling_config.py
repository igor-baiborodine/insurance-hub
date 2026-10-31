"""Validated configuration for shared Go-service repository tooling."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path, PurePosixPath

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
NAME = re.compile(r"[a-z][a-z0-9-]*")


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
class ServiceTooling:
    service: str
    module_path: str
    module_root: Path
    image: ImageConfig
    candidate: CandidateConfig


def require(condition: object, message: str) -> None:
    if not condition:
        raise ToolingConfigError(message)


def relative_path(value: object, field: str) -> str:
    require(isinstance(value, str) and bool(value), f"{field} must be a non-empty string")
    path = PurePosixPath(value)
    require(not path.is_absolute() and ".." not in path.parts, f"{field} must be relative")
    return value


def string_list(value: object, field: str) -> tuple[str, ...]:
    require(isinstance(value, list) and bool(value), f"{field} must be a non-empty list")
    result = tuple(relative_path(item, field) for item in value)
    require(len(result) == len(set(result)), f"{field} contains duplicates")
    return result


def load_tooling_config(path: Path, *, repository_root: Path = REPOSITORY_ROOT) -> ServiceTooling:
    """Load one service configuration and bind it to its declared module directory."""
    try:
        root = repository_root.resolve(strict=True)
        config_path = path.resolve(strict=True)
        value = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ToolingConfigError("service tooling configuration is unreadable") from error
    require(config_path.is_relative_to(root), "service tooling configuration escapes the checkout")
    require(isinstance(value, dict) and value.get("schemaVersion") == 1, "unsupported schema")

    service = value.get("service")
    require(isinstance(service, str) and NAME.fullmatch(service), "service name is invalid")
    module_path = relative_path(value.get("modulePath"), "modulePath")
    try:
        module_root = (root / module_path).resolve(strict=True)
    except OSError as error:
        raise ToolingConfigError("declared module path is missing") from error
    require(module_root.is_relative_to(root), "module path escapes the checkout")
    require(config_path.parent == module_root, "configuration must be owned by its module root")
    require((module_root / "Makefile").is_file(), "declared module does not own a Makefile")
    require((module_root / "Dockerfile").is_file(), "declared module does not own a Dockerfile")

    image = value.get("image")
    require(isinstance(image, dict), "image configuration is missing")
    name = image.get("name")
    title = image.get("title")
    user = image.get("user")
    platform = image.get("platform")
    entrypoint = image.get("entrypoint")
    require(isinstance(name, str) and bool(name), "image.name is required")
    require(isinstance(title, str) and bool(title), "image.title is required")
    require(isinstance(user, str) and bool(user), "image.user is required")
    require(platform == "linux/amd64", "image.platform must be linux/amd64")
    require(
        isinstance(entrypoint, list)
        and bool(entrypoint)
        and all(isinstance(item, str) and item.startswith("/") for item in entrypoint),
        "image.entrypoint is invalid",
    )

    lifecycle_value = image.get("lifecycle")
    lifecycle = None
    if lifecycle_value is not None:
        require(isinstance(lifecycle_value, dict), "image.lifecycle is invalid")
        port = lifecycle_value.get("containerPort")
        health_path = lifecycle_value.get("path")
        expected_body = lifecycle_value.get("expectedBody")
        environment = lifecycle_value.get("environment")
        stop_seconds = lifecycle_value.get("stopSeconds")
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

    candidate = value.get("candidate")
    require(isinstance(candidate, dict), "candidate configuration is missing")
    copy_paths = string_list(candidate.get("copyPaths"), "candidate.copyPaths")
    exclude_value = candidate.get("excludePaths", [])
    require(isinstance(exclude_value, list), "candidate.excludePaths must be a list")
    exclude_paths = tuple(relative_path(item, "candidate.excludePaths") for item in exclude_value)
    require(len(exclude_paths) == len(set(exclude_paths)), "candidate.excludePaths has duplicates")
    targets_value = candidate.get("phaseTargets")
    require(isinstance(targets_value, dict), "candidate.phaseTargets is missing")
    require(
        set(targets_value) == {"generated", "breaking", "integration"},
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
    image_check_target = candidate.get("imageCheckTarget")
    require(
        isinstance(image_check_target, str)
        and bool(re.fullmatch(r"_[a-z][a-z0-9-]*", image_check_target)),
        "candidate.imageCheckTarget is invalid",
    )
    for copy_path in copy_paths:
        require((module_root / copy_path).is_dir(), f"candidate copy path is missing: {copy_path}")

    return ServiceTooling(
        service=service,
        module_path=module_path,
        module_root=module_root,
        image=ImageConfig(
            name=name,
            title=title,
            entrypoint=tuple(entrypoint),
            user=user,
            platform=platform,
            lifecycle=lifecycle,
        ),
        candidate=CandidateConfig(
            copy_paths=copy_paths,
            exclude_paths=exclude_paths,
            phase_targets=phase_targets,
            image_check_target=image_check_target,
        ),
    )
