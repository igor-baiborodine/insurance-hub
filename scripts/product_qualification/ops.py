"""Read-only preview rendering/status and foreground localhost forwards."""

from __future__ import annotations

import argparse
import json
import os
import re
import signal
import subprocess
import sys
import time
from collections.abc import Sequence
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import yaml

from scripts.product_qualification.core import (
    QualificationError,
    accepted_bodies,
    check_reads,
    compare_body,
    decode_body,
    http_request,
    load_token,
    validate_target,
)

JAVA_URL = "http://qa-product-api-legacy.qa-svc.svc.cluster.local:80"
GO_URL = "http://qa-product-api.qa-svc.svc.cluster.local:8081"
REVISION = re.compile(r"main@sha1:[0-9a-f]{40}\Z")
FORWARDS = {
    "preview": ("qa-svc", "service/qa-web-vue-product-preview", 80),
    "established": ("qa-svc", "service/qa-agent-portal-gateway-legacy", 80),
    "auth": ("qa-svc", "service/qa-auth-api-legacy", 80),
    "grpc": ("qa-svc", "service/qa-product-api", 9090),
    "prometheus": ("qa-monitoring", "service/qa-prometheus-kube-prometh-prometheus", 9090),
}


class Interrupted(Exception):
    def __init__(self, code: int) -> None:
        self.code = code


