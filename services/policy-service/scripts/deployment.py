"""Guarded deployment and private verification for the Go Policy foundation."""

from __future__ import annotations

import argparse
import http.client
import json
import os
import re
import signal
import sys
import time
import uuid
from contextlib import ExitStack
from ipaddress import IPv4Address
from pathlib import Path

import yaml

# isort: split
from scripts.go.service import deployment as lifecycle
from scripts.go.service.configuration import service_record, target_record
from scripts.go.service.tooling_contracts import (
    AdministrationError,
    OwnedPortForward,
    PortForwardSpec,
    loopback_port,
)
from scripts.python.process_runner import CommandError, run_command

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
SELECTOR = {"app.kubernetes.io/name": "policy-api", "app.kubernetes.io/component": "api"}
VERSION_LABEL = "app.kubernetes.io/version"
PLACEHOLDER = "git-000000000000"
PORTS = {"http": 8080, "grpc": 9090, "management": 8081}
DNS_EGRESS = ("kube-system", {"k8s-app": "kube-dns"}, [(53, "UDP"), (53, "TCP")])
ALLOY_EGRESS = (
    "qa-monitoring",
    {"app.kubernetes.io/name": "alloy", "app.kubernetes.io/instance": "qa-alloy"},
    [(4317, "TCP")],
)
# Each environment reaches its Policy CNPG cluster as the writer, Kafka as the producer and both
# providers over gRPC.
DATABASE_PORT = 5432
KAFKA_PORT = 9093
PROVIDER_PORT = 9090
BUSINESS_CLIENT_LABEL = "insurance-hub.io/policy-business-client"
DATABASE_CONFIG = {"DB_MAX_CONNS": "4", "DB_CONNECT_TIMEOUT": "3s", "DB_QUERY_TIMEOUT": "1s"}
WRITER_CA_DIRECTORY = "/etc/policy-service/postgres"
KAFKA_CA_DIRECTORY = "/etc/policy-service/kafka"
PROBE_IMAGE = (
    "docker.io/curlimages/curl:8.16.0@"
    "sha256:463eaf6072688fe96ac64fa623fe73e1dbe25d8ad6c34404a669ad3ce1f104b6"
)
PROBE_OWNER_LABEL = "insurance-hub.io/policy-denial-owner"
DIRECT_RPC_SMOKE = "TestDeployedDirectRPC_CreateOffer"
NETWORK_POLICY_CONVERGENCE_SECONDS = 4
# curl reports a completed TCP handshake as either message, depending on its version.
PROBE_SHELL_FUNCTIONS = (
    'connected() { case "$1" in *"Connected to"*|*"Established connection"*) return 0;; esac; '
    "return 1; }; "
    "denied() { status=0; output=$(curl --verbose --connect-timeout 2 --max-time 3 "
    '"telnet://$1" 2>&1) || status=$?; ! connected "$output" '
    '&& { [ "$status" = 7 ] || [ "$status" = 28 ]; }; }; '
    'allowed() { output=$(curl --verbose --connect-timeout 2 --max-time 3 "telnet://$1" 2>&1) '
    '|| true; connected "$output"; }; '
)
require = lifecycle.require


def safe_environment() -> dict[str, str]:
    return {
        key: os.environ[key]
        for key in ("HOME", "PATH", "KUBECONFIG", "DOCKER_CONFIG", "SSL_CERT_DIR", "SSL_CERT_FILE")
        if key in os.environ
    }


def run(argv: list[str], *, input_text: str | None = None, timeout: float = 30) -> str:
    return run_command(
        argv,
        cwd=ROOT,
        env=safe_environment(),
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity="Policy deployment command",
        input_text=input_text,
    ).stdout


def source_version(revision: str) -> str:
    require(bool(re.fullmatch(r"[0-9a-f]{40}", revision)), "full image source revision is required")
    version = f"git-{revision[:12]}"
    require(version != PLACEHOLDER, "placeholder source revision is forbidden")
    return version


def database_egress(environment: str, namespace: str) -> tuple:
    return (
        namespace,
        {"cnpg.io/cluster": f"{environment}-postgres-policy"},
        [(DATABASE_PORT, "TCP")],
    )


def provider_egress(namespace: str, provider: str) -> tuple:
    return (
        namespace,
        {"app.kubernetes.io/name": f"{provider}-api", "app.kubernetes.io/component": "api"},
        [(PROVIDER_PORT, "TCP")],
    )


def kafka_egress(environment: str, namespace: str) -> tuple:
    return (
        namespace,
        {"strimzi.io/cluster": f"{environment}-kafka", "strimzi.io/component-type": "kafka"},
        [(KAFKA_PORT, "TCP")],
    )


