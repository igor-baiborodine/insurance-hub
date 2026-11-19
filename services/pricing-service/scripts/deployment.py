"""Guarded deployment and private verification for Go Pricing."""

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
from typing import Any

import yaml
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
SELECTOR = {"app.kubernetes.io/name": "pricing-api", "app.kubernetes.io/component": "api"}
VERSION_LABEL = "app.kubernetes.io/version"
PLACEHOLDER = "git-000000000000"
PORTS = {"http": 8080, "grpc": 9090, "management": 8081}
DNS_EGRESS = ("kube-system", {"k8s-app": "kube-dns"}, [(53, "UDP"), (53, "TCP")])
ALLOY_EGRESS = (
    "qa-monitoring",
    {"app.kubernetes.io/name": "alloy", "app.kubernetes.io/instance": "qa-alloy"},
    [(4317, "TCP")],
)
DATABASE_EGRESS = {
    "local-dev": (
        "local-dev-all",
        {"cnpg.io/cluster": "local-dev-postgres-pricing"},
        [(5432, "TCP")],
    ),
    "qa": ("qa-data", {"cnpg.io/cluster": "qa-postgres-pricing"}, [(5432, "TCP")]),
}
PROBE_IMAGE = (
    "docker.io/curlimages/curl:8.16.0@"
    "sha256:463eaf6072688fe96ac64fa623fe73e1dbe25d8ad6c34404a669ad3ce1f104b6"
)
PROBE_OWNER_LABEL = "insurance-hub.io/pricing-denial-owner"
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
        activity="Pricing deployment command",
        input_text=input_text,
    ).stdout


def source_version(revision: str) -> str:
    require(bool(re.fullmatch(r"[0-9a-f]{40}", revision)), "full image source revision is required")
    version = f"git-{revision[:12]}"
    require(version != PLACEHOLDER, "placeholder source revision is forbidden")
    return version


def expected_policy(environment: str) -> dict:
    destinations = (
        (DNS_EGRESS, DATABASE_EGRESS[environment], ALLOY_EGRESS)
        if environment == "qa"
        else (DNS_EGRESS, DATABASE_EGRESS[environment])
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
        "ingress": [],
        "egress": egress,
    }


def policy_spec(spec: dict) -> dict:
    return {"ingress": [], "egress": [], **spec}


def render(environment: str) -> str:
    record = service_record(ROOT, "pricing")
    target = target_record(ROOT, record, environment)
    return run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(ROOT / target["overlay"])])


def render_administration(environment: str) -> str:
    record = service_record(ROOT, "pricing")
    target = target_record(ROOT, record, environment)
    return run(
        [
            os.environ.get("KUBECTL", "kubectl"),
            "kustomize",
            str(ROOT / target["initialization"]["overlay"]),
        ]
    )