def command(*arguments: str, timeout: float = 10) -> str:
    try:
        result = subprocess.run(
            arguments, check=True, capture_output=True, text=True, timeout=timeout
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise QualificationError("QA inspection command failed") from error
    return result.stdout


def kubectl(namespace: str, *arguments: str) -> dict[str, Any]:
    if namespace not in ("qa-svc", "flux-system"):
        raise QualificationError("wrong inspection namespace")
    return json.loads(
        command(
            "kubectl",
            "--context",
            "qa-insurance-hub",
            "--namespace",
            namespace,
            *arguments,
            "-o",
            "json",
        )
    )


def ready(value: dict[str, Any]) -> bool:
    return any(
        item.get("type") == "Ready" and item.get("status") == "True"
        for item in value.get("status", {}).get("conditions", [])
    )


def selected_url(config: str) -> str:
    try:
        value = yaml.safe_load(config)
        urls = value["micronaut"]["http"]["services"]["product-service"]["urls"]
    except (yaml.YAMLError, TypeError, KeyError) as error:
        raise QualificationError("preview Product override is malformed") from error
    if not isinstance(urls, list) or len(urls) != 1 or urls[0] not in (JAVA_URL, GO_URL):
        raise QualificationError("preview Product URL is not the agreed Java or Go target")
    return urls[0]


def render() -> dict[str, Any]:
    text = command("kubectl", "kustomize", "k8s/overlays/qa/svc/product-preview", timeout=20)
    try:
        resources = list(yaml.safe_load_all(text))
    except yaml.YAMLError as error:
        raise QualificationError("preview render is invalid YAML") from error
    names = {(item["kind"], item["metadata"]["name"]) for item in resources}
    expected = {
        ("Deployment", "qa-agent-portal-gateway-product-preview"),
        ("Deployment", "qa-web-vue-product-preview"),
        ("Service", "qa-agent-portal-gateway-product-preview"),
        ("Service", "qa-web-vue-product-preview"),
    }
    if len(resources) != 6 or not expected.issubset(names) or len(names) != 6:
        raise QualificationError("preview render lacks isolated resource set")
    if any(item["metadata"].get("namespace") != "qa-svc" for item in resources):
        raise QualificationError("preview resource is outside qa-svc")
    configs = [item for item in resources if item["kind"] == "ConfigMap"]
    gateway_config = next((item for item in configs if "application.yml" in item["data"]), None)
    if gateway_config is None:
        raise QualificationError("rendered gateway Product override is missing")
    url = selected_url(gateway_config["data"]["application.yml"])
    for stem in ("agent-portal-gateway", "web-vue"):
        name = f"qa-{stem}-product-preview"
        deployment = next(
            item
            for item in resources
            if item["kind"] == "Deployment" and item["metadata"]["name"] == name
        )
        service = next(
            item
            for item in resources
            if item["kind"] == "Service" and item["metadata"]["name"] == name
        )
        selector = deployment["spec"]["selector"]["matchLabels"]
        if (
            selector != service["spec"]["selector"]
            or any(
                deployment["spec"]["template"]["metadata"]["labels"].get(key) != value
                for key, value in selector.items()
            )
            or selector.get("app.kubernetes.io/name") != f"{stem}-product-preview"
        ):
            raise QualificationError("preview selector is not isolated")
    return {
        "resources": sorted(f"{kind}/{name}" for kind, name in names),
        "product_url": url,
        "config_map": gateway_config["metadata"]["name"],
    }


def endpoint_addresses(service: str) -> set[str]:
    value = kubectl(
        "qa-svc", "get", "endpointslices", "-l", f"kubernetes.io/service-name={service}"
    )
    return {
        address
        for item in value.get("items", [])
        for endpoint in item.get("endpoints", [])
        if endpoint.get("conditions", {}).get("ready") is True
        for address in endpoint.get("addresses", [])
    }


def status(expected_revision: str, phase: str) -> dict[str, Any]:
    if not REVISION.fullmatch(expected_revision) or phase not in ("java", "go"):
        raise QualificationError("expected main revision and phase are required")
    source = kubectl("flux-system", "get", "gitrepository", "insurance-hub")
    applied = kubectl("flux-system", "get", "kustomization", "qa-svc")
    source_revision = source.get("status", {}).get("artifact", {}).get("revision")
    applied_revision = applied.get("status", {}).get("lastAppliedRevision")
    if (
        source_revision != expected_revision
        or applied_revision != expected_revision
        or not ready(source)
        or not ready(applied)
    ):
        raise QualificationError("QA Flux has not applied the expected Ready revision")
    deployment = kubectl("qa-svc", "get", "deployment", "qa-agent-portal-gateway-product-preview")
    if (
        deployment.get("status", {}).get("readyReplicas") != 1
        or deployment.get("status", {}).get("updatedReplicas") != 1
    ):
        raise QualificationError("preview gateway rollout is not complete")
    pod_template = deployment["spec"]["template"]["spec"]
    config_volume = next((item for item in pod_template["volumes"] if "configMap" in item), None)
    config_name = config_volume["configMap"]["name"] if config_volume else None
    if not config_name or not config_name.startswith("qa-gateway-product-preview-config-"):
        raise QualificationError("preview gateway has no generated configuration")
    containers = pod_template["containers"]
    if (
        len(containers) != 1
        or "@sha256:" not in containers[0].get("image", "")
        or not any(
            item.get("name") == "MICRONAUT_CONFIG_FILES"
            and item.get("value") == "/etc/product-preview/application.yml"
            for item in containers[0].get("env", [])
        )
        or not any(
            item.get("name") == config_volume["name"]
            and item.get("mountPath") == "/etc/product-preview/application.yml"
            for item in containers[0].get("volumeMounts", [])
        )
    ):
        raise QualificationError("preview gateway effective configuration mount is missing")
    config = kubectl("qa-svc", "get", "configmap", config_name)
    actual_url = selected_url(config["data"]["application.yml"])
    if actual_url != (JAVA_URL if phase == "java" else GO_URL):
        raise QualificationError("preview backend selection differs from expected phase")
    pods = kubectl(
        "qa-svc", "get", "pods", "-l", "app.kubernetes.io/name=agent-portal-gateway-product-preview"
    )
    live = [
        item
        for item in pods.get("items", [])
        if item.get("status", {}).get("phase") == "Running"
        and len(item.get("status", {}).get("containerStatuses", [])) == 1
        and all(state.get("ready") for state in item.get("status", {}).get("containerStatuses", []))
    ]
    if len(live) != 1 or live[0]["metadata"]["uid"] == "":
        raise QualificationError("preview gateway has no unique Ready Pod")
    live_config = next(
        (item["configMap"]["name"] for item in live[0]["spec"]["volumes"] if "configMap" in item),
        None,
    )
    if live_config != config_name:
        raise QualificationError("serving preview gateway uses an older Product config")
    preview_endpoints = endpoint_addresses("qa-agent-portal-gateway-product-preview")
    established_endpoints = endpoint_addresses("qa-agent-portal-gateway-legacy")
    pod_ip = live[0]["status"]["podIP"]
    if (
        preview_endpoints != {pod_ip}
        or not established_endpoints
        or preview_endpoints & established_endpoints
    ):
        raise QualificationError("preview or established gateway endpoint isolation failed")
    frontend = kubectl("qa-svc", "get", "deployment", "qa-web-vue-product-preview")
    if (
        frontend.get("status", {}).get("readyReplicas") != 1
        or frontend.get("status", {}).get("updatedReplicas") != 1
    ):
        raise QualificationError("preview Vue rollout is not complete")
    frontend_pods = kubectl(
        "qa-svc", "get", "pods", "-l", "app.kubernetes.io/name=web-vue-product-preview"
    )
    ready_frontend = [
        item
        for item in frontend_pods.get("items", [])
        if item.get("status", {}).get("phase") == "Running"
        and len(item.get("status", {}).get("containerStatuses", [])) == 1
        and item["status"]["containerStatuses"][0].get("ready")
    ]
    if len(ready_frontend) != 1:
        raise QualificationError("preview Vue has no unique Ready Pod")
    frontend_containers = ready_frontend[0]["spec"]["containers"]
    if len(frontend_containers) != 1 or "@sha256:" not in frontend_containers[0].get("image", ""):
        raise QualificationError("serving preview Vue image is not immutable")
    frontend_config = next(
        (
            item["configMapRef"]["name"]
            for item in frontend_containers[0].get("envFrom", [])
            if "configMapRef" in item
        ),
        None,
    )
    if not frontend_config or not frontend_config.startswith("qa-web-vue-product-preview-config-"):
        raise QualificationError("serving preview Vue has no generated configuration")
    frontend_values = kubectl("qa-svc", "get", "configmap", frontend_config)["data"]
    if (
        frontend_values.get("GATEWAY_URL")
        != "http://qa-agent-portal-gateway-product-preview.qa-svc.svc.cluster.local"
        or frontend_values.get("AUTH_URL") != "http://qa-auth-api-legacy.qa-svc.svc.cluster.local"
        or frontend_values.get("CHAT_URL") != "http://qa-chat-api-legacy.qa-svc.svc.cluster.local"
    ):
        raise QualificationError("preview Vue proxy destinations are not the QA selections")
    frontend_endpoints = endpoint_addresses("qa-web-vue-product-preview")
    established_frontend_endpoints = endpoint_addresses("qa-web-vue-legacy")
    if (
        frontend_endpoints != {ready_frontend[0]["status"]["podIP"]}
        or not established_frontend_endpoints
        or frontend_endpoints & established_frontend_endpoints
    ):
        raise QualificationError("preview or established Vue endpoint isolation failed")
    return {
        "source_revision": source_revision,
        "applied_revision": applied_revision,
        "phase": phase,
        "product_url": actual_url,
        "config_map": config_name,
        "gateway_pod": live[0]["metadata"]["name"],
        "gateway_image_id": live[0]["status"]["containerStatuses"][0]["imageID"],
        "frontend_pod": ready_frontend[0]["metadata"]["name"],
        "frontend_image_id": ready_frontend[0]["status"]["containerStatuses"][0]["imageID"],
        "frontend_config_map": frontend_config,
        "preview_endpoints": sorted(preview_endpoints),
        "established_endpoints": sorted(established_endpoints),
        "frontend_endpoints": sorted(frontend_endpoints),
    }


def utc_time(raw: str) -> datetime:
    try:
        value = datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError as error:
        raise QualificationError("invalid UTC publication timestamp") from error
    if value.tzinfo is None or value.utcoffset().total_seconds() != 0:
        raise QualificationError("publication timestamp must be UTC")
    return value


def restore(
    expected_revision: str,
    published_at: str,
    preview_url: str,
    established_url: str,
    token_file: Path,
    output: Path,
) -> bool:
    if not REVISION.fullmatch(expected_revision) or output.exists():
        raise QualificationError("expected Java revision and new evidence path are required")
    validate_target(preview_url)
    validate_target(established_url)
    published = utc_time(published_at)
    if (datetime.now(UTC) - published).total_seconds() < -5:
        raise QualificationError("publication timestamp is in the future")
    token = load_token(token_file)
    accepted = accepted_bodies(
        Path("legacy/product-service/src/test/resources/product-read-baseline/http/qa.json")
    )
    evidence: dict[str, Any] = {
        "expected_revision": expected_revision,
        "published_at_utc": published_at,
        "status": "incomplete",
        "observations": [],
    }
    deadline = time.monotonic() + 1800
    next_heartbeat = time.monotonic()
    last_error = "awaiting QA reconciliation"
    attempt = 0
    prior_int = signal.getsignal(signal.SIGINT)
    prior_term = signal.getsignal(signal.SIGTERM)

    def interrupted(signum, _frame) -> None:
        raise Interrupted(130 if signum == signal.SIGINT else 143)

    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    try:
        while time.monotonic() < deadline:
            attempt += 1
            try:
                state = status(expected_revision, "java")
                for round_number in (1, 2):
                    check_reads(
                        accepted,
                        lambda path, credential: http_request(preview_url, credential, path),
                        token,
                    )
                    established_status, body = http_request(established_url, token, "/api/products")
                    if established_status != 200:
                        raise QualificationError("established Java list control failed")
                    compare_body(accepted["list"], decode_body(body), "established")
                    evidence["observations"].append(
                        {
                            "round": round_number,
                            "attempt": attempt,
                            "at_utc": datetime.now(UTC).isoformat(),
                            "preview": "accepted",
                            "established": "accepted",
                        }
                    )
                    if round_number == 1:
                        time.sleep(5)
                state = status(expected_revision, "java")
                verified = datetime.now(UTC)
                elapsed = (verified - published).total_seconds()
                evidence.update(
                    {
                        "route": state,
                        "verified_at_utc": verified.isoformat(),
                        "technical_seconds": elapsed,
                        "status": "passed" if elapsed <= 900 else "bound-exceeded",
                    }
                )
                return evidence["status"] == "passed"
            except QualificationError as error:
                last_error = str(error)
            except OSError, ValueError, KeyError, TypeError:
                last_error = "QA transport or evidence input unavailable"
            if time.monotonic() >= next_heartbeat:
                elapsed = (datetime.now(UTC) - published).total_seconds()
                print(
                    f"product-qualification: waiting for Java restoration ({elapsed:.0f}s): "
                    f"{last_error}",
                    file=sys.stderr,
                )
                next_heartbeat = time.monotonic() + 30
            time.sleep(min(15, max(0, deadline - time.monotonic())))
        evidence["status"] = "recovery-timeout"
        evidence["last_error"] = last_error
        return False
    except Interrupted:
        evidence["status"] = "interrupted"
        evidence["last_error"] = last_error
        raise
    finally:
        signal.signal(signal.SIGINT, prior_int)
        signal.signal(signal.SIGTERM, prior_term)
        evidence["finished_at_utc"] = datetime.now(UTC).isoformat()
        descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as target:
            target.write(json.dumps(evidence, indent=2) + "\n")


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    action = parser.add_subparsers(dest="action", required=True)
    action.add_parser("render")
    inspect = action.add_parser("status")
    inspect.add_argument("--expected-revision", required=True)
    inspect.add_argument("--phase", choices=("java", "go"), required=True)
    restoration = action.add_parser("restore")
    restoration.add_argument("--expected-revision", required=True)
    restoration.add_argument("--published-at", required=True)
    restoration.add_argument("--preview-url", required=True)
    restoration.add_argument("--established-url", required=True)
    restoration.add_argument("--token-file", type=Path, required=True)
    restoration.add_argument("--output", type=Path, required=True)
    forward = action.add_parser("forward")
    forward.add_argument("--service", choices=tuple(FORWARDS), required=True)
    forward.add_argument("--local-port", type=int, required=True)
    args = parser.parse_args(argv)
    try:
        if args.action == "render":
            print(json.dumps(render(), indent=2))
        elif args.action == "status":
            print(json.dumps(status(args.expected_revision, args.phase), indent=2))
        elif args.action == "restore":
            if not restore(
                args.expected_revision,
                args.published_at,
                args.preview_url,
                args.established_url,
                args.token_file,
                args.output,
            ):
                raise QualificationError("Java restoration failed or exceeded its bound")
        else:
            if args.local_port < 1024 or args.local_port > 65535:
                raise QualificationError("localhost port must be 1024–65535")
            namespace, service, remote = FORWARDS[args.service]
            command_args = [
                "kubectl",
                "--context",
                "qa-insurance-hub",
                "--namespace",
                namespace,
                "port-forward",
                "--address",
                "127.0.0.1",
                service,
                f"{args.local_port}:{remote}",
            ]
            os.execvp(command_args[0], command_args)
    except Interrupted as error:
        print("product-qualification: restoration verification interrupted", file=sys.stderr)
        return error.code
    except QualificationError as error:
        print(f"product-qualification: {error}", file=sys.stderr)
        return 1
    except KeyError, IndexError, OSError, ValueError, TypeError:
        print("product-qualification: render, status or forward failed", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