def gateway_selector(environment: str) -> dict[str, str]:
    return {
        "app.kubernetes.io/name": "agent-portal-gateway-legacy",
        "app.kubernetes.io/instance": "insurance-hub-agent-portal-gateway-legacy",
        "app.kubernetes.io/component": "gateway",
        "environment": environment,
        BUSINESS_CLIENT_LABEL: "true",
    }


def gateway_ingress(environment: str, namespace: str) -> dict:
    return {
        "from": [
            {
                "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": namespace}},
                "podSelector": {"matchLabels": gateway_selector(environment)},
            }
        ],
        "ports": [{"port": PORTS["http"], "protocol": "TCP"}],
    }


def provider_targets(environment: str, namespace: str) -> dict[str, str]:
    return {
        f"{provider.upper()}_GRPC_TARGET": (
            f"{environment}-{provider}-api.{namespace}.svc.cluster.local:{PROVIDER_PORT}"
        )
        for provider in ("product", "pricing")
    }


def expected_policy(environment: str, data_namespace: str, service_namespace: str) -> dict:
    destinations = (
        DNS_EGRESS,
        *((ALLOY_EGRESS,) if environment == "qa" else ()),
        database_egress(environment, data_namespace),
        provider_egress(service_namespace, "product"),
        provider_egress(service_namespace, "pricing"),
        kafka_egress(environment, data_namespace),
    )
    egress = [
        {
            "to": [
                {
                    "namespaceSelector": {
                        "matchLabels": {"kubernetes.io/metadata.name": namespace}
                    },
                    "podSelector": {"matchLabels": labels},
                }
            ],
            "ports": [{"port": port, "protocol": protocol} for port, protocol in ports],
        }
        for namespace, labels, ports in destinations
    ]
    return {
        "podSelector": {"matchLabels": SELECTOR},
        "policyTypes": ["Ingress", "Egress"],
        "ingress": [gateway_ingress(environment, service_namespace)],
        "egress": egress,
    }


def policy_spec(spec: dict) -> dict:
    return {"ingress": [], "egress": [], **spec}


def render(environment: str) -> str:
    record = service_record(ROOT, "policy")
    target = target_record(ROOT, record, environment)
    return run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(ROOT / target["overlay"])])


