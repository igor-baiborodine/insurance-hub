#!/usr/bin/env python3
"""Exercise Product release gating with real Flux and CNPG in an owned Kind cluster."""

from __future__ import annotations

import base64
import functools
import http.server
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from copy import deepcopy
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))
from scripts.go.service.configuration import service_record  # noqa: E402
from scripts.go.service.release import (  # noqa: E402
    release_documents,
    retained_release_documents,
    service_release,
)

if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.python.process_runner import CommandError, run_command  # noqa: E402

KIND_IMAGE = "kindest/node@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a"
CNPG_MANIFEST = (
    "https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v1.27.0/cnpg-1.27.0.yaml"
)
POSTGRES_IMAGE = (
    "ghcr.io/cloudnative-pg/postgresql:17@"
    "sha256:70664ebcfa1100361b5bdc28bbf06fdbe08db2dc4ad7bd14de33c5e05fe8ea8e"
)
FLUX_COMPONENTS = ROOT / "k8s/flux/qa/flux-system/gotk-components.yaml"
BASE_JOB = ROOT / "k8s/apps/svc/product/base/administration/job.yaml"
CATALOG_PATH = "/usr/share/insurance-hub/product/catalogs/base.json"


class TestFailure(RuntimeError):
    """Disposable controller behavior differed from the release contract."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise TestFailure(message)


class Cluster:
    def __init__(self, name: str, namespace: str):
        self.name = name
        self.context = f"kind-{name}"
        self.namespace = namespace

    def command(
        self,
        arguments: list[str],
        *,
        activity: str,
        timeout: int = 120,
        input_text: str | None = None,
    ) -> str:
        result = run_command(
            arguments,
            cwd=ROOT,
            env=os.environ,
            timeout_seconds=timeout,
            teardown_seconds=10,
            activity=activity,
            input_text=input_text,
        )
        return result.stdout.strip()

    def kubectl(
        self,
        *arguments: str,
        activity: str = "kubectl operation",
        timeout: int = 120,
        input_text: str | None = None,
    ) -> str:
        return self.command(
            ["kubectl", "--context", self.context, *arguments],
            activity=activity,
            timeout=timeout,
            input_text=input_text,
        )

    def apply(self, document: dict[str, Any]) -> None:
        if document["kind"] != "Secret":
            result = subprocess.run(
                ["kubectl", "--context", self.context, "apply", "-f", "-"],
                cwd=ROOT,
                env=os.environ,
                input=json.dumps(document),
                text=True,
                capture_output=True,
                timeout=120,
                check=False,
            )
            if result.returncode != 0:
                diagnostic = result.stderr.strip() or result.stdout.strip()
                raise TestFailure(
                    f"apply {document['kind']}/{document['metadata']['name']} failed: {diagnostic}"
                )
            return
        self.kubectl(
            "apply",
            "-f",
            "-",
            activity=f"apply {document['kind']}/{document['metadata']['name']}",
            input_text=json.dumps(document),
        )

    def object(self, kind: str, name: str, namespace: str | None = None) -> dict[str, Any]:
        arguments = []
        if namespace:
            arguments.extend(("-n", namespace))
        arguments.extend(("get", kind, name, "-o", "json"))
        return json.loads(self.kubectl(*arguments, activity=f"inspect {kind}/{name}", timeout=30))

    def wait(self, description: str, predicate, seconds: int = 180) -> None:
        print(f"flux-product-test: waiting for {description}", flush=True)
        started = time.monotonic()
        deadline = started + seconds
        heartbeat = started + 30
        last_error = ""
        while time.monotonic() < deadline:
            try:
                if predicate():
                    print(f"PASS {description}", flush=True)
                    return
            except (CommandError, KeyError, json.JSONDecodeError) as error:
                last_error = str(error)
            if time.monotonic() >= heartbeat:
                print(
                    f"flux-product-test: {description} still pending after "
                    f"{time.monotonic() - started:.0f}s",
                    flush=True,
                )
                heartbeat = time.monotonic() + 30
            time.sleep(2)
        raise TestFailure(f"timed out waiting for {description}; {last_error}")

    def ready(self, kind: str, name: str, namespace: str) -> bool:
        item = self.object(kind, name, namespace)
        return any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in item.get("status", {}).get("conditions", [])
        )

    def deployment_available(self, name: str, namespace: str) -> bool:
        item = self.object("deployment", name, namespace)
        desired = int(item.get("spec", {}).get("replicas", 1))
        return int(item.get("status", {}).get("availableReplicas", 0)) >= desired


class GitHTTPHandler(http.server.BaseHTTPRequestHandler):
    def __init__(self, *args: object, project_root: Path, **kwargs: object):
        self.project_root = project_root
        super().__init__(*args, **kwargs)

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def do_GET(self) -> None:  # noqa: N802
        self._serve_git()

    def do_POST(self) -> None:  # noqa: N802
        self._serve_git()

    def _serve_git(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length) if length else b""
        target = urlsplit(self.path)
        environment = dict(os.environ)
        environment.update(
            {
                "GIT_PROJECT_ROOT": str(self.project_root),
                "GIT_HTTP_EXPORT_ALL": "1",
                "PATH_INFO": target.path,
                "QUERY_STRING": target.query,
                "REQUEST_METHOD": self.command,
                "CONTENT_TYPE": self.headers.get("Content-Type", ""),
                "CONTENT_LENGTH": str(length),
                "SERVER_PROTOCOL": self.protocol_version,
            }
        )
        result = subprocess.run(
            ["git", "http-backend"],
            cwd=ROOT,
            env=environment,
            input=body,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            timeout=30,
            check=False,
        )
        if result.returncode != 0:
            self.send_error(500, "git http-backend failed")
            return
        separator = b"\r\n\r\n" if b"\r\n\r\n" in result.stdout else b"\n\n"
        header_bytes, response_body = result.stdout.split(separator, 1)
        status = 200
        headers: list[tuple[str, str]] = []
        for raw_line in header_bytes.decode("latin-1").splitlines():
            key, value = raw_line.split(":", 1)
            if key.lower() == "status":
                status = int(value.strip().split(" ", 1)[0])
            else:
                headers.append((key, value.strip()))
        self.send_response(status)
        for key, value in headers:
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(response_body)


class GitSource:
    def __init__(self, cluster: Cluster, root: Path, image: str):
        self.cluster = cluster
        self.root = root
        self.repository = root / "source"
        self.image = image
        self.server: http.server.ThreadingHTTPServer | None = None
        self.thread: threading.Thread | None = None
        self.cluster.command(
            ["git", "init", "--initial-branch=main", str(self.repository)],
            activity="initialize disposable release source",
            timeout=30,
        )
        for key, value in (
            ("user.name", "Product Flux Test"),
            ("user.email", "test@example.invalid"),
        ):
            self.cluster.command(
                ["git", "-C", str(self.repository), "config", key, value],
                activity="configure disposable release source",
                timeout=30,
            )

    def write(self, release: str, *, transfer: bool) -> str:
        job = yaml.safe_load(BASE_JOB.read_text(encoding="utf-8"))
        job["metadata"]["namespace"] = self.cluster.namespace
        pod = job["spec"]["template"]["spec"]
        migrate = pod["initContainers"][0]
        seed = pod["containers"][0]
        migrate["image"] = seed["image"] = self.image
        migrate["imagePullPolicy"] = seed["imagePullPolicy"] = "Never"
        migrate["args"] = [
            "--operation=migrate",
            "--environment=disposable",
            f"--context={self.cluster.context}",
            "--target-file=/etc/product-admin/target/target.json",
            "--database-url-file=/etc/product-admin/migrator/database-url",
            "--ca-file=/etc/product-admin/migrator/ca.crt",
            "--timeout=120s",
        ]
        seed["args"] = [
            "--environment=disposable",
            f"--context={self.cluster.context}",
            "--target-file=/etc/product-admin/target/target.json",
            f"--catalog-file={CATALOG_PATH}",
            "--database-url-file=/etc/product-admin/seeder/database-url",
            "--ca-file=/etc/product-admin/seeder/ca.crt",
        ]
        for directory in ("init", "api", "shared"):
            path = self.repository / directory
            path.mkdir(parents=True, exist_ok=True)
            for child in path.iterdir():
                child.unlink()
        self._write_yaml("init/job.yaml", job)
        self._write_yaml(
            "init/kustomization.yaml",
            {
                "apiVersion": "kustomize.config.k8s.io/v1beta1",
                "kind": "Kustomization",
                "resources": ["job.yaml"],
            },
        )
        release_config = self._configmap("product-release-state", {"release": release})
        transferred = self._configmap("product-owned-resource", {"owner": "product"})
        if not transfer:
            transferred["metadata"]["annotations"] = {
                "kustomize.toolkit.fluxcd.io/prune": "disabled"
            }
        api_resources = ["release.yaml"]
        self._write_yaml("api/release.yaml", release_config)
        if transfer:
            self._write_yaml("api/product-owned.yaml", transferred)
            api_resources.append("product-owned.yaml")
        self._write_yaml(
            "api/kustomization.yaml",
            {
                "apiVersion": "kustomize.config.k8s.io/v1beta1",
                "kind": "Kustomization",
                "resources": api_resources,
            },
        )
        shared_resources = ["unrelated.yaml"]
        self._write_yaml(
            "shared/unrelated.yaml", self._configmap("unrelated-service-state", {"state": "ready"})
        )
        if not transfer:
            self._write_yaml("shared/product-owned.yaml", transferred)
            shared_resources.append("product-owned.yaml")
        self._write_yaml(
            "shared/kustomization.yaml",
            {
                "apiVersion": "kustomize.config.k8s.io/v1beta1",
                "kind": "Kustomization",
                "resources": shared_resources,
            },
        )
        return self._commit(f"release {release}")

    def write_bundle(self, documents: list[dict[str, Any]]) -> str:
        root = self.repository / "root"
        root.mkdir(parents=True, exist_ok=True)
        for child in root.iterdir():
            child.unlink()
        (root / "release.yaml").write_text(
            yaml.safe_dump_all(documents, explicit_start=True, sort_keys=False),
            encoding="utf-8",
        )
        self._write_yaml(
            "root/kustomization.yaml",
            {
                "apiVersion": "kustomize.config.k8s.io/v1beta1",
                "kind": "Kustomization",
                "resources": ["release.yaml"],
            },
        )
        return self._commit("replace root-owned release bundle")

    def _commit(self, message: str) -> str:
        self.cluster.command(
            ["git", "-C", str(self.repository), "add", "."],
            activity="stage disposable source",
            timeout=30,
        )
        self.cluster.command(
            ["git", "-C", str(self.repository), "commit", "-m", message],
            activity="commit disposable source",
            timeout=30,
        )
        self.cluster.command(
            ["git", "-C", str(self.repository), "update-server-info"],
            activity="publish disposable source metadata",
            timeout=30,
        )
        return self.cluster.command(
            ["git", "-C", str(self.repository), "rev-parse", "HEAD"],
            activity="resolve disposable source commit",
            timeout=30,
        )

    def _configmap(self, name: str, data: dict[str, str]) -> dict[str, Any]:
        return {
            "apiVersion": "v1",
            "kind": "ConfigMap",
            "metadata": {"name": name, "namespace": self.cluster.namespace},
            "data": data,
        }

    def _write_yaml(self, relative: str, document: dict[str, Any]) -> None:
        (self.repository / relative).write_text(
            yaml.safe_dump(document, sort_keys=False), encoding="utf-8"
        )

    def serve(self) -> str:
        with socket.socket() as probe:
            probe.bind(("0.0.0.0", 0))
            port = probe.getsockname()[1]
        handler = functools.partial(GitHTTPHandler, project_root=self.root)
        self.server = http.server.ThreadingHTTPServer(
            ("0.0.0.0", port),
            handler,  # nosec B104 - disposable Kind bridge endpoint
        )
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        gateway = self.cluster.command(
            [
                "docker",
                "network",
                "inspect",
                "kind",
                "--format",
                "{{(index .IPAM.Config 0).Gateway}}",
            ],
            activity="resolve Kind network gateway",
            timeout=30,
        )
        return f"http://{gateway}:{port}/source/.git"

    def close(self) -> None:
        if self.server is not None:
            self.server.shutdown()
            self.server.server_close()
        if self.thread is not None:
            self.thread.join(timeout=5)


def metadata(name: str, namespace: str = "flux-system") -> dict[str, str]:
    return {"name": name, "namespace": namespace}


def source_document(name: str, url: str, commit: str) -> dict[str, Any]:
    return {
        "apiVersion": "source.toolkit.fluxcd.io/v1",
        "kind": "GitRepository",
        "metadata": metadata(name),
        "spec": {"interval": "2s", "url": url, "ref": {"commit": commit}},
    }


def branch_source_document(name: str, url: str) -> dict[str, Any]:
    document = source_document(name, url, "0" * 40)
    document["spec"]["ref"] = {"branch": "main"}
    return document


def root_kustomization(name: str, source: str) -> dict[str, Any]:
    return {
        "apiVersion": "kustomize.toolkit.fluxcd.io/v1",
        "kind": "Kustomization",
        "metadata": metadata(name),
        "spec": {
            # The real root reconciles periodically on a long interval and reacts to source
            # revision events. Avoid continuously perturbing the child owners under test.
            "interval": "5m",
            "retryInterval": "2s",
            "timeout": "90s",
            "prune": True,
            "wait": False,
            "force": False,
            "sourceRef": {"kind": "GitRepository", "name": source},
            "path": "./root",
        },
    }


def kustomization(
    name: str,
    source: str,
    path: str,
    namespace: str,
    *,
    suffix: str = "",
    depends_on: str = "",
    health_job: str = "",
) -> dict[str, Any]:
    spec: dict[str, Any] = {
        "interval": "2s",
        "retryInterval": "2s",
        "timeout": "90s",
        "prune": True,
        "wait": True,
        "force": False,
        "sourceRef": {"kind": "GitRepository", "name": source},
        "path": path,
        "targetNamespace": namespace,
    }
    if suffix:
        spec["nameSuffix"] = suffix
    if depends_on:
        spec["dependsOn"] = [{"name": depends_on}]
    if health_job:
        spec["healthChecks"] = [
            {"apiVersion": "batch/v1", "kind": "Job", "name": health_job, "namespace": namespace}
        ]
    return {
        "apiVersion": "kustomize.toolkit.fluxcd.io/v1",
        "kind": "Kustomization",
        "metadata": metadata(name),
        "spec": spec,
    }


def secret_document(name: str, namespace: str, values: dict[str, bytes]) -> dict[str, Any]:
    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": metadata(name, namespace),
        "type": "Opaque",
        "data": {key: base64.b64encode(value).decode() for key, value in values.items()},
    }


def prepare_database(cluster: Cluster) -> tuple[str, dict[str, Any]]:
    namespace = cluster.namespace
    owner_password = secrets.token_urlsafe(48)
    cluster.apply(
        secret_document(
            "product-owner",
            namespace,
            {"username": b"product", "password": owner_password.encode()},
        )
    )
    cluster.apply(
        {
            "apiVersion": "postgresql.cnpg.io/v1",
            "kind": "Cluster",
            "metadata": metadata("product-db", namespace),
            "spec": {
                "instances": 1,
                "imageName": POSTGRES_IMAGE,
                "enableSuperuserAccess": True,
                "storage": {"size": "1Gi"},
                "bootstrap": {
                    "initdb": {
                        "database": "product",
                        "owner": "product",
                        "secret": {"name": "product-owner"},
                    }
                },
            },
        }
    )
    cluster.wait(
        "disposable CNPG cluster readiness",
        lambda: cluster.ready("cluster.postgresql.cnpg.io", "product-db", namespace),
        seconds=300,
    )
    record = cluster.object("cluster.postgresql.cnpg.io", "product-db", namespace)
    primary = record["status"]["currentPrimary"]
    uid = record["metadata"]["uid"]
    instance_id = str(uuid.uuid4())
    passwords = {role: secrets.token_urlsafe(48) for role in ("reader", "migrator", "seeder")}
    sql = f"""
