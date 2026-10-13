#!/usr/bin/env python3
"""Guarded Product deployment operations; invoked through the selected-service Make target."""

from __future__ import annotations

from contextlib import contextmanager
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
from typing import Any, Iterator

ROOT = Path(__file__).resolve().parents[3]
TARGETS = {
    "local-dev": ("kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"),
    "qa": ("qa-insurance-hub", "qa-svc", "qa-data"),
}
OPERATIONS = {"deploy-preflight", "deploy-status", "image-load", "deploy", "deploy-verify", "stop"}
IDENTITY = "product-api"


class DeploymentError(Exception):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise DeploymentError(message)


def run(argv: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    try:
        result = subprocess.run(argv, input=input_text, text=True, capture_output=True,
                                timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise DeploymentError(f"command unavailable or timed out: {argv[0]}") from error
    # A CLI error may contain a credential-bearing URL from a resource. Never relay it.
    require(result.returncode == 0, f"command failed: {argv[0]} (exit {result.returncode})")
    return result.stdout


def documents(text: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    output = []
    remaining = text.strip()
    while remaining:
        value, offset = decoder.raw_decode(remaining)
        output.extend(value.get("items", []) if value.get("kind") == "List" else [value])
        remaining = remaining[offset:].strip()
    require(bool(output) and all(isinstance(item, dict) for item in output),
            "rendered resources are missing or malformed")
    return output


def resource(items: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [item for item in items if item.get("kind") == kind and
               item.get("metadata", {}).get("name") == name]
    require(len(matches) == 1, f"expected exactly one {kind}/{name}")
    return matches[0]


def labels_match(selector: dict[str, str], labels: dict[str, str]) -> bool:
    return bool(selector) and all(labels.get(key) == value for key, value in selector.items())


def matching_volumes(actual: Any, expected: Any) -> bool:
    if not isinstance(actual, list) or not isinstance(expected, list) or len(actual) != len(expected):
        return False
    for current, declared in zip(actual, expected):
        if not isinstance(current, dict) or not isinstance(declared, dict):
            return False
        secret = current.get("secret", {})
        desired_secret = declared.get("secret", {})
        if not isinstance(secret, dict) or not isinstance(desired_secret, dict):
            return False
        if (current.get("name") != declared.get("name") or
                secret.get("secretName") != desired_secret.get("secretName") or
                secret.get("items") != desired_secret.get("items") or
                secret.get("defaultMode") not in (None, 420) or
                secret.get("optional") not in (None, False) or
                set(current) != {"name", "secret"}):
            return False
    return True


def matching_probe(actual: Any, expected: Any) -> bool:
    if actual == expected:
        return True
    if not isinstance(actual, dict) or not isinstance(expected, dict):
        return False
    declared_with_defaults = dict(expected)
    declared_with_defaults.setdefault("successThreshold", 1)
    return actual == declared_with_defaults


class Target:
    def __init__(self) -> None:
        self.environment = os.environ.get("DEPLOY_ENV", "")
        require(self.environment in TARGETS, "DEPLOY_ENV must be local-dev or qa")
        self.context, self.namespace, self.data_namespace = TARGETS[self.environment]
        for key, expected in (("DEPLOY_CONTEXT", self.context),
                              ("DEPLOY_SVC_NS", self.namespace),
                              ("DEPLOY_DATA_NS", self.data_namespace)):
            require(os.environ.get(key) == expected, f"{key} must explicitly equal {expected}")
        self.prefix = f"{self.environment}-"
        self.deployment = self.prefix + IDENTITY
        self.business = self.deployment
        self.management = self.deployment + "-management"
        self.secret = self.prefix + "postgres-product-reader-creds"
        self.overlay = ROOT / "k8s" / "overlays" / self.environment / "svc" / "product"

    def kubectl(self, *arguments: str, input_text: str | None = None, timeout: int = 30) -> str:
        return run(["kubectl", "--context", self.context, "--namespace", self.namespace,
                    *arguments], input_text=input_text, timeout=timeout)

    def json(self, *arguments: str) -> dict[str, Any]:
        try:
            raw = self.kubectl(*arguments, "-o", "json").strip()
            value = json.loads(raw) if raw else {}
        except (ValueError, TypeError) as error:
            raise DeploymentError("Kubernetes JSON response is malformed") from error
        require(isinstance(value, dict), "Kubernetes JSON response is malformed")
        return value

    def coordinates(self) -> None:
        require(run(["kubectl", "config", "current-context"]).strip() == self.context,
                "active Kubernetes context differs from the requested target")
        namespace = run(["kubectl", "config", "view", "--minify",
                         "-o", "jsonpath={..namespace}"]).strip() or "default"
        require(namespace == self.namespace,
                "active Kubernetes namespace differs from the requested target")
        self.json("get", "namespace", self.namespace)
        self.json("get", "namespace", self.data_namespace)

    def rendered(self) -> tuple[str, list[dict[str, Any]], str]:
        yaml = self.kubectl("kustomize", str(self.overlay))
        converted = self.kubectl("create", "--dry-run=client", "--validate=false",
                                 "-f", "-", "-o", "json", input_text=yaml)
        items = documents(converted)
        names = {(item.get("kind"), item.get("metadata", {}).get("name")) for item in items}
        expected = {("Deployment", self.deployment), ("Service", self.business),
                    ("Service", self.management)}
        config_maps = [name for kind, name in names if kind == "ConfigMap" and
                       re.fullmatch(re.escape(self.prefix + "product-config-") + r"[a-z0-9]+", name)]
        require(expected.issubset(names) and len(config_maps) == 1 and len(items) == 4 and
                all(item.get("metadata", {}).get("namespace") == self.namespace for item in items),
                "rendered resources differ from the selected Product Go inventory")
        deployment = resource(items, "Deployment", self.deployment)
        image = deployment["spec"]["template"]["spec"]["containers"][0]["image"]
        require(os.environ.get("DEPLOY_IMAGE_REF") == image,
                "DEPLOY_IMAGE_REF must equal the rendered image")
        if self.environment == "qa":
            require(bool(re.fullmatch(r"ghcr\.io/igor-baiborodine/insurance-hub/product-api@sha256:[0-9a-f]{64}", image)),
                    "QA image must use an immutable digest")
        else:
            require(bool(re.fullmatch(r"insurance-hub-product-api:git-[0-9a-f]{12}", image)),
                    "local image must use the source-derived tag")
        return yaml, items, image

    def qa_source(self) -> None:
        source = self.json("get", "gitrepository", "insurance-hub", "-n", "flux-system")
        reconciler = self.json("get", "kustomization", "qa-svc", "-n", "flux-system")
        require(source.get("spec", {}).get("ref", {}).get("branch") == "main", "QA source must track main")
        require(reconciler.get("spec", {}).get("path") == "./k8s/overlays/qa/svc" and
                reconciler.get("spec", {}).get("sourceRef") ==
                {"kind": "GitRepository", "name": "insurance-hub"} and
                reconciler.get("spec", {}).get("prune") is True and
                reconciler.get("spec", {}).get("wait") is True and
                reconciler.get("spec", {}).get("suspend") is not True,
                "QA Flux ownership differs from the declared path/prune/wait contract")
        for item in (source, reconciler):
            require(any(condition.get("type") == "Ready" and condition.get("status") == "True"
                        for condition in item.get("status", {}).get("conditions", [])),
                    "QA Flux source or Kustomization is not ready")
        source_revision = source.get("status", {}).get("artifact", {}).get("revision", "")
        applied_revision = reconciler.get("status", {}).get("lastAppliedRevision", "")
        expected_revision = os.environ.get("DEPLOY_SOURCE_REVISION", "")
        require(bool(re.fullmatch(r"[0-9a-f]{40}", expected_revision)) and
                source_revision.endswith(expected_revision) and applied_revision == source_revision,
                "QA Flux source/applied revisions differ from DEPLOY_SOURCE_REVISION")
        print(f"product-deployment: QA Flux revision {source_revision}")

    def live(self, items: list[dict[str, Any]], *, require_deployment: bool) -> None:
        go = self.json("get", "deployment", self.deployment, "--ignore-not-found")
        if go.get("kind") == "Deployment":
            desired = resource(items, "Deployment", self.deployment)
            require(go.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/instance") ==
                    desired["metadata"]["labels"]["app.kubernetes.io/instance"],
                    "live Go Deployment identity differs from rendered identity")
            require(go.get("spec", {}).get("selector") == desired["spec"]["selector"],
                    "live Go Deployment selector differs from rendered selector")
            live_template = go.get("spec", {}).get("template", {}).get("spec", {})
            desired_template = desired["spec"]["template"]["spec"]
            require(live_template.get("automountServiceAccountToken") is False and
                    live_template.get("securityContext") == desired_template.get("securityContext") and
                    live_template.get("hostNetwork") is not True,
                    "live Go Deployment security settings differ from rendered settings")
            for field in ("enableServiceLinks", "terminationGracePeriodSeconds"):
                require(live_template.get(field) == desired_template.get(field),
                        f"live Go Deployment {field} differs from rendered settings")
            require(matching_volumes(live_template.get("volumes"), desired_template.get("volumes")),
                    "live Go Deployment CA volume differs from rendered settings")
            live_containers = live_template.get("containers", [])
            require(len(live_containers) == 1,
                    "live Go Deployment has an unexpected container inventory")
            for field in ("name", "securityContext", "ports", "env", "volumeMounts", "resources",
                          "imagePullPolicy"):
                require(live_containers[0].get(field) == desired_template["containers"][0].get(field),
                        f"live Go Deployment container {field} differs from rendered settings")
            for field in ("startupProbe", "livenessProbe", "readinessProbe"):
                require(matching_probe(live_containers[0].get(field),
                                       desired_template["containers"][0].get(field)),
                        f"live Go Deployment container {field} differs from rendered settings")
            actual_env_from = live_containers[0].get("envFrom")
            desired_env_from = desired_template["containers"][0].get("envFrom")
            previous_config = os.environ.get("DEPLOY_PREVIOUS_CONFIG_MAP", "")
            allowed_previous = (not require_deployment and self.environment == "local-dev" and
                                re.fullmatch(re.escape(self.prefix + "product-config-") + r"[a-z0-9]+", previous_config) and
                                actual_env_from == [{"configMapRef": {"name": previous_config}}])
            require(actual_env_from == desired_env_from or allowed_previous,
                    "live Go ConfigMap reference differs; declare DEPLOY_PREVIOUS_CONFIG_MAP for a local rollout")
            actual_image = go.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [{}])[0].get("image")
            expected_image = desired["spec"]["template"]["spec"]["containers"][0]["image"]
            require(actual_image == expected_image or
                    (not require_deployment and self.environment == "local-dev" and
                     os.environ.get("DEPLOY_PREVIOUS_IMAGE_REF") == actual_image and actual_image),
                    "live Go image differs from rendered image; declare DEPLOY_PREVIOUS_IMAGE_REF for a local rollout")
        else:
            require(not require_deployment, "Go Deployment is absent")
        legacy = self.json("get", "deployment", self.prefix + "product-api-legacy")
        go_selector = resource(items, "Deployment", self.deployment)["spec"]["selector"]["matchLabels"]
        java_selector = legacy.get("spec", {}).get("selector", {}).get("matchLabels", {})
        require(java_selector and not labels_match(go_selector, java_selector) and
                not labels_match(java_selector, go_selector), "Go and Java selectors overlap")
        expected_config = next(item for item in items if item.get("kind") == "ConfigMap")
        config = self.json("get", "configmap", expected_config["metadata"]["name"], "--ignore-not-found")
        if config.get("kind") == "ConfigMap":
            require(config.get("data") == expected_config.get("data"),
                    "live Go ConfigMap differs from rendered configuration")
        else:
            require(not require_deployment, "Go ConfigMap is absent")
        for name in (self.business, self.management):
            service = self.json("get", "service", name, "--ignore-not-found")
            if service.get("kind") != "Service":
                require(not require_deployment, f"Service/{name} is absent")
                continue
            spec = service.get("spec", {})
            expected = resource(items, "Service", name)["spec"]
            require(service.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/instance") ==
                    resource(items, "Service", name)["metadata"]["labels"]["app.kubernetes.io/instance"],
                    f"Service/{name} has a different workload identity")
            require(spec.get("type") == "ClusterIP" and spec.get("selector") == expected["selector"] and
                    spec.get("ports") == expected["ports"] and
                    not any(field in spec for field in ("externalIPs", "loadBalancerIP", "externalName")) and
                    all("nodePort" not in port for port in spec.get("ports", [])),
                    f"Service/{name} has drift or external exposure")
        go_labels = resource(items, "Deployment", self.deployment)["spec"]["template"]["metadata"]["labels"]
        all_services = self.json("get", "services")
        for service in all_services.get("items", []):
            selector = service.get("spec", {}).get("selector", {})
            if labels_match(selector, go_labels):
                require(service.get("metadata", {}).get("name") in {self.business, self.management},
                        "an unexpected Service selects Go Pods")
        pods = self.json("get", "pods", "-l", "app.kubernetes.io/name=product-api")
        if require_deployment:
            require(bool(pods.get("items")), "Go Pods are absent")
        for pod in pods.get("items", []):
            spec = pod.get("spec", {})
            desired_spec = resource(items, "Deployment", self.deployment)["spec"]["template"]["spec"]
            pod_image = desired_spec["containers"][0]["image"]
            allowed_images = {pod_image}
            if not require_deployment and self.environment == "local-dev":
                previous = os.environ.get("DEPLOY_PREVIOUS_IMAGE_REF", "")
                if previous:
                    allowed_images.add(previous)
            require(all(container.get("image") in allowed_images for container in spec.get("containers", [])),
                    "Go Pod image differs from rendered image")
            require(not spec.get("hostNetwork") and not any("hostPort" in port for container in
                    spec.get("containers", []) for port in container.get("ports", [])),
                    "Go Pod has host networking or a host port")
            require(not labels_match(java_selector, pod.get("metadata", {}).get("labels", {})),
                    "Java selector matches a Go Pod")
            require(pod.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/instance") ==
                    go_labels["app.kubernetes.io/instance"],
                    "Go Pod workload identity differs from rendered identity")
            require(spec.get("automountServiceAccountToken") is False and
                    spec.get("securityContext") == desired_spec["securityContext"] and
                    matching_volumes(spec.get("volumes"), desired_spec["volumes"]),
                    "Go Pod security settings differ from rendered settings")
            containers = spec.get("containers", [])
            require(len(containers) == 1, "Go Pod has an unexpected container inventory")
            for field in ("securityContext", "ports", "env", "volumeMounts", "resources",
                          "imagePullPolicy"):
                require(containers[0].get(field) == desired_spec["containers"][0].get(field),
                        f"Go Pod {field} differs from rendered settings")
            for field in ("startupProbe", "livenessProbe", "readinessProbe"):
                require(matching_probe(containers[0].get(field),
                                       desired_spec["containers"][0].get(field)),
                        f"Go Pod {field} differs from rendered settings")
            if require_deployment:
                statuses = pod.get("status", {}).get("containerStatuses", [])
                require(all(status.get("imageID") for status in statuses) and bool(statuses),
                        "Go Pod image ID is unavailable")
                print("product-deployment: pod image IDs " + ", ".join(
                    status["imageID"] for status in statuses))
        slices = self.json("get", "endpointslices", "-l", f"kubernetes.io/service-name={self.business}")
        if require_deployment:
            require(any(endpoint.get("conditions", {}).get("ready") is True for item in
                        slices.get("items", []) for endpoint in (item.get("endpoints") or [])),
                    "Go business Service has no ready endpoint")
        legacy_service = self.json("get", "service", self.prefix + "product-api-legacy")
        require(legacy_service.get("spec", {}).get("selector") == java_selector,
                "Java Service selector differs from its Deployment")
        legacy_slices = self.json("get", "endpointslices", "-l",
                                  f"kubernetes.io/service-name={self.prefix}product-api-legacy")
        require(any(endpoint.get("conditions", {}).get("ready") is True for item in
                    legacy_slices.get("items", []) for endpoint in (item.get("endpoints") or [])),
                "Java Product Service has no ready endpoint")
        gateway = self.json("get", "deployment", self.prefix + "agent-portal-gateway-legacy")
        gateway_spec = gateway.get("spec", {}).get("template", {}).get("spec", {})
        require(gateway.get("status", {}).get("readyReplicas", 0) > 0 and
                any(variable.get("name") == "MICRONAUT_ENVIRONMENTS" and
                    variable.get("value") == self.environment for container in
                    gateway_spec.get("containers", []) for variable in container.get("env", [])),
                "Java gateway deployment is not ready in the selected environment")
        route_file = ROOT / "legacy" / "agent-portal-gateway" / "src" / "main" / "resources" / f"application-{self.environment}.yml"
        route_text = route_file.read_text(encoding="utf-8")
        require(f"http://{self.prefix}product-api-legacy:80" in route_text and
                f"http://{self.business}:" not in route_text,
                "checked-in gateway Product destination differs from Java")
        self.routes()
        print("product-deployment: Go/Java selectors, Services, Pods, EndpointSlices, gateway identity and routes checked")

    def routes(self) -> None:
        available = run(["kubectl", "--context", self.context, "api-resources", "-o", "name"])
        for kind, plural in (("ingress", "ingresses"), ("gateway", "gateways"),
                             ("httproute", "httproutes"), ("grpcroute", "grpcroutes")):
            # Skip optional CRDs only when the API itself is unavailable.
            if not any(line.split(".")[0] == plural for line in available.splitlines()):
                continue
            listing = self.json("get", plural, "--all-namespaces")
            for item in listing.get("items", []):
                require(not refers_to_go(item.get("spec", {}), {self.business, self.management}),
                        f"{kind} routes a Product Go listener")


def refers_to_go(value: Any, names: set[str]) -> bool:
    if isinstance(value, dict):
        return any(key in {"name", "serviceName"} and isinstance(child, str) and child in names or
                   refers_to_go(child, names) for key, child in value.items())
    if isinstance(value, list):
        return any(refers_to_go(child, names) for child in value)
    return False


def secret_and_image(target: Target, image: str) -> None:
    secret = target.json("get", "secret", target.secret)
    require(secret.get("kind") == "Secret" and
            set(secret.get("data", {})) == {"database-url", "ca.crt"},
            "reader Secret is absent or has unexpected keys")
    if target.environment == "local-dev":
        try:
            details = json.loads(run(["docker", "image", "inspect", image]))[0]
        except (ValueError, IndexError, KeyError) as error:
            raise DeploymentError("local image inspection failed") from error
        source = details.get("Config", {}).get("Labels", {}).get("org.opencontainers.image.revision", "")
        require(source.startswith(image.rsplit("git-", 1)[1]) and len(source) == 40,
                "local image source revision differs from the rendered tag")


def rollout(target: Target) -> None:
    target.kubectl("rollout", "status", f"deployment/{target.deployment}", "--timeout=180s", timeout=195)


@contextmanager
def forwards(target: Target) -> Iterator[dict[str, int]]:
    children: list[subprocess.Popen[str]] = []
    ports: dict[str, int] = {}
    try:
        for name, service, remote in (("http", target.business, 8081),
                                      ("grpc", target.business, 9090),
                                      ("health", target.management, 8080)):
            with socket.socket() as reservation:
                reservation.bind(("127.0.0.1", 0))
                local = reservation.getsockname()[1]
            child = subprocess.Popen(["kubectl", "--context", target.context, "--namespace",
                                      target.namespace, "port-forward", "--address", "127.0.0.1",
                                      f"service/{service}", f"{local}:{remote}"],
                                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, text=True)
            children.append(child)
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                require(child.poll() is None, f"port-forward for {name} exited")
                try:
                    with socket.create_connection(("127.0.0.1", local), timeout=0.2):
                        ports[name] = local
                        break
                except OSError:
                    time.sleep(0.1)
            require(name in ports, f"port-forward for {name} timed out")
        yield ports
    finally:
        for child in children:
            if child.poll() is None:
                child.terminate()
        for child in children:
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)


def role_environment(target: Target) -> dict[str, str]:
    role_env = {
        "POSTGRES_ROLE_ENV": target.environment,
        "POSTGRES_ROLE_CONTEXT": target.context,
        "POSTGRES_ROLE_DATA_NS": target.data_namespace,
        "POSTGRES_ROLE_SVC_NS": target.namespace,
        "POSTGRES_ROLE_CLUSTER": target.prefix + "postgres-product",
        "POSTGRES_ROLE_SECRET": target.secret,
        "POSTGRES_ROLE_DB_HOST": target.prefix + "postgres-product-rw." +
                                 target.data_namespace + ".svc.cluster.local",
    }
    environment = os.environ.copy()
    environment.update(role_env)
    environment["SVC_NAME"] = "product"
    environment["ROLE_PROFILE"] = "reader"
    return environment


def role_action(target: Target, action: str) -> str:
    require(action in {"snapshot", "verify"}, "unsupported read-only role action")
    try:
        result = subprocess.run(["make", f"postgres-svc-role-{action}"], cwd=ROOT,
                                env=role_environment(target),
                                capture_output=True, text=True, timeout=180, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise DeploymentError(f"reader {action} unavailable or timed out") from error
    require(result.returncode == 0, f"reader {action} failed; inspect the role safely")
    return result.stdout.strip()


def role_snapshot(target: Target) -> dict[str, Any]:
    try:
        value = json.loads(role_action(target, "snapshot"))
    except ValueError as error:
        raise DeploymentError("reader snapshot is malformed") from error
    require(isinstance(value, dict) and set(value) == {"rows", "catalog", "columns", "tableOwner"} and
            value.get("tableOwner") == "product" and
            all(isinstance(value.get(key), str) and
                re.fullmatch(r"[0-9a-f]{32}", value[key]) for key in ("catalog", "columns")) and
            isinstance(value.get("rows"), int),
            "reader snapshot has unexpected catalog identity")
    return value


def record_snapshot(stage: str, value: dict[str, Any]) -> None:
    print(f"product-deployment: {stage} catalog identity " + json.dumps(value, sort_keys=True))


def verify(target: Target, items: list[dict[str, Any]]) -> None:
    target.live(items, require_deployment=True)
    before = role_snapshot(target)
    record_snapshot("before verification", before)
    role_action(target, "verify")
    environment = role_environment(target)
    with forwards(target) as ports:
        environment.update({
            "PRODUCT_SECURITY_TARGET_CONTEXT": target.context,
            "PRODUCT_SECURITY_TARGET_NAMESPACE": target.namespace,
            "PRODUCT_SECURITY_EXPECTED_ENV": target.environment,
            "PRODUCT_SECURITY_HTTP_ENDPOINT": f"http://127.0.0.1:{ports['http']}",
            "PRODUCT_SECURITY_GRPC_ENDPOINT": f"127.0.0.1:{ports['grpc']}",
            "PRODUCT_SECURITY_HEALTH_ENDPOINT": f"http://127.0.0.1:{ports['health']}",
        })
        environment.pop("PRODUCT_DATABASE_URL", None)
        try:
            result = subprocess.run(["make", "product-security-verify"], cwd=ROOT, env=environment,
                                    capture_output=True, text=True, timeout=180, check=False)
        except (OSError, subprocess.TimeoutExpired) as error:
            raise DeploymentError("Product access verification unavailable or timed out") from error
        require(result.returncode == 0, "Product access verification failed")
    after = role_snapshot(target)
    record_snapshot("after verification", after)
    require(after == before, "catalog/schema identity changed during verification")
    print("product-deployment: reader identity, safe SQL probes and Go HTTP/gRPC reads passed")


def main() -> None:
    require(len(sys.argv) == 2 and sys.argv[1] in OPERATIONS, "unsupported operation")
    operation = sys.argv[1]
    target = Target()
    if operation in {"image-load", "deploy", "stop"}:
        require(target.environment == "local-dev",
                "QA image loading, direct apply and stop are forbidden; use reviewed Flux state")
    target.coordinates()
    run(["make", "-C", str(ROOT / "services" / "product-service"), "deploy-check"], timeout=90)
    yaml, items, image = target.rendered()
    if target.environment == "qa":
        target.qa_source()
    if operation in {"deploy-preflight", "image-load", "deploy"}:
        secret_and_image(target, image)
        target.live(items, require_deployment=False)
    if operation == "deploy-preflight":
        print("product-deployment: target and prerequisites passed")
    elif operation == "deploy-status":
        target.live(items, require_deployment=True)
    elif operation == "image-load":
        require(target.environment == "local-dev", "Kind image load is local-dev only")
        run(["kind", "load", "docker-image", image, "--name", "local-dev-insurance-hub"], timeout=120)
        print(f"product-deployment: loaded {image} into the selected Kind cluster")
    elif operation == "deploy":
        require(target.environment == "local-dev", "QA deployment is Flux-owned; direct apply is forbidden")
        before = role_snapshot(target)
        record_snapshot("before deployment", before)
        target.kubectl("apply", "--server-side", "--dry-run=server",
                       "--field-manager=insurance-hub-go-product", "-f", "-",
                       input_text=yaml, timeout=60)
        target.kubectl("apply", "--server-side", "--field-manager=insurance-hub-go-product",
                       "-f", "-", input_text=yaml, timeout=60)
        rollout(target)
        target.live(items, require_deployment=True)
        after = role_snapshot(target)
        record_snapshot("after deployment", after)
        require(after == before, "catalog/schema identity changed during local deployment")
        print("product-deployment: local Go rollout ready; catalog/schema identity unchanged")
    elif operation == "deploy-verify":
        verify(target, items)
    elif operation == "stop":
        require(target.environment == "local-dev", "QA stop requires a reviewed Flux desired-state change")
        target.live(items, require_deployment=True)
        before = role_snapshot(target)
        record_snapshot("before stop", before)
        deployment = target.json("get", "deployment", target.deployment)
        version = deployment.get("metadata", {}).get("resourceVersion", "")
        require(deployment.get("spec", {}).get("replicas") == 1 and
                isinstance(version, str) and version.isdigit(),
                "Go Deployment replicas or resource version changed before stop")
        target.kubectl("scale", f"deployment/{target.deployment}", "--current-replicas=1",
                       f"--resource-version={version}", "--replicas=0", timeout=30)
        target.kubectl("rollout", "status", f"deployment/{target.deployment}", "--timeout=180s", timeout=195)
        stopped = target.json("get", "deployment", target.deployment)
        require(stopped.get("spec", {}).get("replicas") == 0 and
                stopped.get("status", {}).get("readyReplicas", 0) == 0,
                "Go Deployment did not stop")
        endpoints = target.json("get", "endpointslices", "-l",
                                f"kubernetes.io/service-name={target.business}")
        require(not any(endpoint.get("conditions", {}).get("ready") is True for item in
                        endpoints.get("items", []) for endpoint in (item.get("endpoints") or [])),
                "Go Service still has a ready endpoint after stop")
        java = target.json("get", "deployment", target.prefix + "product-api-legacy")
        require(java.get("status", {}).get("readyReplicas", 0) > 0,
                "Java Product is not ready after Go stop")
        after = role_snapshot(target)
        record_snapshot("after stop", after)
        require(after == before, "catalog/schema identity changed during local stop")
        print("product-deployment: local Go Deployment scaled to zero; Java and data preserved")


if __name__ == "__main__":
    try:
        main()
    except (DeploymentError, KeyError, TypeError, ValueError) as error:
        print(f"product-deployment: {error}", file=sys.stderr)
        raise SystemExit(1) from None