def validate_render(environment: str, text: str) -> dict[str, dict]:
    target = target_record(ROOT, service_record(ROOT, "policy"), environment)
    documents = list(yaml.safe_load_all(text))
    kinds = {
        "ConfigMap": "policy-config",
        "Deployment": "policy-api",
        "Service": "policy-api",
        "NetworkPolicy": "policy-api",
    }
    require(
        len(documents) == len(kinds)
        and all(isinstance(item, dict) for item in documents)
        and {item.get("kind") for item in documents} == kinds.keys(),
        "Policy render must contain exactly its Deployment, Service, ConfigMap and NetworkPolicy",
    )
    items = {item["kind"]: item for item in documents}
    for kind, suffix in kinds.items():
        metadata = items[kind]["metadata"]
        require(
            metadata["name"] == f"{environment}-{suffix}"
            and metadata["namespace"] == target["namespace"],
            "Policy resource name or namespace differs",
        )

    writer_secret = f"{environment}-postgres-policy-writer-creds"
    kafka_ca_secret = f"{environment}-policy-kafka-ca"
    deployment = items["Deployment"]
    spec = deployment["spec"]
    template = spec["template"]
    pod = template["spec"]
    require(
        spec["selector"] == {"matchLabels": SELECTOR}
        and all(template["metadata"]["labels"].get(k) == v for k, v in SELECTOR.items())
        and spec["replicas"] == 1
        and spec["strategy"] == {"type": "Recreate"},
        "Policy selector, labels or rollout policy differs",
    )
    require(
        set(pod)
        == {
            "automountServiceAccountToken",
            "enableServiceLinks",
            "terminationGracePeriodSeconds",
            "securityContext",
            "containers",
            "volumes",
        }
        and pod["automountServiceAccountToken"] is False
        and pod["enableServiceLinks"] is False
        and pod["terminationGracePeriodSeconds"] == 30
        and pod["securityContext"]
        == {
            "runAsNonRoot": True,
            "runAsUser": 65532,
            "runAsGroup": 65532,
            "fsGroup": 65532,
            "seccompProfile": {"type": "RuntimeDefault"},
        }
        and len(pod["containers"]) == 1,
        "Policy Pod isolation or security context differs",
    )
    container = pod["containers"][0]
    require(
        set(container)
        == {
            "name",
            "image",
            "imagePullPolicy",
            "securityContext",
            "ports",
            "env",
            "envFrom",
            "resources",
            "startupProbe",
            "livenessProbe",
            "readinessProbe",
            "volumeMounts",
        }
        and container["name"] == "policy-api"
        and container["imagePullPolicy"] == "IfNotPresent"
        and container["securityContext"]
        == {
            "allowPrivilegeEscalation": False,
            "readOnlyRootFilesystem": True,
            "capabilities": {"drop": ["ALL"]},
        }
        and container["ports"]
        == [
            {"name": name, "containerPort": port, "protocol": "TCP"} for name, port in PORTS.items()
        ]
        and container["env"]
        == [
            {
                "name": "POLICY_DATABASE_URL",
                "valueFrom": {"secretKeyRef": {"name": writer_secret, "key": "database-url"}},
            },
            {
                "name": "SERVICE_INSTANCE_ID",
                "valueFrom": {"fieldRef": {"apiVersion": "v1", "fieldPath": "metadata.uid"}},
            },
        ]
        and container["envFrom"] == [{"configMapRef": {"name": f"{environment}-policy-config"}}]
        and container["resources"]
        == {
            "requests": {"cpu": "50m", "memory": "32Mi"},
            "limits": {"cpu": "200m", "memory": "128Mi"},
        },
        "Policy container security, ports, resources or identity binding differs",
    )
    # Only the writer credential and the two public trust roots reach the serving Pod.
    require(
        container["volumeMounts"]
        == [
            {"name": "postgres-ca", "mountPath": WRITER_CA_DIRECTORY, "readOnly": True},
            {"name": "kafka-ca", "mountPath": KAFKA_CA_DIRECTORY, "readOnly": True},
        ]
        and pod["volumes"]
        == [
            {
                "name": "postgres-ca",
                "secret": {
                    "secretName": writer_secret,
                    "items": [{"key": "ca.crt", "path": "ca.crt"}],
                },
            },
            {
                "name": "kafka-ca",
                "secret": {
                    "secretName": kafka_ca_secret,
                    "items": [{"key": "ca.crt", "path": "ca.crt"}],
                    "defaultMode": 292,
                },
            },
        ],
        "Policy database credential or trust-root mount differs",
    )
    for name, path, period, failures in (
        ("startupProbe", "/livez", 2, 30),
        ("livenessProbe", "/livez", 10, 3),
        ("readinessProbe", "/readyz", 5, 2),
    ):
        require(
            container[name]
            == {
                "httpGet": {"path": path, "port": "management", "scheme": "HTTP"},
                "periodSeconds": period,
                "timeoutSeconds": 2,
                "failureThreshold": failures,
            },
            "Policy management probe differs",
        )
    data = items["ConfigMap"]["data"]
    version = data.get("SERVICE_VERSION", "")
    expected_data = {
        "SERVICE_NAME": "policy-service",
        "DEPLOYMENT_ENVIRONMENT": environment,
        "HTTP_ADDR": "0.0.0.0:8080",
        "GRPC_ADDR": "127.0.0.1:9090",
        "HEALTH_ADDR": "0.0.0.0:8081",
        "SERVICE_VERSION": version,
        "OTEL_ENABLED": "true" if environment == "qa" else "false",
    }
    expected_data.update(DATABASE_CONFIG)
    expected_data.update(provider_targets(environment, target["namespace"]))
    expected_data.update(
        {
            "EVENT_PUBLISHER_ENABLED": "true",
            "KAFKA_BROKERS": (
                "local-dev-kafka-kafka-bootstrap:9093"
                if environment == "local-dev"
                else "qa-kafka-kafka-bootstrap.qa-data.svc:9093"
            ),
            "KAFKA_REGISTERED_TOPIC": "policy-registered",
            "KAFKA_TERMINATED_TOPIC": "policy-terminated",
            "KAFKA_CA_FILE": f"{KAFKA_CA_DIRECTORY}/ca.crt",
            "KAFKA_PUBLISH_TIMEOUT": "3s",
        }
    )
    if environment == "qa":
        expected_data["OTEL_EXPORTER_OTLP_ENDPOINT"] = (
            "http://qa-alloy.qa-monitoring.svc.cluster.local:4317"
        )
    require(
        data == expected_data
        and bool(re.fullmatch(r"git-[0-9a-f]{12}", version))
        and all(
            metadata["labels"].get(VERSION_LABEL) == version
            for metadata in (deployment["metadata"], template["metadata"])
        ),
        "Policy runtime config or version labels differ",
    )
    image = container["image"]
    if environment == "qa":
        registry = "ghcr.io/igor-baiborodine/insurance-hub/policy-api"
        require(
            (version == PLACEHOLDER and image == f"{registry}:0.0.0-unpublished")
            or (
                version != PLACEHOLDER
                and re.fullmatch(
                    rf"{re.escape(registry)}:1\.[0-9]+\.[0-9]+@sha256:[0-9a-f]{{64}}", image
                )
            ),
            "QA image must be the placeholder or a digest-pinned release",
        )
    else:
        require(
            image == f"insurance-hub-policy-api:1.0.0-local.{version[4:]}",
            "local image and release version differ",
        )
    service = items["Service"]
    require(
        service["spec"]
        == {
            "type": "ClusterIP",
            "selector": SELECTOR,
            "ports": [{"name": "http", "port": 80, "targetPort": "http", "protocol": "TCP"}],
        },
        "Policy Service must expose only business HTTP to the Go Policy selector",
    )
    require(
        policy_spec(items["NetworkPolicy"]["spec"])
        == expected_policy(environment, target["dataNamespace"], target["namespace"]),
        "Policy NetworkPolicy must deny Pod ingress and allow only the accepted egress",
    )
    return items


