#!/usr/bin/env python3
"""Exercise the Product image and rendered pod in an owned Kind namespace."""

from __future__ import annotations

import base64
from copy import deepcopy
import hashlib
import json
import os
from pathlib import Path
import select
import secrets
import subprocess
import sys
import tempfile
import time
from typing import Any, Callable
from urllib.error import HTTPError, URLError
from urllib.request import urlopen

from deployment import Target, forwards

ROOT = Path(__file__).resolve().parents[3]
CONTEXT = "kind-local-dev-insurance-hub"
KIND_CLUSTER = "local-dev-insurance-hub"
FIXTURE = ROOT / "legacy/product-service/src/test/resources/product-read-baseline/catalog/qa.json"
OVERLAY = ROOT / "k8s/overlays/local-dev/svc/product"
LABEL = "insurance-hub.io/disposable-test"


class CheckFailed(Exception):
    pass


def require(ok: bool, message: str) -> None:
    if not ok:
        raise CheckFailed(message)


def command(argv: list[str], *, stdin: str | None = None, timeout: int = 40) -> str:
    try:
        result = subprocess.run(argv, input=stdin, text=True, capture_output=True,
                                timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise CheckFailed(f"command unavailable or timed out: {argv[0]}") from error
    if result.returncode:
        # kubectl errors can echo Secret values. Keep credentials out of output.
        raise CheckFailed(f"command failed: {argv[0]} (exit {result.returncode})")
    return result.stdout


class Cluster:
    def __init__(self, namespace: str) -> None:
        self.namespace = namespace

    def kubectl(self, *args: str, stdin: str | None = None, timeout: int = 40) -> str:
        return command(["kubectl", "--context", CONTEXT, "--namespace", self.namespace,
                        *args], stdin=stdin, timeout=timeout)

    def json(self, *args: str) -> dict[str, Any]:
        return json.loads(self.kubectl(*args, "-o", "json"))

    def apply(self, obj: dict[str, Any]) -> None:
        self.kubectl("apply", "-f", "-", stdin=json.dumps(obj))

    def exists(self, kind: str, name: str) -> bool:
        return bool(self.kubectl("get", kind, name, "--ignore-not-found", "-o", "json").strip())

    def wait(self, description: str, predicate: Callable[[], bool], seconds: int = 90) -> None:
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if predicate():
                print(f"PASS {description}", flush=True)
                return
            time.sleep(2)
        raise CheckFailed(f"timed out: {description}")


def find(items: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [item for item in items if item["kind"] == kind and
               item["metadata"]["name"] == name]
    require(len(matches) == 1, f"rendered {kind}/{name} inventory differs")
    return matches[0]


def rendered(cluster: Cluster, image: str) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    yaml = cluster.kubectl("kustomize", str(OVERLAY))
    raw = command(["kubectl", "--context", CONTEXT, "--namespace", "local-dev-all",
                   "create", "--dry-run=client", "--validate=false", "-f", "-", "-o", "json"],
                  stdin=yaml)
    decoder = json.JSONDecoder()
    items: list[dict[str, Any]] = []
    remaining = raw.strip()
    while remaining:
        document, consumed = decoder.raw_decode(remaining)
        items.extend(document.get("items", []) if document.get("kind") == "List" else [document])
        remaining = remaining[consumed:].strip()
    require(len(items) == 4 and {item["kind"] for item in items} ==
            {"ConfigMap", "Service", "Deployment"}, "unexpected overlay inventory")
    deployment = find(items, "Deployment", "local-dev-product-api")
    spec = deployment["spec"]
    require(spec["replicas"] == 1 and spec["strategy"]["rollingUpdate"] ==
            {"maxSurge": 1, "maxUnavailable": 0}, "unexpected rollout bounds")
    pod = spec["template"]["spec"]
    require(pod["automountServiceAccountToken"] is False and
            pod["securityContext"]["runAsNonRoot"] is True and
            pod["containers"][0]["securityContext"]["readOnlyRootFilesystem"] is True,
            "unexpected pod security settings")
    for item in items:
        require(item["metadata"]["namespace"] == "local-dev-all", "unexpected overlay namespace")
        item["metadata"]["namespace"] = cluster.namespace
        item["metadata"].setdefault("labels", {})[LABEL] = cluster.namespace
    pod["containers"][0]["image"] = image
    return items, deployment


def secret(name: str, namespace: str, data: dict[str, str]) -> dict[str, Any]:
    return {"apiVersion": "v1", "kind": "Secret", "metadata": {
        "name": name, "namespace": namespace, "labels": {LABEL: namespace}},
        "type": "Opaque", "stringData": data}


def postgres(cluster: Cluster, name: str, sql: str) -> str:
    pods = cluster.json("get", "pods", "-l",
                        f"cnpg.io/cluster={name},cnpg.io/instanceRole=primary")["items"]
    require(len(pods) == 1, "disposable PostgreSQL pod is not unique")
    pod = pods[0]["metadata"]["name"]
    return cluster.kubectl("exec", "-i", pod, "--", "psql", "-X", "-v", "ON_ERROR_STOP=1",
                           "-U", "postgres", "-d", "product", "-At", stdin=sql, timeout=45)


def snapshot(cluster: Cluster, name: str) -> str:
    return postgres(cluster, name, "SELECT md5(coalesce(string_agg(code || definition::text, '' ORDER BY code), '')) FROM public.product;\n").strip()


def endpoints(cluster: Cluster, service: str) -> int:
    slices = cluster.json("get", "endpointslices", "-l", f"kubernetes.io/service-name={service}")["items"]
    return sum(1 for item in slices for endpoint in (item.get("endpoints") or [])
               if endpoint.get("conditions", {}).get("ready") is True)


def verify_catalog_reads(cluster: Cluster) -> None:
    target = Target.__new__(Target)
    target.context = CONTEXT
    target.namespace = cluster.namespace
    target.business = "local-dev-product-api"
    target.management = "local-dev-product-api-management"
    with tempfile.TemporaryDirectory(prefix="product-step6-verifier-") as directory:
        binary = str(Path(directory) / "security-verifier")
        command(["make", "-C", str(ROOT / "services/product-service"),
                 "build-security-verifier", f"SECURITY_VERIFIER_OUTPUT={binary}"], timeout=150)
        with forwards(target) as ports:
            output = command([binary, "-context", CONTEXT, "-namespace", cluster.namespace,
                              "-http", f"http://127.0.0.1:{ports['http']}",
                              "-grpc", f"127.0.0.1:{ports['grpc']}",
                              "-health", f"http://127.0.0.1:{ports['health']}",
                              "-accepted", str(ROOT / "legacy/product-service/src/test/resources/"
                                               "product-read-baseline/access/qa.json")], timeout=35)
    require("anonymous HTTP/gRPC catalog" in output, "Product verifier did not complete")
    print("PASS pod HTTP/gRPC and management reads match accepted QA access fixture", flush=True)


def verify_empty_catalog(cluster: Cluster) -> None:
    target = Target.__new__(Target)
    target.context = CONTEXT
    target.namespace = cluster.namespace
    target.business = "local-dev-product-api"
    target.management = "local-dev-product-api-management"
    with forwards(target) as ports, urlopen(
        f"http://127.0.0.1:{ports['http']}/products", timeout=5
    ) as response:
        require(response.status == 200 and json.load(response) == [],
                "empty catalog did not return a successful empty HTTP list")
    print("PASS empty catalog HTTP list is readable and empty", flush=True)


def verify_inflight_drain(cluster: Cluster, database: str, pod: str) -> None:
    target = Target.__new__(Target)
    target.context = CONTEXT
    target.namespace = cluster.namespace
    target.business = "local-dev-product-api"
    target.management = "local-dev-product-api-management"
    database_pods = cluster.json("get", "pods", "-l",
                                 f"cnpg.io/cluster={database},cnpg.io/instanceRole=primary")["items"]
    require(len(database_pods) == 1, "disposable database Pod is not unique")
    database_pod = database_pods[0]["metadata"]["name"]
    lock = subprocess.Popen(["kubectl", "--context", CONTEXT, "--namespace", cluster.namespace,
                             "exec", "-i", database_pod, "--", "psql", "-X", "-v",
                             "ON_ERROR_STOP=1", "-U", "postgres", "-d", "product"],
                            stdin=subprocess.PIPE, stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL, text=True)
    try:
        require(lock.stdin is not None, "database lock input unavailable")
        lock.stdin.write("BEGIN; LOCK TABLE public.product IN ACCESS EXCLUSIVE MODE; "
                         "SELECT pg_sleep(20) /* step6_lock_marker */; COMMIT;\n")
        lock.stdin.close()
        cluster.wait("disposable Product table lock held", lambda: postgres(cluster, database,
            "SELECT count(*) FROM pg_locks WHERE relation = 'public.product'::regclass "
            "AND mode = 'AccessExclusiveLock' AND granted;\n").strip() == "1", seconds=10)
        with tempfile.TemporaryDirectory(prefix="product-step6-client-") as directory:
            binary = str(Path(directory) / "lifecycle-client")
            command(["make", "-C", str(ROOT / "services/product-service"),
                     "build-lifecycle-client", f"LIFECYCLE_CLIENT_OUTPUT={binary}"], timeout=150)
            with forwards(target) as ports:
                client = subprocess.Popen([binary, "-http", f"http://127.0.0.1:{ports['http']}",
                                           "-grpc", f"127.0.0.1:{ports['grpc']}"],
                                          stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                          text=True)
                try:
                    require(client.stdout is not None, "lifecycle client output unavailable")
                    readable, _, _ = select.select([client.stdout], [], [], 10)
                    require(bool(readable) and client.stdout.readline().strip() == "started",
                            "lifecycle client did not start")
                    deadline = time.monotonic() + 4
                    while time.monotonic() < deadline:
                        count = postgres(cluster, database,
                            "SELECT count(*) FROM pg_stat_activity WHERE usename = "
                            "'go_product_reader' AND (query LIKE '%name: ListProducts%' "
                            "OR query LIKE '%name: GetProduct%') AND wait_event_type = 'Lock';\n")
                        if count.strip() == "2":
                            break
                        time.sleep(0.2)
                    else:
                        raise CheckFailed("in-flight HTTP and gRPC database reads were not observed")
                    started = time.monotonic()
                    cluster.kubectl("delete", "pod", pod, "--wait=false")
                    shutdown_deadline = time.monotonic() + 4
                    while time.monotonic() < shutdown_deadline:
                        try:
                            with urlopen(f"http://127.0.0.1:{ports['health']}/readyz",
                                         timeout=0.3) as response:
                                if response.status != 200:
                                    break
                        except (HTTPError, URLError, TimeoutError):
                            break
                        time.sleep(0.05)
                    else:
                        raise CheckFailed("terminating Pod kept accepting readiness probes")
                    postgres(cluster, database, "SELECT pg_terminate_backend(pid) FROM "
                             "pg_stat_activity WHERE query LIKE '%step6_lock_marker%' "
                             "AND pid <> pg_backend_pid();\n")
                    output, _ = client.communicate(timeout=20)
                    require(client.returncode == 0, "lifecycle client failed")
                    result = json.loads(output.strip())
                    require(result == {"httpStatus": 200, "httpFailed": False,
                                       "grpcCode": "OK", "grpcItem": "CAR"},
                            "accepted in-flight Pod reads did not drain")
                    cluster.wait("drained Pod removed within grace period", lambda: not
                                 cluster.exists("pod", pod), seconds=30)
                    require(time.monotonic() - started < 30,
                            "in-flight Pod drain exceeded Kubernetes grace budget")
                    print("PASS in-flight Pod HTTP/gRPC reads drained after SIGTERM", flush=True)
                finally:
                    if client.poll() is None:
                        client.terminate()
                        try:
                            client.wait(timeout=3)
                        except subprocess.TimeoutExpired:
                            client.kill()
                            client.wait(timeout=3)
    finally:
        postgres(cluster, database, "SELECT pg_terminate_backend(pid) FROM "
                 "pg_stat_activity WHERE query LIKE '%step6_lock_marker%' "
                 "AND pid <> pg_backend_pid();\n")
        if lock.poll() is None:
            lock.terminate()
        try:
            lock.wait(timeout=5)
        except subprocess.TimeoutExpired:
            lock.kill()
            lock.wait(timeout=5)


def main() -> None:
    require("PRODUCT_DATABASE_URL" not in os.environ, "PRODUCT_DATABASE_URL must be unset")
    require(command(["kubectl", "config", "current-context"]).strip() == CONTEXT,
            "active context must be the controlled local Kind cluster")
    image = os.environ.get("IMAGE_REF", "")
    revision = command(["git", "-C", str(ROOT), "rev-parse", "--short=12", "HEAD"]).strip()
    require(image == f"insurance-hub-product-api:git-{revision}", "image must match HEAD")
    command(["docker", "image", "inspect", image])
    require(command(["kind", "get", "clusters"]).splitlines().count(KIND_CLUSTER) == 1,
            "controlled Kind cluster is missing")
    namespace = "issue-134-step6-" + secrets.token_hex(4)
    cluster = Cluster(namespace)
    items, deployment = rendered(cluster, image)
    print(f"Disposable namespace: {namespace}; image: {image}", flush=True)
    command(["kind", "load", "docker-image", image, "--name", KIND_CLUSTER], timeout=180)
    require(not cluster.exists("namespace", namespace),
            "refusing to reuse an existing namespace")
    try:
        cluster.apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {
            "name": namespace, "labels": {LABEL: namespace}}})
        run_scenarios(cluster, items, deployment)
    finally:
        owner = cluster.kubectl("get", "namespace", namespace, "--ignore-not-found", "-o", "json")
        if owner:
            require(json.loads(owner)["metadata"]["labels"].get(LABEL) == namespace,
                    "refusing cleanup of unowned namespace")
            cluster.kubectl("delete", "namespace", namespace, "--wait=false", timeout=30)
            cluster.wait("owned namespace removed", lambda: not cluster.exists(
                "namespace", namespace), seconds=180)


