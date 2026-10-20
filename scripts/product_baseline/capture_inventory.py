#!/usr/bin/env python3
"""Capture a sanitized Product Kubernetes and PostgreSQL inventory."""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from scripts.product_baseline import capture, fixtures, preflight

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SENSITIVE = re.compile(r"password|secret|token|credential|private|api[_-]?key", re.IGNORECASE)


@dataclass(frozen=True)
class InventoryTarget:
    context: str
    service_namespace: str
    data_namespace: str
    product_deployment: str
    product_service: str
    gateway_deployment: str
    gateway_service: str
    database_cluster: str
    database_secret: str
    scenario: str
    product_overlay: str
    gateway_overlay: str
    product_image: str
    gateway_image: str
    database_host: str


TARGETS = {
    "local-dev": InventoryTarget(
        "kind-local-dev-insurance-hub",
        "local-dev-all",
        "local-dev-all",
        "local-dev-product-api-legacy",
        "local-dev-product-api-legacy",
        "local-dev-agent-portal-gateway-legacy",
        "local-dev-agent-portal-gateway-legacy",
        "local-dev-postgres-product",
        "local-dev-postgres-product-user-creds",
        "INV-LOCAL-001",
        "k8s/overlays/local-dev/svc/product/legacy/deployment-patch.yaml",
        "k8s/overlays/local-dev/svc/agent-portal-gateway/legacy/deployment-patch.yaml",
        "insurance-hub-product-api-legacy:latest",
        "insurance-hub-agent-portal-gateway-legacy:latest",
        "local-dev-postgres-product-rw.local-dev-all.svc.cluster.local",
    ),
    "qa": InventoryTarget(
        "qa-insurance-hub",
        "qa-svc",
        "qa-data",
        "qa-product-api-legacy",
        "qa-product-api-legacy",
        "qa-agent-portal-gateway-legacy",
        "qa-agent-portal-gateway-legacy",
        "qa-postgres-product",
        "qa-postgres-product-user-creds",
        "INV-QA-001",
        "k8s/overlays/qa/svc/product/legacy/deployment-patch.yaml",
        "k8s/overlays/qa/svc/agent-portal-gateway/legacy/deployment-patch.yaml",
        "ghcr.io/igor-baiborodine/insurance-hub/product-api-legacy:1.4.0",
        "ghcr.io/igor-baiborodine/insurance-hub/agent-portal-gateway-legacy:1.0.0",
        "qa-postgres-product-rw.qa-data.svc.cluster.local",
    ),
}


def kubectl(
    target: InventoryTarget, *arguments: str, allow_missing: bool = False
) -> dict[str, Any] | None:
    try:
        output = preflight.command(
            ["kubectl", f"--context={target.context}", *arguments], timeout=45
        )
    except preflight.PreflightError:
        if allow_missing:
            return None
        raise
    try:
        value = json.loads(output)
    except json.JSONDecodeError as error:
        raise capture.CaptureError(f"kubectl returned malformed JSON for {arguments[0]}") from error
    capture.require(isinstance(value, dict), "kubectl inventory response must be an object")
    return value


def sanitized_environment(deployment: dict[str, Any], container_name: str) -> list[dict[str, Any]]:
    container = next(
        item
        for item in deployment["spec"]["template"]["spec"]["containers"]
        if item.get("name") == container_name
    )
    result = []
    for variable in container.get("env", []):
        record: dict[str, Any] = {"name": variable.get("name")}
        if "value" in variable:
            record["value"] = (
                "<redacted>" if SENSITIVE.search(str(variable.get("name"))) else variable["value"]
            )
        else:
            source = variable.get("valueFrom", {})
            if "secretKeyRef" in source:
                reference = source["secretKeyRef"]
                record["secretKeyRef"] = {
                    "name": reference.get("name"),
                    "key": reference.get("key"),
                    "optional": reference.get("optional", False),
                }
            elif "configMapKeyRef" in source:
                reference = source["configMapKeyRef"]
                record["configMapKeyRef"] = {
                    "name": reference.get("name"),
                    "key": reference.get("key"),
                    "optional": reference.get("optional", False),
                }
            elif "fieldRef" in source:
                record["fieldRef"] = source["fieldRef"].get("fieldPath")
            else:
                record["valueFrom"] = "other"
        result.append(record)
    return result