def qa_activation(resources: list[str], items: dict[str, dict]) -> None:
    version = items["ConfigMap"]["data"]["SERVICE_VERSION"]
    require(
        resources.count("./policy") == int(version != PLACEHOLDER),
        "QA Policy activation and published release must agree",
    )


def check_render() -> None:
    resources = yaml.safe_load((ROOT / "k8s/overlays/qa/svc/kustomization.yaml").read_text())[
        "resources"
    ]
    for environment in ("local-dev", "qa"):
        items = validate_render(environment, render(environment))
        if environment == "qa":
            qa_activation(resources, items)


class Target:
    service = "policy"

    def __init__(self) -> None:
        self.record = service_record(ROOT, self.service)
        self.environment = os.environ.get("DEPLOY_ENV", "")
        self.configuration = target_record(ROOT, self.record, self.environment)
        self.context = self.configuration["context"]
        self.namespace = self.configuration["namespace"]
        requested = os.environ.get("DEPLOY_SVC_NS")
        require(
            os.environ.get("DEPLOY_CONTEXT") == self.context and requested == self.namespace,
            "deployment coordinates differ from the catalog",
        )
        self.deployment = f"{self.environment}-policy-api"
        self.kubectl_binary = os.environ.get("KUBECTL", "kubectl")

    def kubectl(self, *args: str, input_text: str | None = None, timeout: float = 30) -> str:
        return run(
            [self.kubectl_binary, "--context", self.context, "--namespace", self.namespace, *args],
            input_text=input_text,
            timeout=timeout,
        )

    def json(self, *args: str, timeout: float = 30) -> dict:
        return json.loads(self.kubectl(*args, "-o", "json", timeout=timeout) or "{}")

    def coordinates(self) -> None:
        require(
            run([self.kubectl_binary, "config", "current-context"]).strip() == self.context,
            "active Kubernetes context differs",
        )
        namespace = self.json("get", "namespace", self.namespace)
        require(
            namespace.get("metadata", {}).get("name") == self.namespace
            and namespace.get("status", {}).get("phase") == "Active",
            "target namespace is not active",
        )

    def rendered(self) -> tuple[str, dict[str, dict], str]:
        items = validate_render(self.environment, render(self.environment))
        version = source_version(os.environ.get("DEPLOY_IMAGE_SOURCE_REVISION", ""))
        deployment = items["Deployment"]
        template = deployment["spec"]["template"]
        container = template["spec"]["containers"][0]
        config = items["ConfigMap"]["data"]
        if self.configuration["mode"] == "direct":
            image = os.environ.get("DEPLOY_IMAGE_REF", "")
            require(
                image == f"{self.record['localImage']}:1.0.0-local.{version[4:]}",
                "select the exact local Policy image for the source revision",
            )
            container["image"] = image
            config["SERVICE_VERSION"] = version
            for metadata in (deployment["metadata"], template["metadata"]):
                metadata["labels"][VERSION_LABEL] = version
        else:
            require(not os.environ.get("DEPLOY_IMAGE_REF"), "image override is local-dev only")
            require(
                config["SERVICE_VERSION"] == version, "QA Policy release is inactive or differs"
            )
        self.template_labels = template["metadata"]["labels"]
        return yaml.safe_dump_all(items.values(), explicit_start=True), items, container["image"]

    def live(self, items: dict[str, dict], *, require_deployment: bool) -> dict:
        deployment = self.json("get", "deployment", self.deployment, "--ignore-not-found")
        for kind in ("Deployment", "Service", "ConfigMap", "NetworkPolicy"):
            desired = items[kind]
            actual = (
                deployment
                if kind == "Deployment"
                else self.json(
                    "get", kind.lower(), desired["metadata"]["name"], "--ignore-not-found"
                )
            )
            require(actual or not require_deployment, f"Policy {kind} is missing")
            if not actual:
                continue
            metadata = actual.get("metadata", {})
            require(
                metadata.get("namespace") == self.namespace
                and metadata.get("labels", {}).get("app.kubernetes.io/name") == "policy-api"
                and metadata.get("labels", {}).get("app.kubernetes.io/managed-by") == "kustomize",
                f"existing {kind} is outside Policy ownership",
            )
            if kind == "Deployment":
                require(
                    actual["spec"]["selector"] == {"matchLabels": SELECTOR}, "live selector differs"
                )
            if kind == "Service":
                actual_spec = actual.get("spec", {})
                require(
                    actual_spec.get("type") == "ClusterIP"
                    and actual_spec.get("selector") == desired["spec"]["selector"]
                    and actual_spec.get("ports") == desired["spec"]["ports"]
                    and not any(
                        field in actual_spec
                        for field in ("externalIPs", "loadBalancerIP", "externalName")
                    ),
                    "live Policy Service differs or is externally exposed",
                )
            if require_deployment and kind == "ConfigMap":
                require(actual.get("data") == desired["data"], "live Policy configuration differs")
            if require_deployment and kind == "NetworkPolicy":
                require(
                    policy_spec(actual["spec"]) == desired["spec"],
                    "live Policy NetworkPolicy differs",
                )
        check_requested_route(self, required=require_deployment)
        for network_policy in self.json("get", "networkpolicies").get("items", []):
            if network_policy["metadata"]["name"] == self.deployment:
                continue
            selector = network_policy["spec"]["podSelector"].get("matchLabels", {})
            require(
                not selector.items() <= self.template_labels.items(),
                "unexpected NetworkPolicy may permit Policy access",
            )
        if not require_deployment:
            return {}
        if self.configuration["mode"] == "flux":
            self.verify_flux_inventory(items)
        desired = items["Deployment"]["spec"]["template"]
        actual = deployment["spec"]["template"]
        version = desired["metadata"]["labels"][VERSION_LABEL]
        image = desired["spec"]["containers"][0]["image"]
        require(
            deployment["metadata"]["labels"].get(VERSION_LABEL) == version
            and actual["metadata"]["labels"].get(VERSION_LABEL) == version
            and actual["spec"]["containers"][0]["image"] == image
            and deployment["status"].get("observedGeneration")
            == deployment["metadata"]["generation"]
            and deployment["status"].get("readyReplicas") == 1,
            "Policy Deployment release identity or readiness differs",
        )
        selector = ",".join(f"{key}={value}" for key, value in SELECTOR.items())
        pods = [
            pod
            for pod in self.json("get", "pods", "-l", selector).get("items", [])
            if not pod["metadata"].get("deletionTimestamp")
        ]
        require(len(pods) == 1, "expected exactly one selected Policy Pod")
        pod = pods[0]
        statuses = pod["status"].get("containerStatuses", [])
        digest = image_id(self, image)
        require(
            pod["metadata"]["labels"].get(VERSION_LABEL) == version
            and pod["spec"]["containers"][0]["image"] == image
            and len(statuses) == 1
            and statuses[0].get("name") == "policy-api"
            and statuses[0].get("ready") is True
            and running_image_matches(
                self.configuration["mode"], image, digest, statuses[0].get("imageID", "")
            ),
            "Policy Pod image identity or readiness differs",
        )
        print(
            f"Policy Pod {pod['metadata']['name']} UID={pod['metadata']['uid']} "
            f"version={version} image={image} imageID={statuses[0]['imageID']}"
        )
        return pod

    def verify_flux_inventory(self, items: dict[str, dict]) -> None:
        flux = self.configuration["flux"]
        owner = self.json("get", "kustomization", flux["sharedOwner"], "-n", flux["namespace"])
        status = owner.get("status", {})
        revision = os.environ.get("DEPLOY_SOURCE_REVISION", "")
        require(
            lifecycle.ready(owner)
            and bool(re.fullmatch(r"[0-9a-f]{40}", revision))
            and status.get("lastAppliedRevision", "").rsplit(":", 1)[-1] == revision,
            "shared Flux inventory is not ready at the selected revision",
        )
        observed = {
            (entry["id"], entry["v"]) for entry in status.get("inventory", {}).get("entries", [])
        }
        expected = set()
        for kind, item in items.items():
            group, _, version = item["apiVersion"].rpartition("/")
            metadata = item["metadata"]
            identity = f"{metadata['namespace']}_{metadata['name']}_{group}_{kind}"
            expected.add((identity, version))
        require(expected <= observed, "shared Flux inventory does not own all Policy resources")