def run_scenarios(cluster: Cluster, items: list[dict[str, Any]],
                  deployment: dict[str, Any]) -> None:
    name = "step6-product-db"
    owner_password = secrets.token_urlsafe(24)
    reader_password = secrets.token_urlsafe(24)
    cluster.apply(secret("step6-db-owner", cluster.namespace,
                         {"username": "product", "password": owner_password}))
    cluster.apply({"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "metadata": {
        "name": name, "namespace": cluster.namespace, "labels": {LABEL: cluster.namespace}},
        "spec": {"instances": 1, "imageName": "ghcr.io/cloudnative-pg/postgresql:17",
                 "storage": {"size": "1Gi"}, "resources": {"requests": {"cpu": "125m", "memory": "256Mi"},
                        "limits": {"cpu": "250m", "memory": "512Mi"}},
                 "bootstrap": {"initdb": {"database": "product", "owner": "product",
                                          "secret": {"name": "step6-db-owner"}}}}})
    cluster.wait("disposable database ready", lambda: cluster.json(
        "get", "cluster", name).get("status", {}).get("readyInstances") == 1, seconds=240)
    ca = cluster.json("get", "secret", name + "-ca")["data"]["ca.crt"]
    ca_text = base64.b64decode(ca).decode()
    # The only database reachable by this Secret is the disposable CNPG cluster.
    url = (f"postgresql://go_product_reader:{reader_password}@{name}-rw."
           f"{cluster.namespace}.svc.cluster.local:5432/product?sslmode=verify-full&"
           "sslrootcert=/etc/product-service/postgres/ca.crt")
    credential = "local-dev-postgres-product-reader-creds"
    cluster.apply(secret(credential, cluster.namespace,
                         {"database-url": url, "ca.crt": ca_text}))
    setup = ("REVOKE ALL ON DATABASE product FROM PUBLIC;\n"
             "REVOKE ALL ON SCHEMA public FROM PUBLIC;\n"
             "CREATE ROLE go_product_reader LOGIN PASSWORD '" + reader_password + "';\n"
             "GRANT CONNECT ON DATABASE product TO go_product_reader;\n"
             "GRANT USAGE ON SCHEMA public TO go_product_reader;\n")
    postgres(cluster, name, setup)

    config_maps = [item for item in items if item["kind"] == "ConfigMap"]
    require(len(config_maps) == 1, "expected one rendered Product ConfigMap")
    cluster.apply(config_maps[0])

    # The exact rendered Pod spec is used for negative startup cases, with only
    # the selected error injected. No failure Pod matches the business Service.
    protected = (reader_password, owner_password, url)
    failure_pod(cluster, deployment, "missing-config", lambda spec: spec["containers"][0]["env"].clear(), protected)
    failure_pod(cluster, deployment, "absent-schema", lambda spec: None, protected)
    postgres(cluster, name, "SET ROLE product; CREATE TABLE public.product (code varchar(255) PRIMARY KEY, definition jsonb NOT NULL);\n")
    failure_pod(cluster, deployment, "absent-grant", lambda spec: None, protected)
    postgres(cluster, name, "GRANT SELECT ON public.product TO go_product_reader;\n")
    failure_pod(cluster, deployment, "invalid-credential", lambda spec: spec["containers"][0]["env"].__setitem__(
        0, {"name": "PRODUCT_DATABASE_URL", "value": url.replace(reader_password, "invalid")}), protected)
    failure_pod(cluster, deployment, "unavailable-db", lambda spec: spec["containers"][0]["env"].__setitem__(
        0, {"name": "PRODUCT_DATABASE_URL", "value":
            "postgresql://go_product_reader:invalid@127.0.0.1:5432/product?sslmode=disable"}), protected)

    # A test-owned routing Service permits a sustained dependency outage. The
    # earlier Pods prove verify-full against CNPG's real service certificate;
    # this switch uses verify-ca because the gate has a different DNS name.
    gate = "step6-db-gate"
    cluster.apply({"apiVersion": "v1", "kind": "Service", "metadata": {
        "name": gate, "namespace": cluster.namespace, "labels": {LABEL: cluster.namespace}},
        "spec": {"type": "ClusterIP", "ports": [{"name": "postgres", "port": 5432,
                 "targetPort": 5432}], "selector": {"cnpg.io/cluster": name,
                                                     "cnpg.io/instanceRole": "primary"}}})
    cluster.wait("database gate endpoint ready", lambda: endpoints(cluster, gate) == 1)
    gate_url = (f"postgresql://go_product_reader:{reader_password}@{gate}."
                f"{cluster.namespace}.svc.cluster.local:5432/product?sslmode=verify-ca&"
                "sslrootcert=/etc/product-service/postgres/ca.crt")
    cluster.apply(secret(credential, cluster.namespace,
                         {"database-url": gate_url, "ca.crt": ca_text}))

    for item in items:
        cluster.apply(item)
    cluster.wait("empty catalog deployment ready", lambda: cluster.json("get", "deployment",
        "local-dev-product-api").get("status", {}).get("availableReplicas") == 1, seconds=120)
    cluster.wait("empty catalog business EndpointSlice ready", lambda: endpoints(
        cluster, "local-dev-product-api") == 1)
    cluster.wait("empty catalog management EndpointSlice ready", lambda: endpoints(
        cluster, "local-dev-product-api-management") == 1)
    require(snapshot(cluster, name) == hashlib.md5(b"").hexdigest(), "empty catalog changed")
    live_pods = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=product-api")["items"]
    require(len(live_pods) == 1, "empty catalog Product Pod is not unique")
    live_spec = live_pods[0]["spec"]
    desired_spec = deployment["spec"]["template"]["spec"]
    checks = {field: live_spec.get(field) == desired_spec.get(field) for field in
              ("automountServiceAccountToken", "enableServiceLinks",
               "terminationGracePeriodSeconds")}
    checks.update({"pod-security-" + key: live_spec.get("securityContext", {}).get(key) == value
                   for key, value in desired_spec["securityContext"].items()})
    checks.update({"container-security-" + key:
                   live_spec["containers"][0].get("securityContext", {}).get(key) == value
                   for key, value in desired_spec["containers"][0]["securityContext"].items()})
    checks["restart-count"] = live_pods[0]["status"]["containerStatuses"][0]["restartCount"] == 0
    require(all(checks.values()), "admitted Product Pod differs in " +
            ", ".join(key for key, passed in checks.items() if not passed))
    print("PASS admitted Pod security matches rendered settings", flush=True)
    verify_empty_catalog(cluster)
    print("PASS empty catalog readiness and unchanged empty table", flush=True)

    fixture = json.loads(FIXTURE.read_text())
    require(len(fixture["rows"]) == 4, "accepted QA fixture inventory changed")
    values = ",".join("('" + row["code"].replace("'", "''") + "','" +
                      row["rawLosslessDefinitionJson"].replace("'", "''") + "'::jsonb)"
                      for row in fixture["rows"])
    postgres(cluster, name, "SET ROLE product; INSERT INTO public.product(code, definition) VALUES " +
             values + ";\n")
    before = snapshot(cluster, name)
    require(before != hashlib.md5(b"").hexdigest(), "fixture seed is empty")
    print(f"PASS fixture seeded by setup identity; database checksum {before}", flush=True)
    verify_catalog_reads(cluster)

    # Move only the test-owned database gate endpoint away from its Pod.
    original = cluster.json("get", "service", gate)["spec"]["selector"]
    cluster.kubectl("patch", "service", gate, "--type=json", "-p", json.dumps([
        {"op": "replace", "path": "/spec/selector", "value": {LABEL: "no-such-pod"}}]))
    outage_started = time.monotonic()
    try:
        cluster.wait("database gate endpoints withdrawn", lambda: endpoints(cluster, gate) == 0)
        postgres(cluster, name, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
                 "WHERE usename = 'go_product_reader' AND pid <> pg_backend_pid();\n")
        cluster.wait("database loss withdraws Product readiness", lambda: endpoints(
            cluster, "local-dev-product-api") == 0, seconds=50)
        pods = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=product-api")["items"]
        require(len(pods) == 1 and pods[0]["status"]["containerStatuses"][0]["restartCount"] == 0,
                "database loss restarted Product")
        while time.monotonic() - outage_started < 35:
            require(endpoints(cluster, "local-dev-product-api") == 0 and
                    endpoints(cluster, "local-dev-product-api-management") == 0,
                    "Product unexpectedly became ready during database outage")
            pods = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=product-api")["items"]
            require(len(pods) == 1 and
                    pods[0]["status"]["containerStatuses"][0]["restartCount"] == 0,
                    "database-dependent liveness restarted Product")
            time.sleep(3)
        print("PASS database loss stays unready without Product restart beyond liveness window", flush=True)
    finally:
        cluster.kubectl("patch", "service", gate, "--type=json", "-p", json.dumps([
            {"op": "replace", "path": "/spec/selector", "value": original}]))
    cluster.wait("database recovery restores Product EndpointSlice", lambda: endpoints(
        cluster, "local-dev-product-api") == 1, seconds=90)

    # A no-op template annotation forces the rendered rolling strategy to run.
    cluster.kubectl("patch", "deployment", "local-dev-product-api", "--type=merge", "-p",
                    json.dumps({"spec": {"template": {"metadata": {"annotations": {
                        "insurance-hub.io/step6-rollout": secrets.token_hex(4)}}}}}))
    max_pods = 0
    peak_connections = 0
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        pods = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=product-api")["items"]
        max_pods = max(max_pods, len(pods))
        require(len(pods) <= 2, "rolling strategy exceeded maxSurge")
        connections = int(postgres(cluster, name, "SELECT count(*) FROM pg_stat_activity "
                                   "WHERE usename = 'go_product_reader';\n").strip())
        peak_connections = max(peak_connections, connections)
        require(connections <= 16, "rolling Product connection pool exceeded 2 x DB_MAX_CONNS")
        live = cluster.json("get", "deployment", "local-dev-product-api")
        if live.get("status", {}).get("updatedReplicas") == 1 and \
                live.get("status", {}).get("availableReplicas") == 1 and len(pods) == 1:
            break
        time.sleep(2)
    else:
        raise CheckFailed("rolling deployment did not settle")
    require(all(pod["status"]["containerStatuses"][0]["restartCount"] == 0 for pod in pods),
            "healthy rollout restarted a Product container")
    require(snapshot(cluster, name) == before, "runtime changed accepted fixture")
    print(f"PASS bounded rollout (peak Pods {max_pods}, observed reader connections "
          f"{peak_connections}, pool bound 16)", flush=True)
    print("PASS fixture checksum unchanged and no healthy-path restarts", flush=True)

    verify_inflight_drain(cluster, name, pods[0]["metadata"]["name"])
    cluster.wait("post-drain replacement Pod ready", lambda: endpoints(
        cluster, "local-dev-product-api") == 1 and cluster.json("get", "deployment",
        "local-dev-product-api").get("status", {}).get("availableReplicas") == 1,
        seconds=120)
    pods = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=product-api")["items"]
    require(len(pods) == 1 and pods[0]["status"]["containerStatuses"][0]["restartCount"] == 0,
            "drained Pod was not replaced cleanly")

    old_pod = pods[0]["metadata"]["name"]
    started = time.monotonic()
    cluster.kubectl("delete", "pod", old_pod, "--wait=true", "--timeout=30s", timeout=40)
    require(time.monotonic() - started < 30, "idle Pod exceeded termination grace budget")
    cluster.wait("replacement Pod and EndpointSlice ready", lambda: endpoints(
        cluster, "local-dev-product-api") == 1 and cluster.json("get", "deployment",
        "local-dev-product-api").get("status", {}).get("availableReplicas") == 1,
        seconds=120)
    pods = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=product-api")["items"]
    require(len(pods) == 1 and pods[0]["metadata"]["name"] != old_pod and
            pods[0]["status"]["containerStatuses"][0]["restartCount"] == 0,
            "idle termination left an unhealthy replacement")
    logs = cluster.kubectl("logs", pods[0]["metadata"]["name"])
    require(all(marker not in logs for marker in (*protected, gate_url,
            "rawLosslessDefinitionJson")), "runtime logs exposed secret or fixture data")
    require(snapshot(cluster, name) == before, "lifecycle checks changed accepted fixture")
    print("PASS idle Pod SIGTERM and healthy replacement within grace budget", flush=True)
    print("PASS final fixture checksum and runtime log redaction", flush=True)