def validate_render(environment: str, text: str) -> dict[str, dict]:
    target = target_record(ROOT, service_record(ROOT, "pricing"), environment)
    documents = list(yaml.safe_load_all(text))
    kinds = {
        "ConfigMap": "pricing-config",
        "Deployment": "pricing-api",
        "NetworkPolicy": "pricing-api",
    }
    require(
        len(documents) == len(kinds)
        and all(isinstance(item, dict) for item in documents)
        and {item.get("kind") for item in documents} == kinds.keys(),
        "Pricing render must contain exactly its Deployment, ConfigMap and NetworkPolicy",
    )
    items = {item["kind"]: item for item in documents}
    for kind, suffix in kinds.items():
        metadata = items[kind]["metadata"]
        require(
            metadata["name"] == f"{environment}-{suffix}"
            and metadata["namespace"] == target["namespace"],
            "Pricing resource name or namespace differs",
        )

    deployment = items["Deployment"]
    spec = deployment["spec"]
    template = spec["template"]
    pod = template["spec"]
    require(
        spec["selector"] == {"matchLabels": SELECTOR}
        and all(template["metadata"]["labels"].get(k) == v for k, v in SELECTOR.items())
        and spec["replicas"] == 1
        and spec["strategy"] == {"type": "Recreate"},
        "Pricing selector, labels or rollout policy differs",
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
        "Pricing Pod isolation or security context differs",
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
            "volumeMounts",
            "resources",
            "startupProbe",
            "livenessProbe",
            "readinessProbe",
        }
        and container["name"] == "pricing-api"
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
                "name": "PRICING_DATABASE_URL",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"{environment}-postgres-pricing-reader-creds",
                        "key": "database-url",
                    }
                },
            },
            {
                "name": "SERVICE_INSTANCE_ID",
                "valueFrom": {"fieldRef": {"apiVersion": "v1", "fieldPath": "metadata.uid"}},
            },
        ]
        and container["envFrom"] == [{"configMapRef": {"name": f"{environment}-pricing-config"}}]
        and container["volumeMounts"]
        == [{"name": "postgres-ca", "mountPath": "/etc/pricing-service/postgres", "readOnly": True}]
        and pod["volumes"]
        == [
            {
                "name": "postgres-ca",
                "secret": {
                    "secretName": f"{environment}-postgres-pricing-reader-creds",
                    "items": [{"key": "ca.crt", "path": "ca.crt"}],
                },
            }
        ]
        and container["resources"]
        == {
            "requests": {"cpu": "50m", "memory": "32Mi"},
            "limits": {"cpu": "200m", "memory": "128Mi"},
        },
        "Pricing container security, ports, resources or identity binding differs",
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
            "Pricing management probe differs",
        )
    data = items["ConfigMap"]["data"]
    version = data.get("SERVICE_VERSION", "")
    expected_data = {
        "SERVICE_NAME": "pricing-service",
        "DEPLOYMENT_ENVIRONMENT": environment,
        "HTTP_ADDR": "127.0.0.1:8080",
        "GRPC_ADDR": "127.0.0.1:9090",
        "HEALTH_ADDR": "0.0.0.0:8081",
        "DB_MAX_CONNS": "8",
        "DB_CONNECT_TIMEOUT": "3s",
        "DB_ACQUIRE_TIMEOUT": "2s",
        "DB_QUERY_TIMEOUT": "5s",
        "STARTUP_TIMEOUT": "10s",
        "REQUEST_TIMEOUT": "8s",
        "PROBE_TIMEOUT": "2s",
        "SERVICE_VERSION": version,
        "OTEL_ENABLED": "true" if environment == "qa" else "false",
    }
    if environment == "qa":
        expected_data["OTEL_EXPORTER_OTLP_ENDPOINT"] = (
            "http://qa-alloy.qa-monitoring.svc.cluster.local:4317"
        )
    require(
        data == expected_data
        and bool(re.fullmatch(r"git-[0-9a-f]{12}|\$\{PRICING_SERVICE_VERSION\}", version))
        and all(
            metadata["labels"].get(VERSION_LABEL) == version
            for metadata in (deployment["metadata"], template["metadata"])
        ),
        "Pricing runtime config or version labels differ",
    )
    image = container["image"]
    if environment == "qa":
        registry = "ghcr.io/igor-baiborodine/insurance-hub/pricing-api"
        require(
            (
                version == "${PRICING_SERVICE_VERSION}"
                and image == f"{registry}:${{PRICING_SERVICE_VERSION}}"
            )
            or (
                bool(re.fullmatch(r"git-[0-9a-f]{12}", version))
                and bool(
                    re.fullmatch(
                        rf"{re.escape(registry)}(?:"
                        rf":(?:1\.[0-9]+\.[0-9]+|{re.escape(version)})"
                        rf"|@sha256:[0-9a-f]{{64}})",
                        image,
                    )
                )
            ),
            "QA image and release version differ",
        )
    else:
        require(
            image == f"insurance-hub-pricing-api:1.0.0-local.{version[4:]}",
            "local image and release version differ",
        )
    require(
        policy_spec(items["NetworkPolicy"]["spec"]) == expected_policy(environment),
        "Pricing NetworkPolicy must deny Pod ingress and allow only the accepted egress",
    )
    return items