def running_image_matches(mode: str, image: str, digest: str, running: str) -> bool:
    if mode == "flux":
        # Same rule as the other Go services: the published digest, optionally repository-qualified.
        return running == digest or running.endswith("@" + digest)
    return running in {digest, f"{image.split(':', 1)[0]}@{digest}", f"containerd://{digest}"}


def image_id(target: Target, image: str) -> str:
    flux = target.configuration["mode"] == "flux"
    digest = os.environ.get("DEPLOY_IMAGE_DIGEST", "") if flux else ""
    if flux:
        require(valid_digest(digest), "recorded publication digest is required")
        require(
            image.endswith(f"@{digest}"), "QA desired state is not pinned to the publication digest"
        )
        image = f"{target.record['registryImage']}@{digest}"
    records = json.loads(run([os.environ.get("DOCKER", "docker"), "image", "inspect", image]))
    require(len(records) == 1, "Policy image inspection differs")
    labels = records[0]["Config"]["Labels"]
    revision = os.environ.get("DEPLOY_IMAGE_SOURCE_REVISION", "")
    require(
        labels.get("org.opencontainers.image.revision") == revision
        and labels.get("org.opencontainers.image.version") == source_version(revision),
        "Policy OCI labels differ from the selected source",
    )
    if flux:
        require(image in records[0].get("RepoDigests", []), "Policy publication digest differs")
        return digest
    result = records[0]["Id"]
    require(valid_digest(result), "exact Policy image ID is required")
    return result


