#!/usr/bin/env python3
"""Run the Document foundation in one owned disposable Kubernetes namespace."""

from __future__ import annotations

import json
import os
import re
import secrets
import sys
import tempfile
import time
from contextlib import ExitStack
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

import document_deployment_checks as checks  # noqa: E402
import resource_administration as administration  # noqa: E402
from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.go.service.tooling_contracts import OwnedPortForward, PortForwardSpec, loopback_port  # noqa: E402
from scripts.python.process_runner import CommandError  # noqa: E402


class DisposableError(RuntimeError):
    """The owned disposable deployment did not satisfy its contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise DisposableError(message)


def prepared_resources(
    text: str, namespace: str, image: str, source_revision: str
) -> list[dict[str, Any]]:
    require(bool(re.fullmatch(r"document-smoke-[0-9a-f]{8}", namespace)), "namespace is not owned")
    require(
        bool(
            re.fullmatch(r"insurance-hub-document-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}", image)
        ),
        "candidate image identity differs",
    )
    require(
        bool(re.fullmatch(r"[0-9a-f]{40}", source_revision))
        and image.endswith(source_revision[:12]),
        "candidate source identity differs",
    )
    items = checks.validate_render("local-dev", text)
    version = "git-" + source_revision[:12]
    for item in items:
        item.setdefault("metadata", {})["namespace"] = namespace
    deployment = checks.resource(items, "Deployment", "local-dev-document-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = image
    deployment["metadata"]["labels"]["app.kubernetes.io/version"] = version
    deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = version
    checks.resource(items, "ConfigMap", "local-dev-document-config")["data"]["SERVICE_VERSION"] = (
        version
    )
    policy = checks.resource(items, "NetworkPolicy", "local-dev-document-api")
    for direction in ("ingress", "egress"):
        for rule in policy.get("spec", {}).get(direction, []):
            for peer in rule.get("from", []) + rule.get("to", []):
                selector = peer.get("namespaceSelector", {}).get("matchLabels", {})
                if "kubernetes.io/metadata.name" in selector:
                    selector["kubernetes.io/metadata.name"] = namespace
    return items


POSTGRES_PASSWORD = "synthetic-postgres-admin-362"
READER_PASSWORD = "synthetic-postgres-reader-362"
MINIO_ADMIN_PASSWORD = "synthetic-minio-admin-lookup-362"
MINIO_READER_PASSWORD = "synthetic-minio-reader-lookup-362"


def dependency_resources(namespace: str) -> list[dict[str, Any]]:
    postgres_image = os.environ.get("POSTGRES_IMAGE", "postgres:17.10-alpine")
    minio_image = os.environ.get(
        "MINIO_IMAGE",
        "cgr.dev/chainguard/minio@sha256:7ad2a5efd83f0ca1b8e00af9f7ec7fc3ff977c38ed29b942b0c47460ad2f8f3e",
    )
    metadata = lambda name: {"name": name, "namespace": namespace}  # noqa: E731
    return [
        {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": metadata("local-dev-postgres-document-reader-creds"),
            "stringData": {
                "database-url": (
                    "postgresql://go_document_reader:"
                    + READER_PASSWORD
                    + "@document-smoke-postgres:5432/document?sslmode=disable"
                ),
                "password": READER_PASSWORD,
                "ca.crt": "",
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": metadata("local-dev-minio-document-reader-creds"),
            "stringData": {
                "access-key": administration.USER,
                "secret-key": MINIO_READER_PASSWORD,
                "endpoint": "http://document-smoke-minio:9000",
                "ca.crt": "",
            },
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": metadata("document-smoke-postgres"),
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {"app": "document-smoke-postgres"}},
                "template": {
                    "metadata": {
                        "labels": {
                            "app": "document-smoke-postgres",
                            "cnpg.io/cluster": "local-dev-postgres-document",
                        }
                    },
                    "spec": {
                        "containers": [
                            {
                                "name": "postgres",
                                "image": postgres_image,
                                "imagePullPolicy": "IfNotPresent",
                                "env": [
                                    {"name": "POSTGRES_DB", "value": "document"},
                                    {"name": "POSTGRES_PASSWORD", "value": POSTGRES_PASSWORD},
                                ],
                                "ports": [{"name": "postgres", "containerPort": 5432}],
                                "readinessProbe": {
                                    "exec": {
                                        "command": [
                                            "pg_isready",
                                            "-h",
                                            "127.0.0.1",
                                            "-U",
                                            "postgres",
                                            "-d",
                                            "document",
                                        ]
                                    },
                                    "periodSeconds": 2,
                                    "timeoutSeconds": 2,
                                },
                            }
                        ]
                    },
                },
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": metadata("document-smoke-postgres"),
            "spec": {
                "selector": {"app": "document-smoke-postgres"},
                "ports": [{"name": "postgres", "port": 5432, "targetPort": "postgres"}],
            },
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": metadata("document-smoke-minio"),
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {"app": "document-smoke-minio"}},
                "template": {
                    "metadata": {"labels": {"app": "document-smoke-minio"}},
                    "spec": {
                        "securityContext": {"fsGroup": 65532},
                        "containers": [
                            {
                                "name": "minio",
                                "image": minio_image,
                                "imagePullPolicy": "IfNotPresent",
                                "command": ["minio"],
                                "args": ["server", "/data", "--address", ":9000"],
                                "env": [
                                    {"name": "MINIO_ROOT_USER", "value": "fixture-admin"},
                                    {
                                        "name": "MINIO_ROOT_PASSWORD",
                                        "value": MINIO_ADMIN_PASSWORD,
                                    },
                                ],
                                "ports": [{"name": "minio", "containerPort": 9000}],
                                "volumeMounts": [{"name": "data", "mountPath": "/data"}],
                            }
                        ],
                        "volumes": [{"name": "data", "emptyDir": {}}],
                    },
                },
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": metadata("document-smoke-minio"),
            "spec": {
                "selector": {"app": "document-smoke-minio"},
                "ports": [{"name": "minio", "port": 9000, "targetPort": "minio"}],
            },
        },
    ]


def initialize_postgres(context: str, namespace: str) -> None:
    sql = f"""