def validate_administration(environment: str, text: str) -> dict:
    target = target_record(ROOT, service_record(ROOT, "pricing"), environment)
    documents = list(yaml.safe_load_all(text))
    require(
        len(documents) == 1 and documents[0].get("kind") == "Job",
        "Pricing administration render must contain exactly one Job",
    )
    job = documents[0]
    require(
        job["metadata"]["name"] == f"{environment}-pricing-init"
        and job["metadata"]["namespace"] == target["namespace"]
        and job["spec"]["backoffLimit"] == 0
        and job["spec"]["activeDeadlineSeconds"] == 300,
        "Pricing administration Job identity or retry budget differs",
    )
    pod = job["spec"]["template"]["spec"]
    require(
        pod["automountServiceAccountToken"] is False
        and pod["enableServiceLinks"] is False
        and pod["restartPolicy"] == "Never"
        and len(pod["initContainers"]) == 1
        and len(pod["containers"]) == 1,
        "Pricing administration Pod isolation or execution order differs",
    )
    migrate = pod["initContainers"][0]
    seed = pod["containers"][0]
    require(
        migrate["name"] == "migrate"
        and migrate["command"] == ["/pricing-migrate"]
        and seed["name"] == "seed"
        and seed["command"] == ["/pricing-seed"]
        and migrate["image"] == seed["image"]
        and {mount["name"] for mount in migrate["volumeMounts"]} == {"target", "migrator"}
        and {mount["name"] for mount in seed["volumeMounts"]} == {"target", "seeder"},
        "Pricing administration must migrate before seed with operation-specific credentials",
    )
    expected_timeout = "${MIGRATION_TIMEOUT}" if environment == "qa" else "120s"
    require(
        migrate["args"]
        == [
            "--operation=migrate",
            f"--environment={environment}",
            f"--context={target['context']}",
            "--target-file=/etc/pricing-admin/target/target.json",
            "--database-url-file=/etc/pricing-admin/migrator/database-url",
            "--ca-file=/etc/pricing-admin/migrator/ca.crt",
            f"--timeout={expected_timeout}",
        ]
        and seed["args"]
        == [
            f"--environment={environment}",
            f"--context={target['context']}",
            "--target-file=/etc/pricing-admin/target/target.json",
            "--database-url-file=/etc/pricing-admin/seeder/database-url",
            "--ca-file=/etc/pricing-admin/seeder/ca.crt",
        ],
        "Pricing administration target or bounded command arguments differ",
    )
    secrets = {volume["name"]: volume["secret"]["secretName"] for volume in pod["volumes"]}
    require(
        secrets
        == {
            "target": f"{environment}-postgres-pricing-admin-target",
            "migrator": f"{environment}-postgres-pricing-migrator-creds",
            "seeder": f"{environment}-postgres-pricing-seeder-creds",
        }
        and f"{environment}-postgres-pricing-reader-creds" not in secrets.values(),
        "Pricing administration Secret binding differs",
    )
    return job


def qa_release_ownership() -> None:
    resources = yaml.safe_load((ROOT / "k8s/overlays/qa/svc/kustomization.yaml").read_text())[
        "resources"
    ]
    require(
        [resource for resource in resources if resource.startswith("./pricing/")]
        == ["./pricing/legacy"],
        "QA legacy Pricing must remain the only nested Pricing resource",
    )
    require("./pricing" not in resources, "shared QA ownership must not activate Pricing Go")
    flux_resources = yaml.safe_load((ROOT / "k8s/flux/qa/kustomization.yaml").read_text())[
        "resources"
    ]
    require(
        flux_resources.count("./releases/pricing/release.yaml") == 1,
        "QA Pricing release owner must be included exactly once",
    )
    bundle = list(
        yaml.safe_load_all((ROOT / "k8s/flux/qa/releases/pricing/release.yaml").read_text())
    )
    api = [item for item in bundle if item.get("metadata", {}).get("name") == "qa-pricing-api"]
    sources = {item["metadata"]["name"] for item in bundle if item.get("kind") == "GitRepository"}
    initialization = {
        item["metadata"]["name"]: item
        for item in bundle
        if item.get("kind") == "Kustomization"
        and item.get("spec", {}).get("path", "").endswith("/administration")
    }
    require(
        len(api) == 1 and bool(initialization) and api[0]["spec"]["sourceRef"]["name"] in sources,
        "QA Pricing release bundle must contain one API owner and its sources",
    )
    # The staged predecessor's image predates the administration commands; it must never run.
    require(
        all(
            item["spec"].get("suspend") is True
            for item in initialization.values()
            if item["metadata"].get("annotations", {}).get("insurance-hub.io/staged-predecessor")
            == "true"
        ),
        "staged Pricing predecessor must not run initialization",
    )
    dependencies = api[0]["spec"].get("dependsOn")
    if dependencies is None:
        require(
            all(item["spec"].get("suspend") is True for item in initialization.values()),
            "staged Pricing release must retain the old API without running initialization",
        )
        return
    prerequisite = (
        initialization.get(dependencies[0].get("name")) if len(dependencies) == 1 else None
    )
    require(
        prerequisite is not None
        and prerequisite["spec"].get("suspend") is not True
        and prerequisite["spec"]["sourceRef"] == api[0]["spec"]["sourceRef"],
        "QA Pricing API must depend on its own release initialization",
    )