def valid_digest(value: str) -> bool:
    return bool(re.fullmatch(r"sha256:[0-9a-f]{64}", value)) and value != "sha256:" + "0" * 64


def secret_and_image(target: Target, image: str) -> None:
    image_id(target, image)


def check_requested_route(target: Target, *, required: bool = True) -> None:
    selected = []
    for service in target.json("get", "services").get("items", []):
        selector = service.get("spec", {}).get("selector") or {}
        if selector and selector.items() <= target.template_labels.items():
            selected.append(service)
    require(
        len(selected) in ({1} if required else {0, 1})
        and all(service["metadata"]["name"] == target.deployment for service in selected),
        "Go Policy must have exactly its intended private HTTP Service",
    )


def rollout(target: Target) -> None:
    target.kubectl(
        "rollout", "status", f"deployment/{target.deployment}", "--timeout=180s", timeout=195
    )


def stop(target: Target, items: dict[str, dict]) -> None:
    target.live(items, require_deployment=False)
    target.kubectl(
        "scale", f"deployment/{target.deployment}", "--replicas=0", "--timeout=60s", timeout=65
    )
    deadline = time.monotonic() + 90
    while (remaining := deadline - time.monotonic()) > 0:
        deployment = target.json("get", "deployment", target.deployment, timeout=min(30, remaining))
        status = deployment.get("status", {})
        if (
            status.get("observedGeneration") == deployment["metadata"]["generation"]
            and status.get("replicas", 0) == 0
        ):
            return
        time.sleep(max(0, min(1, deadline - time.monotonic())))
    raise lifecycle.DeploymentError("Policy stop exceeded 90 seconds")


def response(port: int, path: str, payload: bytes | None = None) -> tuple[int, bytes]:
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    try:
        conn.request(
            "POST" if payload else "GET", path, payload, {"Content-Type": "application/json"}
        )
        result = conn.getresponse()
        return result.status, result.read(1024)
    finally:
        conn.close()


def validate_direct_rpc_output(output: str) -> None:
    selected_runs = 0
    selected_passes = 0
    selected_failed = False
    for line in output.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(event, dict) or event.get("Test") != DIRECT_RPC_SMOKE:
            continue
        if event.get("Action") == "run":
            selected_runs += 1
        elif event.get("Action") == "pass":
            selected_passes += 1
        elif event.get("Action") == "fail":
            selected_failed = True
    require(
        selected_runs == 1 and selected_passes == 1 and not selected_failed,
        "Policy direct RPC smoke did not execute and pass exactly once",
    )


def verify_direct_rpc(module_path: str, port: int) -> None:
    try:
        output = run(
            [
                os.environ.get("MAKE_COMMAND", "make"),
                "--no-print-directory",
                "-C",
                str(ROOT / module_path),
                "_deploy-smoke-rpc",
                f"GO={os.environ.get('GO', 'go')}",
                f"POLICY_SMOKE_GRPC_ADDR=127.0.0.1:{port}",
            ],
            timeout=120,
        )
    except CommandError as error:
        raise lifecycle.DeploymentError("Policy direct RPC offer smoke failed") from error
    validate_direct_rpc_output(output)


def verify(target: Target, items: dict[str, dict]) -> None:
    """Verify the private Policy workload and its ready Service endpoint."""
    pod = verify_workload(target, items)
    verify_go_service(target, pod)