def workload(
    deployment: dict[str, Any], pods: dict[str, Any], container_name: str
) -> dict[str, Any]:
    container = next(
        item
        for item in deployment["spec"]["template"]["spec"]["containers"]
        if item.get("name") == container_name
    )
    pod_records = []
    for pod in pods.get("items", []):
        status = next(
            (
                item
                for item in pod.get("status", {}).get("containerStatuses", [])
                if item.get("name") == container_name
            ),
            {},
        )
        ready = next(
            (
                item.get("status")
                for item in pod.get("status", {}).get("conditions", [])
                if item.get("type") == "Ready"
            ),
            "False",
        )
        pod_records.append(
            {
                "name": pod.get("metadata", {}).get("name"),
                "node": pod.get("spec", {}).get("nodeName"),
                "phase": pod.get("status", {}).get("phase"),
                "ready": ready,
                "image": status.get("image"),
                "imageDigest": status.get("imageID"),
            }
        )
    return {
        "deployment": deployment["metadata"]["name"],
        "generation": deployment["metadata"].get("generation"),
        "observedGeneration": deployment.get("status", {}).get("observedGeneration"),
        "desiredReplicas": deployment["spec"].get("replicas", 1),
        "readyReplicas": deployment.get("status", {}).get("readyReplicas", 0),
        "availableReplicas": deployment.get("status", {}).get("availableReplicas", 0),
        "deploymentRevision": deployment["metadata"]
        .get("annotations", {})
        .get("deployment.kubernetes.io/revision"),
        "strategy": deployment["spec"].get("strategy"),
        "container": {
            key: container.get(key)
            for key in (
                "name",
                "image",
                "imagePullPolicy",
                "ports",
                "readinessProbe",
                "livenessProbe",
                "envFrom",
            )
        }
        | {"environment": sanitized_environment(deployment, container_name)},
        "pods": pod_records,
    }


def service_record(service: dict[str, Any], endpoints: dict[str, Any]) -> dict[str, Any]:
    addresses = {
        address
        for item in endpoints.get("items", [])
        for endpoint in item.get("endpoints", [])
        if endpoint.get("conditions", {}).get("ready") is not False
        for address in endpoint.get("addresses", [])
    }
    return {
        "name": service["metadata"]["name"],
        "type": service["spec"].get("type"),
        "clusterIP": service["spec"].get("clusterIP"),
        "ports": service["spec"].get("ports"),
        "readyEndpointAddresses": sorted(addresses),
    }


def flux_record(
    kustomization: dict[str, Any] | None, source: dict[str, Any] | None
) -> dict[str, Any]:
    kustomization_record = None
    if kustomization is not None:
        specification = kustomization.get("spec", {})
        status = kustomization.get("status", {})
        kustomization_record = {
            "name": kustomization.get("metadata", {}).get("name"),
            "sourceRef": specification.get("sourceRef"),
            "path": specification.get("path"),
            "interval": specification.get("interval"),
            "retryInterval": specification.get("retryInterval"),
            "prune": specification.get("prune"),
            "lastAppliedRevision": status.get("lastAppliedRevision"),
            "lastAttemptedRevision": status.get("lastAttemptedRevision"),
            "conditions": status.get("conditions"),
        }
    source_record = None
    if source is not None:
        specification = source.get("spec", {})
        source_record = {
            "name": source.get("metadata", {}).get("name"),
            "url": specification.get("url"),
            "branch": specification.get("ref", {}).get("branch"),
            "interval": specification.get("interval"),
            "artifactRevision": source.get("status", {}).get("artifact", {}).get("revision"),
        }
    return {
        "apiAvailable": kustomization is not None or source is not None,
        "qaServiceKustomization": kustomization_record,
        "source": source_record,
    }