def check_render() -> None:
    for environment in ("local-dev", "qa"):
        validate_render(environment, render(environment))
        validate_administration(environment, render_administration(environment))
    qa_release_ownership()


class Target:
    service = "pricing"

    def __init__(self) -> None:
        self.record = service_record(ROOT, self.service)
        self.environment = os.environ.get("DEPLOY_ENV", "")
        self.configuration = target_record(ROOT, self.record, self.environment)
        self.context = self.configuration["context"]
        self.namespace = self.configuration["namespace"]
        self.data_namespace = self.configuration["dataNamespace"]
        for key, expected in (
            ("DEPLOY_CONTEXT", self.context),
            ("DEPLOY_SVC_NS", self.namespace),
            ("DEPLOY_DATA_NS", self.data_namespace),
        ):
            require(os.environ.get(key) == expected, f"{key} must explicitly equal {expected}")
        self.deployment = f"{self.environment}-pricing-api"
        self.secret = f"{self.environment}-postgres-pricing-reader-creds"
        self.administration_overlay = ROOT / self.configuration["initialization"]["overlay"]
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
        data_namespace = self.json("get", "namespace", self.data_namespace)
        require(
            data_namespace.get("metadata", {}).get("name") == self.data_namespace
            and data_namespace.get("status", {}).get("phase") == "Active",
            "target data namespace is not active",
        )

    def rendered(self) -> tuple[str, dict[str, dict], str]:
        rendered = render(self.environment)
        if self.configuration["mode"] == "flux" and "@sha256:" in os.environ.get(
            "DEPLOY_IMAGE_REF", ""
        ):
            for key, value in lifecycle.release_substitutions(self).items():
                rendered = rendered.replace(f"${{{key}}}", value)
        items = validate_render(self.environment, rendered)
        deployment = items["Deployment"]
        template = deployment["spec"]["template"]
        container = template["spec"]["containers"][0]
        config = items["ConfigMap"]["data"]
        if self.configuration["mode"] == "direct":
            version = source_version(os.environ.get("DEPLOY_IMAGE_SOURCE_REVISION", ""))
            image = os.environ.get("DEPLOY_IMAGE_REF", "")
            require(
                image == f"{self.record['localImage']}:1.0.0-local.{version[4:]}",
                "select the exact local Pricing image for the source revision",
            )
            container["image"] = image
            config["SERVICE_VERSION"] = version
            for metadata in (deployment["metadata"], template["metadata"]):
                metadata["labels"][VERSION_LABEL] = version
        else:
            require(
                "@sha256:" in os.environ.get("DEPLOY_IMAGE_REF", ""),
                "select the immutable reviewed QA image",
            )
            container["image"] = lifecycle.release_image(self)
            require(
                container["image"] == os.environ["DEPLOY_IMAGE_REF"],
                "QA image differs from the reviewed release",
            )
        self.template_labels = template["metadata"]["labels"]
        return yaml.safe_dump_all(items.values(), explicit_start=True), items, container["image"]

    def live(self, items: dict[str, dict], *, require_deployment: bool) -> dict:
        deployment = self.json("get", "deployment", self.deployment, "--ignore-not-found")
        for kind in ("Deployment", "ConfigMap", "NetworkPolicy"):
            desired = items[kind]
            actual = (
                deployment
                if kind == "Deployment"
                else self.json(
                    "get", kind.lower(), desired["metadata"]["name"], "--ignore-not-found"
                )
            )
            require(actual or not require_deployment, f"Pricing {kind} is missing")
            if not actual:
                continue
            metadata = actual.get("metadata", {})
            require(
                metadata.get("namespace") == self.namespace
                and metadata.get("labels", {}).get("app.kubernetes.io/name") == "pricing-api"
                and metadata.get("labels", {}).get("app.kubernetes.io/managed-by") == "kustomize",
                f"existing {kind} is outside Pricing ownership",
            )
            if kind == "Deployment":
                require(
                    actual["spec"]["selector"] == {"matchLabels": SELECTOR}, "live selector differs"
                )
            if require_deployment and kind == "ConfigMap":
                require(actual.get("data") == desired["data"], "live Pricing configuration differs")
            if require_deployment and kind == "NetworkPolicy":
                require(
                    policy_spec(actual["spec"]) == desired["spec"], "live Pricing policy differs"
                )
        check_requested_route(self)
        for policy in self.json("get", "networkpolicies").get("items", []):
            if policy["metadata"]["name"] == self.deployment:
                continue
            selector = policy["spec"]["podSelector"].get("matchLabels", {})
            require(
                not selector.items() <= self.template_labels.items(),
                "unexpected NetworkPolicy may permit Pricing access",
            )
        if not require_deployment:
            return {}
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
            "Pricing Deployment release identity or readiness differs",
        )
        selector = ",".join(f"{key}={value}" for key, value in SELECTOR.items())
        pods = [
            pod
            for pod in self.json("get", "pods", "-l", selector).get("items", [])
            if not pod["metadata"].get("deletionTimestamp")
        ]
        require(len(pods) == 1, "expected exactly one selected Pricing Pod")
        pod = pods[0]
        statuses = pod["status"].get("containerStatuses", [])
        digest = image_id(self, image)
        expected_ids = {digest, f"{image.split(':', 1)[0]}@{digest}", f"containerd://{digest}"}
        require(
            pod["metadata"]["labels"].get(VERSION_LABEL) == version
            and pod["spec"]["containers"][0]["image"] == image
            and len(statuses) == 1
            and statuses[0].get("name") == "pricing-api"
            and statuses[0].get("ready") is True
            and statuses[0].get("imageID") in expected_ids,
            "Pricing Pod image identity or readiness differs",
        )
        print(
            f"Pricing Pod {pod['metadata']['name']} UID={pod['metadata']['uid']} "
            f"version={version} image={image} imageID={statuses[0]['imageID']}"
        )
        return pod