def verify_workload(target: Target, items: dict[str, dict]) -> dict:
    """Check the private Policy workload in its target namespace."""
    probe = os.environ.get("DEPLOY_DENIAL_PROBE", "0")
    require(probe in {"0", "1"}, "DEPLOY_DENIAL_PROBE must be 0 or 1")
    pod = target.live(items, require_deployment=True)
    with ExitStack() as forwards:
        ports = {
            name: forwards.enter_context(
                OwnedPortForward(
                    PortForwardSpec(
                        target.context,
                        target.namespace,
                        f"pod/{pod['metadata']['name']}",
                        loopback_port(),
                        port,
                    ),
                    cwd=ROOT,
                    env=safe_environment(),
                    startup_seconds=20,
                    kubectl=target.kubectl_binary,
                )
            )
            for name, port in PORTS.items()
        }
        require(
            response(ports["management"], "/livez") == (200, b"live\n")
            and response(ports["management"], "/readyz") == (200, b"ready\n"),
            "Policy liveness or readiness response differs",
        )
        # An empty offer is rejected by validation before any provider call or write.
        status, body = response(ports["http"], "/offers", b"{}")
        require(
            status == 400
            and json.loads(body) == {"code": 3, "message": "invalid request", "details": []},
            "Policy HTTP offer rejection differs",
        )
        verify_direct_rpc(target.record["modulePath"], ports["grpc"])
    logs = target.kubectl("logs", pod["metadata"]["name"], "-c", "policy-api")
    records = [json.loads(line) for line in logs.splitlines()]
    expected = {
        "service_name": "policy-service",
        "service_namespace": "insurance-hub",
        "service_version": items["ConfigMap"]["data"]["SERVICE_VERSION"],
        "service_instance_id": pod["metadata"]["uid"],
        "deployment_environment_name": target.environment,
        "service_implementation": "go",
    }
    require(
        any(
            record.get("msg") == "service started" and expected.items() <= record.items()
            for record in records
        ),
        "Policy runtime identity differs",
    )
    container = items["Deployment"]["spec"]["template"]["spec"]["containers"][0]
    if any(item["name"] == "POLICY_DATABASE_URL" for item in container["env"]):
        # Readiness above ran the writer's bounded table reads from inside this Pod.
        require(
            any(
                record.get("msg") == "policy database ready"
                and record.get("database_role") == "go_policy_writer"
                for record in records
            ),
            "Policy Pod did not verify its writer database connection",
        )
        print("Policy database readiness passed through the writer role and verified TLS")
    print(
        "Policy HTTP and direct gRPC offer rejection, liveness, readiness and identity passed: "
        f"{expected}"
    )
    if probe == "1":
        verify_denial(target, pod)
        verify_business_access(target)
        verify_provider_access(target, items)
    return pod


def ready_endpoint_pods(target: Target, service: str) -> list[str]:
    slices = target.json(
        "get", "endpointslices", "-l", f"kubernetes.io/service-name={service}"
    ).get("items", [])
    references = [
        endpoint.get("targetRef", {})
        for item in slices
        for endpoint in item.get("endpoints") or []
        if endpoint.get("conditions", {}).get("ready") is True
    ]
    require(
        all(reference.get("kind") == "Pod" for reference in references),
        f"{service} endpoint is not a Pod",
    )
    return sorted({reference.get("name", "") for reference in references})


def verify_go_service(target: Target, pod: dict) -> None:
    names = ready_endpoint_pods(target, target.deployment)
    require(names == [pod["metadata"]["name"]], "Go Policy Service ready endpoints differ")
    labels = target.json("get", "pod", names[0]).get("metadata", {}).get("labels", {})
    require(SELECTOR.items() <= labels.items(), "Go Policy Service endpoint is not Go Policy")


