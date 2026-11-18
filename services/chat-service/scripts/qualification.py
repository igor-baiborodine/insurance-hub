#!/usr/bin/env python3
"""Guard and run loopback-only Chat real-client qualification through Nginx."""

from __future__ import annotations

import argparse
import json
import os
import re
import stat
import sys
import tempfile
import time
import urllib.error
import urllib.request
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from scripts.go.service.tooling_contracts import (
    AdministrationError,
    OwnedPortForward,
    PortForwardSpec,
    loopback_port,
)
from scripts.go.service.tooling_process import run, run_cli
from scripts.go.service.tooling_resources import OwnedProcess, ResourceError
from scripts.python.process_runner import CommandError, child_environment

ROOT = Path(__file__).resolve().parents[3]
SOURCE_REVISION = re.compile(r"[0-9a-f]{40}")
IMAGE_DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
NGINX_IMAGE = "insurance-hub-web-vue-legacy:latest"

TARGETS = {
    "local-dev": {
        "context": "kind-local-dev-insurance-hub",
        "namespace": "local-dev-all",
    },
    "qa": {
        "context": "qa-insurance-hub",
        "namespace": "qa-svc",
    },
}


class QualificationError(RuntimeError):
    """The private qualification environment violated its bounded contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise QualificationError(message)


@dataclass(frozen=True)
class Target:
    environment: str
    context: str
    namespace: str
    service: str
    source_revision: str
    image_digest: str

    @property
    def version(self) -> str:
        return f"git-{self.source_revision[:12]}"

    @property
    def deployment(self) -> str:
        return f"{self.environment}-chat-api"

    @property
    def java_service(self) -> str:
        return f"{self.environment}-chat-api-legacy"

    @classmethod
    def validated(cls, arguments: argparse.Namespace) -> Target:
        expected = TARGETS.get(arguments.environment)
        require(expected is not None, "qualification environment is unsupported")
        assert expected is not None
        require(arguments.context == expected["context"], "qualification context differs")
        require(arguments.namespace == expected["namespace"], "qualification namespace differs")
        require(
            arguments.service == f"{arguments.environment}-chat-api",
            "qualification service differs",
        )
        require(
            bool(SOURCE_REVISION.fullmatch(arguments.source_revision))
            and arguments.source_revision != "0" * 40,
            "qualification source revision is invalid",
        )
        require(
            bool(IMAGE_DIGEST.fullmatch(arguments.image_digest))
            and arguments.image_digest != f"sha256:{'0' * 64}",
            "qualification image digest is invalid",
        )
        return cls(
            environment=arguments.environment,
            context=arguments.context,
            namespace=arguments.namespace,
            service=arguments.service,
            source_revision=arguments.source_revision,
            image_digest=arguments.image_digest,
        )


JsonGetter = Callable[[Target, str, str, str], dict[str, Any]]


def kubectl_json(target: Target, kind: str, name: str, selector: str = "") -> dict[str, Any]:
    arguments = [
        os.environ.get("KUBECTL", "kubectl"),
        "--context",
        target.context,
        "--namespace",
        target.namespace,
        "get",
        kind,
    ]
    if name:
        arguments.append(name)
    if selector:
        arguments.extend(("--selector", selector))
    arguments.extend(("--output", "json"))
    output = run(
        arguments,
        cwd=ROOT,
        env=child_environment(preferred_executables=(arguments[0],)),
        timeout_seconds=30,
        activity=f"inspect Chat qualification {kind}",
        capture=True,
    )
    try:
        value = json.loads(output)
    except json.JSONDecodeError as error:
        raise QualificationError("qualification target returned invalid JSON") from error
    require(isinstance(value, dict), "qualification target JSON is not an object")
    return value


def nested(value: Mapping[str, Any], *path: str) -> Any:
    current: Any = value
    for part in path:
        require(isinstance(current, Mapping) and part in current, "target identity is incomplete")
        current = current[part]
    return current


def config_map_name(deployment: Mapping[str, Any]) -> str:
    containers = nested(deployment, "spec", "template", "spec", "containers")
    require(isinstance(containers, list) and len(containers) == 1, "target container is ambiguous")
    env_from = containers[0].get("envFrom", [])
    names = [
        item.get("configMapRef", {}).get("name") for item in env_from if isinstance(item, Mapping)
    ]
    require(len(names) == 1 and isinstance(names[0], str), "target ConfigMap is ambiguous")
    return names[0]


def validate_live_identity(target: Target, get_json: JsonGetter = kubectl_json) -> dict[str, str]:
    deployment = get_json(target, "deployment", target.deployment, "")
    require(nested(deployment, "spec", "replicas") == 1, "Chat replica count differs")
    require(
        nested(deployment, "spec", "strategy", "type") == "Recreate",
        "Chat replacement strategy differs",
    )
    require(
        nested(deployment, "spec", "template", "metadata", "labels").get(
            "app.kubernetes.io/version"
        )
        == target.version,
        "Chat workload version differs",
    )
    configuration_name = config_map_name(deployment)
    configuration = get_json(target, "configmap", configuration_name, "")
    require(
        nested(configuration, "data").get("SERVICE_VERSION") == target.version,
        "Chat runtime version differs",
    )

    pods = get_json(target, "pods", "", "app.kubernetes.io/name=chat-api")
    items = pods.get("items")
    require(isinstance(items, list) and len(items) == 1, "ready Chat Pod inventory differs")
    statuses = nested(items[0], "status", "containerStatuses")
    require(isinstance(statuses, list) and len(statuses) == 1, "Chat container status differs")
    status = statuses[0]
    require(status.get("ready") is True, "Chat container is not ready")
    image_id = status.get("imageID")
    require(
        isinstance(image_id, str) and image_id.endswith(f"@{target.image_digest}"),
        "Chat Pod image digest differs",
    )

    vue_deployment_name = f"{target.environment}-web-vue-legacy"
    vue_deployment = get_json(target, "deployment", vue_deployment_name, "")
    vue_configuration = get_json(
        target,
        "configmap",
        config_map_name(vue_deployment),
        "",
    )
    expected_chat_url = f"http://{target.java_service}.{target.namespace}.svc.cluster.local"
    require(
        nested(vue_configuration, "data").get("CHAT_URL") == expected_chat_url,
        "deployed Vue Chat route is not the Java service",
    )
    return {
        "environment": target.environment,
        "context": target.context,
        "namespace": target.namespace,
        "service": target.service,
        "source_revision": target.source_revision,
        "version": target.version,
        "image_digest": target.image_digest,
        "pod_image_id": image_id,
        "public_chat_url": expected_chat_url,
    }


def nginx_arguments(
    *,
    nginx_port: int,
    relay_directory: Path,
    container_name: str,
    docker: str,
    nginx_image: str,
) -> tuple[str, ...]:
    require(nginx_image == NGINX_IMAGE, "qualification Nginx image differs")
    require(1 <= nginx_port <= 65535, "loopback port is invalid")
    require(relay_directory.is_absolute(), "qualification relay directory is invalid")
    return (
        docker,
        "run",
        "--rm",
        "--name",
        container_name,
        "--add-host",
        "kube-dns.kube-system.svc.cluster.local:127.0.0.1",
        "--publish",
        f"127.0.0.1:{nginx_port}:80",
        "--volume",
        f"{relay_directory}:/run/chat-upstream:ro",
        "--env",
        "GATEWAY_URL=http://127.0.0.1:1",
        "--env",
        "AUTH_URL=http://127.0.0.1:1",
        "--env",
        "CHAT_URL=http://unix:/run/chat-upstream/chat.sock:",
        nginx_image,
    )


def relay_arguments(socket_path: Path, chat_port: int, socat: str) -> tuple[str, ...]:
    require(socket_path.is_absolute(), "qualification relay socket is invalid")
    require(1 <= chat_port <= 65535, "loopback port is invalid")
    return (
        socat,
        f"UNIX-LISTEN:{socket_path},fork,mode=0666",
        f"TCP:127.0.0.1:{chat_port}",
    )


class NginxProcess:
    def __init__(
        self,
        arguments: Sequence[str],
        *,
        name: str,
        docker: str,
        environment: Mapping[str, str],
    ) -> None:
        self.arguments = tuple(arguments)
        self.name = name
        self.docker = docker
        self.environment = environment
        self.owned: OwnedProcess | None = None

    def __enter__(self) -> OwnedProcess:
        run(
            [self.docker, "container", "inspect", self.name],
            cwd=ROOT,
            env=self.environment,
            timeout_seconds=10,
            activity="check Chat qualification container name",
            expected_failure=True,
        )
        self.owned = OwnedProcess.start(
            self.arguments,
            cwd=ROOT,
            env=self.environment,
            teardown_seconds=10,
        )
        return self.owned

    def __exit__(
        self,
        _exception_type: object,
        exception: BaseException | None,
        _traceback: object,
    ) -> None:
        if self.owned is None:
            return
        cleanup_error: BaseException | None = None
        if self.owned.process.poll() is None:
            try:
                run(
                    [self.docker, "rm", "--force", self.name],
                    cwd=ROOT,
                    env=self.environment,
                    timeout_seconds=15,
                    activity="remove Chat qualification Nginx container",
                )
            except BaseException as error:
                cleanup_error = error
        try:
            self.owned.close()
        except BaseException as error:
            cleanup_error = cleanup_error or error
        if cleanup_error is not None:
            if exception is not None:
                exception.add_note(f"Chat qualification Nginx cleanup also failed: {cleanup_error}")
                return
            raise ResourceError("Chat qualification Nginx cleanup failed") from cleanup_error


class RelayProcess:
    def __init__(self, arguments: Sequence[str], environment: Mapping[str, str]) -> None:
        self.arguments = tuple(arguments)
        self.environment = environment
        self.owned: OwnedProcess | None = None

    def __enter__(self) -> OwnedProcess:
        self.owned = OwnedProcess.start(
            self.arguments,
            cwd=ROOT,
            env=self.environment,
            teardown_seconds=10,
        )
        return self.owned

    def __exit__(
        self,
        _exception_type: object,
        exception: BaseException | None,
        _traceback: object,
    ) -> None:
        if self.owned is None:
            return
        try:
            self.owned.close()
        except BaseException as error:
            if exception is not None:
                exception.add_note(f"Chat qualification relay cleanup also failed: {error}")
                return
            raise ResourceError("Chat qualification relay cleanup failed") from error


def wait_for_relay(socket_path: Path, process: OwnedProcess, seconds: float = 10) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if process.process.poll() is not None:
            raise QualificationError("qualification relay stopped before readiness")
        try:
            if stat.S_ISSOCK(socket_path.stat().st_mode):
                return
        except FileNotFoundError:
            pass
        time.sleep(0.1)
    raise QualificationError("qualification relay readiness timed out")


def wait_for_nginx(port: int, process: OwnedProcess, seconds: float = 30) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if process.process.poll() is not None:
            raise QualificationError("qualification Nginx stopped before readiness")
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=1) as response:
                if response.status == 200:
                    return
        except OSError, urllib.error.URLError:
            pass
        time.sleep(0.2)
    raise QualificationError("qualification Nginx readiness timed out")


def verify_parity(
    nginx_port: int,
    make: str,
    environment: Mapping[str, str],
) -> None:
    run(
        [
            make,
            "-C",
            str(ROOT / "services/chat-service"),
            "verify-live-java-parity",
            f"JAVA_PARITY_ENDPOINT=ws://127.0.0.1:{nginx_port}",
        ],
        cwd=ROOT,
        env=environment,
        timeout_seconds=60,
        activity="verify Chat real-client parity through Nginx",
    )


def qualify(
    target: Target,
    arguments: argparse.Namespace,
    *,
    get_json: JsonGetter = kubectl_json,
    forward_type: type[OwnedPortForward] = OwnedPortForward,
    relay_type: type[RelayProcess] = RelayProcess,
    nginx_type: type[NginxProcess] = NginxProcess,
    relay_waiter: Callable[[Path, OwnedProcess], None] = wait_for_relay,
    waiter: Callable[[int, OwnedProcess], None] = wait_for_nginx,
    verifier: Callable[[int, str, Mapping[str, str]], None] = verify_parity,
) -> None:
    evidence = validate_live_identity(target, get_json)
    require(
        arguments.confirm == f"run-{target.environment}-chat-private-qualification",
        "qualification confirmation differs",
    )
    chat_port = arguments.chat_port or loopback_port()
    nginx_port = arguments.nginx_port or loopback_port()
    require(chat_port != nginx_port, "qualification loopback ports must differ")
    container_name = f"insurance-hub-chat-qualification-{os.getpid()}"
    environment = child_environment(
        preferred_executables=(
            arguments.kubectl,
            arguments.docker,
            arguments.make,
            arguments.socat,
        )
    )
    forward = forward_type(
        PortForwardSpec(
            context=target.context,
            namespace=target.namespace,
            resource=f"service/{target.service}",
            local_port=chat_port,
            remote_port=8080,
        ),
        cwd=ROOT,
        env=environment,
        startup_seconds=30,
        teardown_seconds=10,
        kubectl=arguments.kubectl,
    )
    with tempfile.TemporaryDirectory(prefix="insurance-hub-chat-qualification-") as directory:
        relay_directory = Path(directory)
        relay_directory.chmod(0o755)
        socket_path = relay_directory / "chat.sock"
        relay = relay_type(
            relay_arguments(socket_path, chat_port, arguments.socat),
            environment,
        )
        nginx = nginx_type(
            nginx_arguments(
                nginx_port=nginx_port,
                relay_directory=relay_directory,
                container_name=container_name,
                docker=arguments.docker,
                nginx_image=arguments.nginx_image,
            ),
            name=container_name,
            docker=arguments.docker,
            environment=environment,
        )
        with forward, relay as relay_process, nginx as nginx_process:
            relay_waiter(socket_path, relay_process)
            waiter(nginx_port, nginx_process)
            verifier(nginx_port, arguments.make, environment)
            print(
                json.dumps(
                    {
                        **evidence,
                        "nginx_endpoint": f"ws://127.0.0.1:{nginx_port}",
                        "real_client_parity": "passed",
                    },
                    sort_keys=True,
                )
            )


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("action", choices=("preflight", "run"))
    result.add_argument("--environment", required=True)
    result.add_argument("--context", required=True)
    result.add_argument("--namespace", required=True)
    result.add_argument("--service", required=True)
    result.add_argument("--source-revision", required=True)
    result.add_argument("--image-digest", required=True)
    result.add_argument("--confirm", default="")
    result.add_argument("--chat-port", type=int, default=0)
    result.add_argument("--nginx-port", type=int, default=0)
    result.add_argument("--nginx-image", default=NGINX_IMAGE)
    result.add_argument("--kubectl", default=os.environ.get("KUBECTL", "kubectl"))
    result.add_argument("--docker", default=os.environ.get("DOCKER", "docker"))
    result.add_argument("--make", default=os.environ.get("MAKE", "make"))
    result.add_argument("--socat", default=os.environ.get("SOCAT", "socat"))
    return result


def main(argv: Sequence[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    try:
        target = Target.validated(arguments)
        if arguments.action == "preflight":
            print(json.dumps(validate_live_identity(target), sort_keys=True))
        else:
            qualify(target, arguments)
    except (
        AdministrationError,
        CommandError,
        OSError,
        QualificationError,
        ResourceError,
        ValueError,
    ) as error:
        print(f"chat-qualification: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(lambda: main(), name="chat-qualification"))