def image_id(target: Target, image: str) -> str:
    flux = target.configuration["mode"] == "flux"
    digest = os.environ.get("DEPLOY_IMAGE_DIGEST", "") if flux else ""
    if flux:
        require(valid_digest(digest), "recorded publication digest is required")
        image = f"{target.record['registryImage']}@{digest}"
    records = json.loads(run([os.environ.get("DOCKER", "docker"), "image", "inspect", image]))
    require(len(records) == 1, "Pricing image inspection differs")
    labels = records[0]["Config"]["Labels"]
    revision = os.environ.get("DEPLOY_IMAGE_SOURCE_REVISION", "")
    require(
        labels.get("org.opencontainers.image.revision") == revision
        and labels.get("org.opencontainers.image.version") == source_version(revision),
        "Pricing OCI labels differ from the selected source",
    )
    if flux:
        require(image in records[0].get("RepoDigests", []), "Pricing publication digest differs")
        return digest
    result = records[0]["Id"]
    require(valid_digest(result), "exact Pricing image ID is required")
    return result


def valid_digest(value: str) -> bool:
    return bool(re.fullmatch(r"sha256:[0-9a-f]{64}", value)) and value != "sha256:" + "0" * 64


def secret_and_image(target: Target, image: str) -> None:
    secret = target.json("get", "secret", target.secret)
    require(
        secret.get("kind") == "Secret"
        and set(secret.get("data", {})) == {"database-url", "ca.crt"},
        "reader Secret is absent or has unexpected keys",
    )
    image_id(target, image)