def run_probe(
    target: Target,
    name: str,
    description: str,
    args: list[str],
    labels: dict[str, str] | None = None,
) -> list[str]:
    token = uuid.uuid4().hex
    require(
        not target.json("get", "pod", name, "--ignore-not-found"), "Policy probe name is occupied"
    )
    probe = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": name,
            "namespace": target.namespace,
            "labels": {**(labels or {}), PROBE_OWNER_LABEL: token},
        },
        "spec": {
            "restartPolicy": "Never",
            "activeDeadlineSeconds": 90,
            "automountServiceAccountToken": False,
            "enableServiceLinks": False,
            "securityContext": {
                "runAsNonRoot": True,
                "runAsUser": 65532,
                "runAsGroup": 65532,
                "seccompProfile": {"type": "RuntimeDefault"},
            },
            "containers": [
                {
                    "name": "probe",
                    "image": PROBE_IMAGE,
                    "securityContext": {
                        "allowPrivilegeEscalation": False,
                        "readOnlyRootFilesystem": True,
                        "capabilities": {"drop": ["ALL"]},
                    },
                    "command": ["sh", "-ec"],
                    "args": [
                        f"sleep {NETWORK_POLICY_CONVERGENCE_SECONDS}; "
                        + PROBE_SHELL_FUNCTIONS
                        + args[0],
                        "probe",
                        *args[1:],
                    ],
                }
            ],
        },
    }
    try:
        target.kubectl("create", "-f", "-", input_text=json.dumps(probe))
        deadline = time.monotonic() + 90
        actual: dict = {}
        while (remaining := deadline - time.monotonic()) > 0:
            actual = target.json("get", "pod", name, timeout=min(30, remaining))
            if actual.get("status", {}).get("phase") in {"Succeeded", "Failed"}:
                break
            time.sleep(max(0, min(1, deadline - time.monotonic())))
        logs = target.kubectl("logs", name)
        require(
            actual.get("status", {}).get("phase") == "Succeeded",
            f"Policy {description} probe failed: {logs}",
        )
        return logs.splitlines()
    finally:
        primary_failure = sys.exception()
        try:
            actual = target.json("get", "pod", name, "--ignore-not-found")
            if actual:
                require(
                    actual["metadata"].get("labels", {}).get(PROBE_OWNER_LABEL) == token,
                    f"Policy {description} probe ownership differs",
                )
                target.kubectl("delete", "pod", name, "--timeout=45s", timeout=55)
        except Exception as cleanup_error:
            if primary_failure is None:
                raise
            print(
                f"policy-deployment: {description} probe cleanup failed: {cleanup_error}",
                file=sys.stderr,
            )


def verify_denial(target: Target, pod: dict) -> None:
    ip = str(IPv4Address(pod["status"]["podIP"]))
    observed = run_probe(
        target,
        "policy-access-denied",
        "denial",
        [
            'address=$1; shift; for port do denied "$address:$port" || exit 1; '
            'echo "Policy port $port denied"; done',
            ip,
            *(str(port) for port in PORTS.values()),
        ],
    )
    require(
        observed == [f"Policy port {port} denied" for port in PORTS.values()],
        "Policy workload denial result differs",
    )


def verify_business_access(target: Target) -> None:
    address = f"{target.deployment}:80"
    allowed = run_probe(
        target,
        "policy-gateway-allowed",
        "gateway allow",
        ['allowed "$1" || exit 1; echo "allowed $1"', address],
        labels=gateway_selector(target.environment),
    )
    require(allowed == [f"allowed {address}"], "gateway identity cannot reach Policy HTTP")
    denied = run_probe(
        target,
        "policy-http-denied",
        "HTTP denial",
        ['denied "$1" || exit 1; echo "denied $1"', address],
    )
    require(denied == [f"denied {address}"], "an unrelated workload reaches Policy HTTP")
    print("Policy HTTP passed for the gateway identity and was denied otherwise")


def verify_provider_access(target: Target, items: dict[str, dict]) -> None:
    """Only the Policy identity reaches the configured Product and Pricing gRPC ports."""
    data = items["ConfigMap"]["data"]
    addresses = [data["PRODUCT_GRPC_TARGET"], data["PRICING_GRPC_TARGET"]]
    allowed = run_probe(
        target,
        "policy-provider-allowed",
        "provider allow",
        ['for address do allowed "$address" || exit 1; echo "allowed $address"; done', *addresses],
        labels={**SELECTOR, "environment": target.environment},
    )
    require(
        allowed == [f"allowed {address}" for address in addresses],
        "Policy identity cannot reach a provider",
    )
    denied = run_probe(
        target,
        "policy-provider-denied",
        "provider denial",
        ['for address do denied "$address" || exit 1; echo "denied $address"; done', *addresses],
    )
    require(
        denied == [f"denied {address}" for address in addresses],
        "an unrelated workload reaches a provider",
    )
    print("Policy provider access passed for the Policy identity and was denied otherwise")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=sorted(lifecycle.OPERATIONS | {"render", "check"}))
    operation = parser.parse_args().operation
    if operation == "render":
        environment = os.environ.get("DEPLOY_ENV", "")
        text = render(environment)
        validate_render(environment, text)
        sys.stdout.write(text)
    elif operation == "check":
        check_render()
        print("Policy local-dev and QA renders passed")
    else:
        return lifecycle.execute(sys.modules[__name__], [operation])
    return 0


class Terminated(KeyboardInterrupt):
    """Handled SIGTERM after cleanup."""


def interrupt(_signal: int, _frame: object) -> None:
    raise Terminated


def cli() -> int:
    try:
        return main()
    except Terminated:
        return 143
    except KeyboardInterrupt:
        return 130
    except (
        ValueError,
        OSError,
        KeyError,
        CommandError,
        AdministrationError,
        lifecycle.DeploymentError,
        http.client.HTTPException,
    ) as error:
        print(f"policy-deployment: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupt)
    raise SystemExit(cli())