CREATE ROLE go_product_reader LOGIN NOINHERIT PASSWORD '{passwords["reader"]}';
CREATE ROLE go_product_migrator LOGIN NOINHERIT PASSWORD '{passwords["migrator"]}';
CREATE ROLE go_product_seeder LOGIN NOINHERIT PASSWORD '{passwords["seeder"]}';
REVOKE ALL ON DATABASE product FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE product TO go_product_reader,go_product_migrator,go_product_seeder;
GRANT USAGE ON SCHEMA public TO go_product_reader,go_product_seeder;
GRANT product TO go_product_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
CREATE SCHEMA product_admin AUTHORIZATION postgres;
REVOKE ALL ON SCHEMA product_admin FROM PUBLIC;
CREATE TABLE product_admin.target_identity(singleton boolean PRIMARY KEY CHECK(singleton),
 environment text NOT NULL,instance_id uuid NOT NULL,cluster_uid text NOT NULL,
 seed_allowed boolean NOT NULL);
INSERT INTO product_admin.target_identity
 VALUES(true,'disposable','{instance_id}','{uid}',true);
GRANT USAGE ON SCHEMA product_admin TO product,go_product_migrator,go_product_seeder;
GRANT SELECT ON product_admin.target_identity TO product,go_product_migrator,go_product_seeder;
"""
    cluster.kubectl(
        "-n",
        namespace,
        "exec",
        "-i",
        primary,
        "--",
        "psql",
        "-X",
        "-v",
        "ON_ERROR_STOP=1",
        "-U",
        "postgres",
        "-d",
        "product",
        activity="provision disposable Product roles",
        input_text=sql,
    )
    ca_secret = cluster.object("secret", "product-db-ca", namespace)
    ca = base64.b64decode(ca_secret["data"]["ca.crt"], validate=True)
    host = f"product-db-rw.{namespace}.svc.cluster.local"
    mounts = {
        "reader": "/etc/product-service/postgres/ca.crt",
        "migrator": "/etc/product-admin/migrator/ca.crt",
        "seeder": "/etc/product-admin/seeder/ca.crt",
    }
    for role in ("reader", "migrator", "seeder"):
        login = f"go_product_{role}"
        url = (
            f"postgresql://{login}:{passwords[role]}@{host}:5432/product"
            f"?sslmode=verify-full&sslrootcert={mounts[role]}"
        )
        cluster.apply(
            secret_document(
                f"postgres-product-{role}-creds",
                namespace,
                {"database-url": url.encode(), "ca.crt": ca},
            )
        )
    target = {
        "environment": "disposable",
        "context": cluster.context,
        "dataNamespace": namespace,
        "serviceNamespace": namespace,
        "cluster": "product-db",
        "clusterUID": uid,
        "instanceID": instance_id,
        "database": "product",
        "host": host,
        "seedAllowed": True,
        "disposable": True,
    }
    cluster.apply(
        secret_document(
            "postgres-product-admin-target",
            namespace,
            {"target.json": json.dumps(target, sort_keys=True).encode()},
        )
    )
    return primary, passwords


def sql(cluster: Cluster, primary: str, statement: str) -> str:
    arguments = [
        "-n",
        cluster.namespace,
        "exec",
        "-i",
        primary,
        "--",
        "psql",
        "-X",
        "-qAt",
        "-v",
        "ON_ERROR_STOP=1",
        "-v",
        "VERBOSITY=verbose",
        "-U",
        "postgres",
        "-d",
        "product",
    ]
    return cluster.kubectl(
        *arguments,
        activity="inspect disposable SQL",
        timeout=45,
        input_text=statement,
    )


def sql_observation(cluster: Cluster, primary: str, statement: str) -> tuple[str, str]:
    result = run_command(
        [
            "kubectl",
            "--context",
            cluster.context,
            "-n",
            cluster.namespace,
            "exec",
            "-i",
            primary,
            "--",
            "psql",
            "-X",
            "-qAt",
            "-v",
            "ON_ERROR_STOP=1",
            "-v",
            "VERBOSITY=verbose",
            "-U",
            "postgres",
            "-d",
            "product",
        ],
        cwd=ROOT,
        env=os.environ,
        timeout_seconds=45,
        teardown_seconds=5,
        activity="observe disposable SQL",
        input_text=statement,
    )
    return result.stdout, result.stderr


def catalog_fingerprint(cluster: Cluster, primary: str) -> str:
    return sql(
        cluster,
        primary,
        "SELECT md5(string_agg(code||':'||definition::text,',' ORDER BY code)) FROM public.product;",
    )


def condition_status(cluster: Cluster, name: str) -> str:
    item = cluster.object("kustomization.kustomize.toolkit.fluxcd.io", name, "flux-system")
    for condition in item.get("status", {}).get("conditions", []):
        if condition.get("type") == "Ready":
            return str(condition.get("status"))
    return ""


def generated_release(
    cluster: Cluster, commit: str, digest: str, url: str, attempt: int
) -> tuple[str, list[dict], dict[str, Any]]:
    """Use the production generator with disposable coordinates and a short reconcile interval."""
    release = service_release(ROOT, "product", "qa", commit, digest)
    record = service_record(ROOT, "product")
    target = deepcopy(record["deploymentTargets"]["qa"])
    target["namespace"] = cluster.namespace
    target["overlay"] = "api"
    target["initialization"].update(overlay="init", jobName="product-init", attempt=attempt)
    target["flux"].update(sourcePrefix="product-release", apiName="product-api")
    documents = release_documents(release, record, target, "igor-baiborodine/insurance-hub")
    documents[0]["spec"]["url"] = url
    # Exercise periodic Job recreation within the fixture's bounded wait, without changing
    # the five-minute reconciliation interval generated for deployed targets.
    for item in documents:
        item["spec"]["interval"] = "2s"
    # Kind loads the exact local image ID under a local tag; no registry is used in this test.
    for item in documents[1:]:
        item["spec"].pop("images")
    return release.release_id, documents, target


def run() -> None:
    image = os.environ.get("IMAGE_REF", "")
    require(
        image.startswith("insurance-hub-product-api:"),
        "IMAGE_REF must select the local Product image",
    )
    suffix = secrets.token_hex(4)
    cluster = Cluster(f"issue190-flux-{suffix}", f"product-test-{suffix}")
    source: GitSource | None = None
    try:
        cluster.command(
            [
                "kind",
                "create",
                "cluster",
                "--name",
                cluster.name,
                "--image",
                KIND_IMAGE,
                "--wait",
                "120s",
            ],
            activity="create owned Kind cluster",
            timeout=180,
        )
        cluster.command(
            ["kind", "load", "docker-image", "--name", cluster.name, image],
            activity="load Product image into owned Kind cluster",
            timeout=180,
        )
        cluster.kubectl(
            "create", "namespace", cluster.namespace, activity="create owned test namespace"
        )
        cluster.kubectl(
            "apply",
            "--server-side",
            "-f",
            CNPG_MANIFEST,
            activity="install pinned CNPG controller",
            timeout=180,
        )
        cluster.kubectl(
            "apply",
            "-f",
            str(FLUX_COMPONENTS),
            activity="install pinned Flux controllers",
            timeout=180,
        )
        cluster.wait(
            "CNPG controller readiness",
            lambda: cluster.deployment_available("cnpg-controller-manager", "cnpg-system"),
            seconds=240,
        )
        for controller in ("source-controller", "kustomize-controller"):
            cluster.wait(
                f"Flux {controller} readiness",
                lambda controller=controller: cluster.deployment_available(
                    controller, "flux-system"
                ),
                seconds=240,
            )
        primary, passwords = prepare_database(cluster)
        with tempfile.TemporaryDirectory(prefix="product-flux-source-") as directory:
            source = GitSource(cluster, Path(directory), image)
            commit_a = source.write("release-a", transfer=False)
            url = source.serve()
            image_digest = cluster.command(
                ["docker", "image", "inspect", image, "--format", "{{.Id}}"],
                activity="inspect loaded image identity",
                timeout=30,
            ).strip()
            _, documents_a, target_a = generated_release(cluster, commit_a, image_digest, url, 1)
            bundle_a = retained_release_documents([], documents_a, target_a)
            source.write_bundle(bundle_a)
            source_a = documents_a[0]["metadata"]["name"]
            init_a = documents_a[1]["metadata"]["name"]
            root_source = "product-release-bundle"
            root_owner = "product-release-root"
            cluster.apply(branch_source_document(root_source, url))
            cluster.apply(root_kustomization(root_owner, root_source))
            cluster.apply(kustomization("shared-owner", source_a, "./shared", cluster.namespace))
            for name in (root_owner, "shared-owner", init_a, "product-api"):
                cluster.wait(
                    f"initial Flux Kustomization {name}",
                    lambda name=name: condition_status(cluster, name) == "True",
                    seconds=240,
                )
            marker = cluster.object("configmap", "product-release-state", cluster.namespace)
            require(
                marker["data"] == {"release": "release-a"}, "initial API release marker differs"
            )
            product_uid = cluster.object("configmap", "product-owned-resource", cluster.namespace)[
                "metadata"
            ]["uid"]
            unrelated_uid = cluster.object(
                "configmap", "unrelated-service-state", cluster.namespace
            )["metadata"]["uid"]
            fingerprint = catalog_fingerprint(cluster, primary)
            require(
                sql(
                    cluster,
                    primary,
                    "SET ROLE go_product_reader; SELECT count(*) FROM public.product; RESET ROLE;",
                )
                == "4",
                "reader cannot read the seeded catalog",
            )
            commit_b = source.write("release-b", transfer=True)
            id_b, documents_b, target_b = generated_release(cluster, commit_b, image_digest, url, 1)
            bundle_b = retained_release_documents(bundle_a, documents_b, target_b)
            source_b = f"product-release-{id_b}"
            init_b1 = f"product-init-{id_b}-a1"
            init_b2 = f"product-init-{id_b}-a2"
            bad_secret = cluster.object(
                "secret", "postgres-product-migrator-creds", cluster.namespace
            )
            good_data = dict(bad_secret["data"])
            decoded = base64.b64decode(good_data["database-url"]).decode()
            bad_secret["data"]["database-url"] = base64.b64encode(
                decoded.replace(passwords["migrator"], "wrong-password").encode()
            ).decode()
            bad_secret.pop("status", None)
            bad_secret["metadata"] = {
                "name": bad_secret["metadata"]["name"],
                "namespace": cluster.namespace,
            }
            cluster.apply(bad_secret)
            cluster.apply(kustomization("shared-owner", source_b, "./shared", cluster.namespace))
            source.write_bundle(bundle_b)
            cluster.wait(
                "failed new-release prerequisite",
                lambda: condition_status(cluster, init_b1) == "False",
                seconds=180,
            )
            marker = cluster.object("configmap", "product-release-state", cluster.namespace)
            require(
                marker["data"] == {"release": "release-a"}, "old success released new API state"
            )
            retained_success_uid = cluster.object("job", init_a, cluster.namespace)["metadata"][
                "uid"
            ]
            retained_failure_uid = cluster.object("job", init_b1, cluster.namespace)["metadata"][
                "uid"
            ]
            repaired = cluster.object(
                "secret", "postgres-product-migrator-creds", cluster.namespace
            )
            repaired["data"] = good_data
            repaired.pop("status", None)
            repaired["metadata"] = {
                "name": repaired["metadata"]["name"],
                "namespace": cluster.namespace,
            }
            cluster.apply(repaired)
            _, retry_documents, retry_target = generated_release(
                cluster, commit_b, image_digest, url, 2
            )
            retry_bundle = retained_release_documents(bundle_b, retry_documents, retry_target)
            source.write_bundle(retry_bundle)
            for name in ("shared-owner", init_b2, "product-api"):
                cluster.wait(
                    f"recovered Flux Kustomization {name}",
                    lambda name=name: condition_status(cluster, name) == "True",
                    seconds=240,
                )
            require(
                cluster.object("job", init_a, cluster.namespace)["metadata"]["uid"]
                == retained_success_uid,
                "root replacement pruned the previous successful Job",
            )
            require(
                cluster.object("job", init_b1, cluster.namespace)["metadata"]["uid"]
                == retained_failure_uid,
                "root replacement pruned the failed attempt Job",
            )
            require(
                cluster.object("configmap", "product-release-state", cluster.namespace)["data"]
                == {"release": "release-b"},
                "recovered API release marker differs",
            )
            require(
                cluster.object("configmap", "product-owned-resource", cluster.namespace)[
                    "metadata"
                ]["uid"]
                == product_uid,
                "Product ownership transfer changed resource UID",
            )
            require(
                cluster.object("configmap", "unrelated-service-state", cluster.namespace)[
                    "metadata"
                ]["uid"]
                == unrelated_uid,
                "unrelated resource changed during Product reconciliation",
            )
            require(
                catalog_fingerprint(cluster, primary) == fingerprint,
                "recovery changed catalog identity",
            )
            current_job = f"product-init-{id_b}-a2"
            old_uid = cluster.object("job", current_job, cluster.namespace)["metadata"]["uid"]
            cluster.kubectl(
                "-n",
                cluster.namespace,
                "delete",
                "job",
                current_job,
                activity="delete current prerequisite for recreation proof",
            )
            cluster.wait(
                "deleted prerequisite recreation and safe replay",
                lambda: (
                    cluster.object("job", current_job, cluster.namespace)["metadata"]["uid"]
                    != old_uid
                    and condition_status(cluster, init_b2) == "True"
                ),
                seconds=240,
            )
            require(
                catalog_fingerprint(cluster, primary) == fingerprint,
                "recreated Job changed catalog",
            )
            _, denial = sql_observation(
                cluster,
                primary,
                """
SET ROLE go_product_reader;
DO $probe$
DECLARE observed text := '00000';
BEGIN
  BEGIN
    INSERT INTO public.product VALUES('DENIED','{}');
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS observed = RETURNED_SQLSTATE;
  END;
  RAISE NOTICE 'permission-probe:%', observed;
END $probe$;
RESET ROLE;
""",
            )
            require(
                "permission-probe:42501" in denial, "reader write did not fail with SQLSTATE 42501"
            )
            print(
                "PASS root retention, old-success mismatch, failure, retry, recreation, "
                "ownership and isolation",
                flush=True,
            )
    finally:
        if source is not None:
            source.close()
        try:
            cluster.command(
                ["kind", "delete", "cluster", "--name", cluster.name],
                activity="delete owned Kind cluster",
                timeout=120,
            )
        except (CommandError, OSError) as error:
            raise TestFailure("owned Kind cluster cleanup failed") from error


def main() -> int:
    try:
        run()
    except (CommandError, OSError, TestFailure, KeyError, ValueError, yaml.YAMLError) as error:
        print(f"product-flux-test: {error}", file=os.sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