def resource_environment(target: Target) -> dict[str, str]:
    values = safe_environment()
    values.update(
        {
            "PRICING_RESOURCE_ENV": target.environment,
            "PRICING_RESOURCE_CONTEXT": target.context,
            "PRICING_RESOURCE_DATA_NS": target.data_namespace,
            "PRICING_RESOURCE_SVC_NS": target.namespace,
            "PRICING_RESOURCE_CLUSTER": f"{target.environment}-postgres-pricing",
            "PRICING_RESOURCE_READER_SECRET": target.secret,
            "PRICING_RESOURCE_DB_HOST": (
                f"{target.environment}-postgres-pricing-rw."
                f"{target.data_namespace}.svc.cluster.local"
            ),
            "PYTHONPATH": str(ROOT),
            "PYTHONDONTWRITEBYTECODE": "1",
        }
    )
    return values


def resource_action(target: Target, action: str) -> str:
    require(action in {"inspect", "verify"}, "unsupported read-only resource action")
    result = run_command(
        [
            os.environ.get("MAKE_COMMAND", "make"),
            "--no-print-directory",
            "-C",
            str(ROOT / "services/pricing-service"),
            f"resource-{action}",
        ],
        cwd=ROOT,
        env=resource_environment(target),
        timeout_seconds=180,
        teardown_seconds=10,
        activity=f"Pricing resource {action}",
    )
    return result.stdout.strip()


def resource_snapshot(target: Target) -> dict[str, Any]:
    try:
        value = json.loads(resource_action(target, "inspect"))
    except ValueError as error:
        raise lifecycle.DeploymentError("resource inspection is malformed") from error
    require(
        isinstance(value, dict)
        and set(value)
        == {
            "roles",
            "memberships",
            "target",
            "databaseCompatible",
            "credentials",
            "markerMatchesEnrollment",
        }
        and isinstance(value["roles"], list)
        and isinstance(value["memberships"], list)
        and (value["target"] is None or isinstance(value["target"], dict))
        and isinstance(value["databaseCompatible"], bool)
        and isinstance(value["credentials"], dict)
        and set(value["credentials"])
        == {"go_pricing_reader", "go_pricing_migrator", "go_pricing_seeder"}
        and set(value["credentials"].values()) <= {"missing", "present", "CA differs"}
        and isinstance(value["markerMatchesEnrollment"], bool),
        "resource inspection has unexpected state",
    )
    return value


def administration_snapshot(target: Target) -> dict[str, Any]:
    return resource_snapshot(target)


def role_snapshot(target: Target) -> dict[str, Any]:
    return resource_snapshot(target)


def record_snapshot(stage: str, value: dict[str, Any]) -> None:
    print(f"pricing-deployment: {stage} catalog identity " + json.dumps(value, sort_keys=True))


def initialization_check(target: Target) -> None:
    resource_action(target, "verify")


def initialization_complete(target: Target) -> None:
    initialization_check(target)
    state = resource_snapshot(target)
    require(
        state["databaseCompatible"] is True and state["markerMatchesEnrollment"] is True,
        "Pricing initialization is absent or differs from target enrollment",
    )


def check_requested_route(target: Target) -> None:
    # A namespaced Service is required for ordinary Ingress and Gateway routes.
    for service in target.json("get", "services").get("items", []):
        selector = service["spec"].get("selector") or {}
        require(
            not (selector and selector.items() <= target.template_labels.items())
            and service["metadata"]["name"] != target.deployment,
            "unexpected Service may expose Pricing",
        )


def rollout(target: Target) -> None:
    target.kubectl(
        "rollout", "status", f"deployment/{target.deployment}", "--timeout=180s", timeout=195
    )