def main(_argv: Sequence[str] | None = None) -> int:
    selected_environment = os.environ.get("BASELINE_ENV", "")
    try:
        capture.require(
            os.environ.get("BASELINE_PART") == "inventory",
            "BASELINE_PART must be 'inventory' for the implemented capture capability.",
        )
        capture.require(
            selected_environment in TARGETS,
            f"Unsupported BASELINE_ENV '{selected_environment}'; use local-dev or qa.",
        )
        target = TARGETS[selected_environment]
        with tempfile.TemporaryDirectory(prefix="product-inventory-") as directory:
            postgres = capture.run_preflight(Path(directory) / "postgres.json", catalog=False)
        deployment_product = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "deployment",
            target.product_deployment,
            "-o",
            "json",
        )
        deployment_gateway = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "deployment",
            target.gateway_deployment,
            "-o",
            "json",
        )
        service_product = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "service",
            target.product_service,
            "-o",
            "json",
        )
        service_gateway = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "service",
            target.gateway_service,
            "-o",
            "json",
        )
        endpoints_product = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "endpointslices.discovery.k8s.io",
            f"--selector=kubernetes.io/service-name={target.product_service}",
            "-o",
            "json",
        )
        endpoints_gateway = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "endpointslices.discovery.k8s.io",
            f"--selector=kubernetes.io/service-name={target.gateway_service}",
            "-o",
            "json",
        )
        pods_product = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "pods",
            "-l=app.kubernetes.io/name=product-api-legacy",
            "-o",
            "json",
        )
        pods_gateway = kubectl(
            target,
            "-n",
            target.service_namespace,
            "get",
            "pods",
            "-l=app.kubernetes.io/name=agent-portal-gateway-legacy",
            "-o",
            "json",
        )
        cluster = kubectl(
            target,
            "-n",
            target.data_namespace,
            "get",
            "clusters.postgresql.cnpg.io",
            target.database_cluster,
            "-o",
            "json",
        )
        config_name = next(
            item["configMapRef"]["name"]
            for item in deployment_product["spec"]["template"]["spec"]["containers"]
            if item.get("name") == "product-api-legacy"
            for item in item.get("envFrom", [])
            if "configMapRef" in item
        )
        config = kubectl(
            target, "-n", target.service_namespace, "get", "configmap", config_name, "-o", "json"
        )
        flux_kustomization = None
        flux_source = None
        try:
            resources = preflight.command(
                [
                    "kubectl",
                    f"--context={target.context}",
                    "api-resources",
                    "--api-group=kustomize.toolkit.fluxcd.io",
                    "-o",
                    "name",
                ],
                timeout=30,
            ).splitlines()
            if "kustomizations.kustomize.toolkit.fluxcd.io" in resources:
                flux_kustomization = kubectl(
                    target,
                    "-n",
                    "flux-system",
                    "get",
                    "kustomizations.kustomize.toolkit.fluxcd.io",
                    "qa-svc",
                    "-o",
                    "json",
                    allow_missing=True,
                )
            resources = preflight.command(
                [
                    "kubectl",
                    f"--context={target.context}",
                    "api-resources",
                    "--api-group=source.toolkit.fluxcd.io",
                    "-o",
                    "name",
                ],
                timeout=30,
            ).splitlines()
            if "gitrepositories.source.toolkit.fluxcd.io" in resources:
                flux_source = kubectl(
                    target,
                    "-n",
                    "flux-system",
                    "get",
                    "gitrepositories.source.toolkit.fluxcd.io",
                    "insurance-hub",
                    "-o",
                    "json",
                    allow_missing=True,
                )
        except preflight.PreflightError:
            pass
        product = workload(deployment_product, pods_product, "product-api-legacy")
        gateway = workload(deployment_gateway, pods_gateway, "agent-portal-gateway-legacy")
        sanitized_config = {
            key: "<redacted>" if SENSITIVE.search(key) else value
            for key, value in config.get("data", {}).items()
        }
        checked_in = [
            "k8s/apps/svc/product/base/legacy/deployment.yaml",
            target.product_overlay,
            target.gateway_overlay,
            "k8s/apps/infra/postgres/base/cluster.yaml",
            f"k8s/overlays/{selected_environment}/infra/postgres/product/cluster-patch.yaml",
            "k8s/Makefile",
        ]
        if selected_environment == "qa":
            checked_in.extend(
                [
                    "k8s/flux/qa/flux-system/kustomization-qa-svc.yaml",
                    "k8s/flux/qa/flux-system/gotk-sync.yaml",
                ]
            )
        secret_references = {target.database_secret}
        for current in (product, gateway):
            for variable in current["container"]["environment"]:
                reference = variable.get("secretKeyRef", {}).get("name")
                if reference:
                    secret_references.add(reference)
        differences = []
        for field, expected, observed in (
            ("productImage", target.product_image, product["container"]["image"]),
            ("gatewayImage", target.gateway_image, gateway["container"]["image"]),
            ("productDatabaseHost", target.database_host, sanitized_config.get("PG_HOST")),
            (
                "postgresImage",
                "ghcr.io/cloudnative-pg/postgresql:17",
                cluster["spec"].get("imageName"),
            ),
        ):
            if observed != expected:
                differences.append({"field": field, "checkedIn": expected, "observed": observed})
        flux = flux_record(flux_kustomization, flux_source)
        if selected_environment == "qa" and not flux["apiAvailable"]:
            differences.append(
                {
                    "field": "fluxRuntimeApi",
                    "checkedIn": "QA reconciliation resources under k8s/flux/qa",
                    "observed": "Flux APIs unavailable in the active QA cluster",
                }
            )
        document = {
            "schemaVersion": 1,
            "scenarioId": target.scenario,
            "status": "captured",
            "captureProfile": "inventory",
            "provenance": "captured-observation",
            "credentialCategory": "database-capture",
            "capturedAt": postgres["observedAt"],
            "captureClock": "Target PostgreSQL clock at the read-only inventory query",
            "captureCommand": f"make product-baseline-capture BASELINE_ENV={selected_environment} BASELINE_PART=inventory",
            "environment": selected_environment,
            "sourceRevision": capture.source_revision(),
            "kubernetes": {
                "context": target.context,
                "namespaces": {"service": target.service_namespace, "data": target.data_namespace},
                "workloads": {"product": product, "gateway": gateway},
                "services": {
                    "product": service_record(service_product, endpoints_product),
                    "gateway": service_record(service_gateway, endpoints_gateway),
                },
                "productConfiguration": {"configMap": config_name, "data": sanitized_config},
                "postgresCluster": {
                    "name": cluster["metadata"]["name"],
                    "generation": cluster["metadata"].get("generation"),
                    "observedGeneration": cluster.get("status", {}).get("observedGeneration"),
                    "instances": cluster["spec"].get("instances"),
                    "image": cluster["spec"].get("imageName"),
                    "currentPrimary": cluster.get("status", {}).get("currentPrimary"),
                    "readyInstances": cluster.get("status", {}).get("readyInstances"),
                    "phase": cluster.get("status", {}).get("phase"),
                    "bootstrap": {
                        "database": cluster["spec"]["bootstrap"]["initdb"]["database"],
                        "owner": cluster["spec"]["bootstrap"]["initdb"]["owner"],
                        "secretReference": cluster["spec"]["bootstrap"]["initdb"]["secret"]["name"],
                    },
                },
                "flux": flux,
            },
            "postgres": postgres,
            "connection": {
                "endpoint": (
                    f"{preflight.optional_environment('BASELINE_PRODUCT_DB_HOST', '127.0.0.1')}:"
                    f"{preflight.optional_environment('BASELINE_PRODUCT_DB_PORT', '5492')}"
                ),
                "forwardVerified": True,
                "transactionReadOnly": postgres["sessionReadOnly"],
            },
            "ownership": {
                "runtimeManagers": {
                    "services": deployment_product["metadata"]
                    .get("labels", {})
                    .get("app.kubernetes.io/managed-by"),
                    "databaseOperator": "cloudnative-pg",
                },
                "checkedInResources": checked_in,
                "secretReferences": sorted(secret_references),
                "writers": [
                    {
                        "owner": "legacy Product service",
                        "mechanism": "DataLoader inserts missing CAR, FAI, HSI, and TRI on ServerStartupEvent",
                        "source": "legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/init/DataLoader.java",
                    },
                    {
                        "owner": "legacy Product service Hibernate startup",
                        "mechanism": "hibernate.hbm2ddl.auto=update",
                        "source": "legacy/product-service/src/main/resources/application.yml",
                    },
                    {
                        "owner": "CloudNativePG bootstrap/admin identity",
                        "mechanism": "creates the product database and product owner from a Secret reference",
                        "source": f"k8s/overlays/{selected_environment}/infra/postgres/product/cluster-patch.yaml",
                    },
                ],
            },
            "checkedInExpectations": {
                "productImage": target.product_image,
                "gatewayImage": target.gateway_image,
                "productDatabaseHost": target.database_host,
                "postgresImage": "ghcr.io/cloudnative-pg/postgresql:17",
                "productTable": {
                    "owner": "product",
                    "columns": [
                        {
                            "name": "code",
                            "underlyingType": "varchar",
                            "characterMaximumLength": 255,
                            "nullable": False,
                        },
                        {
                            "name": "definition",
                            "underlyingType": "jsonb",
                            "characterMaximumLength": None,
                            "nullable": False,
                        },
                    ],
                    "primaryKey": "PRIMARY KEY (code)",
                },
            },
            "differencesFromCheckedInSources": differences,
            "sanitizationRecord": {
                "secretValuesCaptured": False,
                "bearerTokensCaptured": False,
                "catalogDefinitionsCaptured": False,
                "retainedSensitiveReferences": "Secret names and keys only",
                "temporaryCredentialHandling": "Database password held only in process memory and removed after the read-only query",
            },
        }
        destination = fixtures.FIXTURE_ROOT / "inventory" / f"{selected_environment}.json"
        capture.store(destination, document)
        fixtures.validate_part("inventory", explicit_file=str(destination))
    except (
        capture.CaptureError,
        fixtures.FixtureError,
        preflight.PreflightError,
        OSError,
        KeyError,
        StopIteration,
        TypeError,
        json.JSONDecodeError,
    ) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    print(
        f"Captured sanitized {selected_environment} Product inventory at {destination.relative_to(REPOSITORY_ROOT)}."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