def failure_pod(cluster: Cluster, deployment: dict[str, Any], scenario: str,
                mutate: Callable[[dict[str, Any]], None], protected: tuple[str, ...]) -> None:
    name = "step6-" + scenario
    spec = deepcopy(deployment["spec"]["template"]["spec"])
    mutate(spec)
    spec["restartPolicy"] = "Never"
    pod = {"apiVersion": "v1", "kind": "Pod", "metadata": {"name": name,
           "namespace": cluster.namespace, "labels": {LABEL: cluster.namespace}}, "spec": spec}
    cluster.apply(pod)
    def stopped() -> bool:
        actual = cluster.json("get", "pod", name)
        statuses = actual.get("status", {}).get("containerStatuses", [])
        return bool(statuses and statuses[0].get("state", {}).get("terminated"))
    cluster.wait(scenario + " startup fails closed", stopped, seconds=55)
    logs = cluster.kubectl("logs", name)
    require("postgresql://" not in logs and "rawLosslessDefinitionJson" not in logs and
            all(marker not in logs for marker in protected),
            scenario + " emitted an unsafe log marker")
    print("PASS " + scenario + " safe startup logs", flush=True)
    cluster.kubectl("delete", "pod", name, "--wait=true", timeout=60)


if __name__ == "__main__":
    try:
        main()
    except CheckFailed as error:
        print(f"FAIL {error}", file=sys.stderr)
        raise SystemExit(1) from error