CREATE ROLE document NOLOGIN;
CREATE ROLE go_document_reader LOGIN PASSWORD '{READER_PASSWORD}' NOINHERIT;
GRANT CREATE ON SCHEMA public TO document;
SET ROLE document;
CREATE SEQUENCE public.hibernate_sequence AS bigint;
CREATE TABLE public.policy_document(id bigint PRIMARY KEY, bytes bytea, policy_number varchar(255));
INSERT INTO public.policy_document(id,bytes,policy_number) VALUES
  (1,decode('255044462d333632','hex'),'SYN-362'),
  (2,convert_to('2026/02/SYN-362.pdf','UTF8'),'SYN-362'),
  (3,convert_to('2026/02/missing.pdf','UTF8'),'SYN-362'),
  (4,''::bytea,'SYN-362'),
  (5,decode('255044462d333632','hex'),'SYN-362');
RESET ROLE;
GRANT CONNECT ON DATABASE document TO go_document_reader;
GRANT USAGE ON SCHEMA public TO go_document_reader;
GRANT SELECT ON public.policy_document TO go_document_reader;
"""
    kubectl(
        context,
        namespace,
        "exec",
        "deployment/document-smoke-postgres",
        "-i",
        "--",
        "psql",
        "-X",
        "-v",
        "ON_ERROR_STOP=1",
        "-U",
        "postgres",
        "-d",
        "document",
        input_text=sql,
    )


def initialize_minio(context: str, namespace: str) -> None:
    port = loopback_port()
    with ExitStack() as stack:
        stack.enter_context(
            OwnedPortForward(
                PortForwardSpec(
                    context=context,
                    namespace=namespace,
                    resource="service/document-smoke-minio",
                    local_port=port,
                    remote_port=9000,
                ),
                cwd=ROOT,
                env=checks.safe_environment(),
                startup_seconds=20,
                teardown_seconds=5,
                kubectl=os.environ.get("KUBECTL", "kubectl"),
            )
        )
        with tempfile.TemporaryDirectory(prefix="document-smoke-mc-") as directory:
            root = Path(directory)
            root.chmod(0o700)
            config = root / "config.json"
            config.write_text(
                json.dumps(
                    {
                        "version": "10",
                        "aliases": {
                            "document": {
                                "url": f"http://127.0.0.1:{port}",
                                "accessKey": "fixture-admin",
                                "secretKey": MINIO_ADMIN_PASSWORD,
                                "api": "S3v4",
                                "path": "auto",
                            }
                        },
                    }
                )
            )
            config.chmod(0o600)
            prefix = ["mc", "--config-dir", str(root)]
            checks.run([*prefix, "ready", "document"], timeout=30)
            checks.run([*prefix, "mb", "--ignore-existing", "document/policies"])
            policy = root / "policy.json"
            policy.write_text(json.dumps(administration.POLICY))
            checks.run(
                [
                    *prefix,
                    "admin",
                    "policy",
                    "create",
                    "document",
                    administration.POLICY_NAME,
                    str(policy),
                ]
            )
            checks.run(
                [*prefix, "admin", "user", "add", "document"],
                input_text=f"{administration.USER}\n{MINIO_READER_PASSWORD}\n",
            )
            checks.run(
                [
                    *prefix,
                    "admin",
                    "policy",
                    "attach",
                    "document",
                    administration.POLICY_NAME,
                    "--user",
                    administration.USER,
                ]
            )
            fixture = root / "fixture.pdf"
            fixture.write_bytes(b"%PDF-OBJECT-362")
            checks.run([*prefix, "cp", str(fixture), "document/policies/2026/02/SYN-362.pdf"])


def kubectl(
    context: str, namespace: str, *arguments: str, input_text: str | None = None, timeout: int = 60
) -> str:
    binary = os.environ.get("KUBECTL", "kubectl")
    return checks.run(
        [binary, "--context", context, "--namespace", namespace, *arguments],
        input_text=input_text,
        timeout=timeout,
    )


def wait_for_namespace_absence(context: str, namespace: str) -> None:
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        result = kubectl(context, "default", "get", "namespace", namespace, "--ignore-not-found")
        if not result.strip():
            return
        time.sleep(1)
    raise DisposableError("disposable namespace cleanup exceeded 90 seconds")


def qualification_target() -> tuple[str, str]:
    record = service_record(ROOT, "document")
    target = target_record(ROOT, record, "local-dev")
    require(target["mode"] == "direct", "qualification target must use direct ownership")
    context = target["context"]
    cluster = target.get("kindCluster")
    require(isinstance(cluster, str) and bool(cluster), "qualification Kind cluster is missing")
    require(
        os.environ.get("QUALIFICATION_CONTEXT", context) == context,
        "qualification context differs from the catalog",
    )
    require(
        os.environ.get("QUALIFICATION_KIND_CLUSTER", cluster) == cluster,
        "qualification Kind cluster differs from the catalog",
    )
    return context, cluster


def main() -> int:
    image = os.environ.get("IMAGE_REF", "")
    source_revision = os.environ.get("QUALIFICATION_SOURCE_REVISION", "")
    context, cluster = qualification_target()
    namespace = "document-smoke-" + secrets.token_hex(4)
    rendered = checks.render("local-dev")
    items = prepared_resources(rendered, namespace, image, source_revision)
    namespace_manifest = yaml.safe_dump(
        {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {
                "name": namespace,
                "labels": {"insurance-hub.io/document-smoke": "owned"},
            },
        },
        sort_keys=False,
    )
    created = False
    try:
        kubectl(context, "default", "apply", "-f", "-", input_text=namespace_manifest)
        created = True
        checks.run(["kind", "load", "docker-image", image, "--name", cluster], timeout=120)
        kubectl(
            context,
            namespace,
            "apply",
            "--server-side",
            "--field-manager=insurance-hub-document-smoke",
            "-f",
            "-",
            input_text=yaml.safe_dump_all(
                dependency_resources(namespace), explicit_start=True, sort_keys=False
            ),
        )
        for dependency in ("document-smoke-postgres", "document-smoke-minio"):
            kubectl(
                context,
                namespace,
                "rollout",
                "status",
                f"deployment/{dependency}",
                "--timeout=180s",
                timeout=210,
            )
        initialize_postgres(context, namespace)
        initialize_minio(context, namespace)
        kubectl(
            context,
            namespace,
            "apply",
            "--server-side",
            "--field-manager=insurance-hub-document-smoke",
            "-f",
            "-",
            input_text=yaml.safe_dump_all(items, explicit_start=True, sort_keys=False),
        )
        kubectl(
            context,
            namespace,
            "rollout",
            "status",
            "deployment/local-dev-document-api",
            "--timeout=180s",
            timeout=210,
        )
        target = object.__new__(checks.Target)
        target.environment = "local-dev"
        target.context = context
        target.namespace = namespace
        target.data_namespace = namespace
        target.deployment = "local-dev-document-api"
        target.business = "local-dev-document-api"
        target.management = "local-dev-document-api-management"
        target.kubectl_binary = os.environ.get("KUBECTL", "kubectl")
        target.configuration = {"mode": "direct"}
        target.json = lambda *args: json.loads(
            kubectl(context, namespace, *args, "-o", "json") or "{}"
        )
        checks.verify(target, items)
        print("document-disposable: lookup, dependencies, health and denied Pod access passed")
        return 0
    finally:
        if created:
            try:
                kubectl(
                    context,
                    "default",
                    "delete",
                    "namespace",
                    namespace,
                    "--wait=false",
                    timeout=30,
                )
                wait_for_namespace_absence(context, namespace)
            except (CommandError, DisposableError) as error:
                print(f"document-disposable: cleanup failed: {error}", file=sys.stderr)
                raise


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (CommandError, DisposableError, checks.DeploymentError, OSError, ValueError) as error:
        print(f"document-disposable: {error}", file=sys.stderr)
        raise SystemExit(1) from error