def stop(target: Target, items: dict[str, dict]) -> None:
    target.live(items, require_deployment=False)
    before = resource_snapshot(target)
    record_snapshot("before stop", before)
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
            after = resource_snapshot(target)
            record_snapshot("after stop", after)
            require(after == before, "Pricing protected state changed during local stop")
            return
        time.sleep(max(0, min(1, deadline - time.monotonic())))
    raise lifecycle.DeploymentError("Pricing stop exceeded 90 seconds")


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


def verify(target: Target, items: dict[str, dict]) -> None:
    probe = os.environ.get("DEPLOY_DENIAL_PROBE", "0")
    require(probe in {"0", "1"}, "DEPLOY_DENIAL_PROBE must be 0 or 1")
    pod = target.live(items, require_deployment=True)
    before = resource_snapshot(target)
    record_snapshot("before verification", before)
    resource_action(target, "verify")
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
            "Pricing liveness or readiness response differs",
        )
        status, body = response(ports["http"], "/v1/example/echo", b'{"message":"hello"}')
        require(
            status == 200 and json.loads(body) == {"message": "hello"}, "Pricing HTTP Echo differs"
        )
        run(
            [
                os.environ.get("MAKE_COMMAND", "make"),
                "-C",
                str(ROOT / target.record["modulePath"]),
                "_deploy-smoke-rpc",
                f"GO={os.environ.get('GO', 'go')}",
                f"PRICING_SMOKE_GRPC_ADDR=127.0.0.1:{ports['grpc']}",
            ],
            timeout=120,
        )
    logs = target.kubectl("logs", pod["metadata"]["name"], "-c", "pricing-api")
    records = [json.loads(line) for line in logs.splitlines()]
    expected = {
        "service_name": "pricing-service",
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
        "Pricing runtime identity differs",
    )
    print(
        f"Pricing HTTP Echo, direct gRPC Echo, liveness, readiness and identity passed: {expected}"
    )
    if probe == "1":
        verify_denial(target, pod)
    after = resource_snapshot(target)
    record_snapshot("after verification", after)
    require(after == before, "Pricing protected state changed during verification")


def verify_denial(target: Target, pod: dict) -> None:
    name = "pricing-access-denied"
    token = uuid.uuid4().hex
    require(
        not target.json("get", "pod", name, "--ignore-not-found"), "Pricing probe name is occupied"
    )
    ip = str(IPv4Address(pod["status"]["podIP"]))
    probe = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": name,
            "namespace": target.namespace,
            "labels": {PROBE_OWNER_LABEL: token},
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
                        "address=$1; shift; for port do "
                        "status=0; output=$(curl --verbose --connect-timeout 2 --max-time 3 "
                        '"telnet://$address:$port" 2>&1) || status=$?; '
                        'case "$output" in *"Connected to"*) exit 1;; esac; '
                        '[ "$status" = 7 ] || [ "$status" = 28 ]; '
                        'echo "Pricing port $port denied"; done',
                        "probe",
                        ip,
                        *(str(port) for port in PORTS.values()),
                    ],
                }
            ],
        },
    }
    try:
        target.kubectl("create", "-f", "-", input_text=json.dumps(probe))
        target.kubectl(
            "wait",
            "--for=jsonpath={.status.phase}=Succeeded",
            f"pod/{name}",
            "--timeout=90s",
            timeout=100,
        )
        observed = target.kubectl("logs", name).splitlines()
        require(
            observed == [f"Pricing port {port} denied" for port in PORTS.values()],
            "Pricing workload denial result differs",
        )
        print("\n".join(observed))
    finally:
        primary_failure = sys.exception()
        try:
            actual = target.json("get", "pod", name, "--ignore-not-found")
            if actual:
                require(
                    actual["metadata"].get("labels", {}).get(PROBE_OWNER_LABEL) == token,
                    "Pricing denial probe ownership differs",
                )
                target.kubectl("delete", "pod", name, "--timeout=45s", timeout=55)
        except Exception as cleanup_error:
            if primary_failure is None:
                raise
            print(
                f"pricing-deployment: denial probe cleanup failed: {cleanup_error}", file=sys.stderr
            )


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
        print("Pricing local-dev and QA renders passed")
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
        print(f"pricing-deployment: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupt)
    raise SystemExit(cli())
