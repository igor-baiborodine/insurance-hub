#!/usr/bin/env python3
"""Pure deployment guard tests; no shared Kubernetes target is contacted."""

from __future__ import annotations

import os
from pathlib import Path
import sys
import unittest
from unittest.mock import patch
from contextlib import nullcontext

sys.path.insert(0, str(Path(__file__).resolve().parent))
import deployment  # noqa: E402


class DeploymentGuardTests(unittest.TestCase):
    def test_rendered_local_inventory_and_image_pin(self) -> None:
        values = {"DEPLOY_ENV": "local-dev", "DEPLOY_CONTEXT": "kind-local-dev-insurance-hub",
                  "DEPLOY_SVC_NS": "local-dev-all", "DEPLOY_DATA_NS": "local-dev-all",
                  "DEPLOY_IMAGE_REF": "insurance-hub-product-api:git-cbe942ab9328"}
        with patch.dict(os.environ, values):
            target = deployment.Target()
            yaml, items, image = target.rendered()
            self.assertIn("kind: Deployment", yaml)
            self.assertEqual(image, values["DEPLOY_IMAGE_REF"])
            self.assertEqual(len(items), 4)
            os.environ["DEPLOY_IMAGE_REF"] = "insurance-hub-product-api:git-000000000000"
            with self.assertRaisesRegex(deployment.DeploymentError, "DEPLOY_IMAGE_REF"):
                target.rendered()

    def test_target_requires_exact_coordinates_before_kubectl(self) -> None:
        with patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(deployment.DeploymentError, "DEPLOY_ENV"):
                deployment.Target()
        for bad in ("qa", "../product", ""):
            with patch.dict(os.environ, {"DEPLOY_ENV": "local-dev", "DEPLOY_CONTEXT": bad}, clear=True):
                with self.assertRaisesRegex(deployment.DeploymentError, "DEPLOY_CONTEXT"):
                    deployment.Target()

    def test_active_context_and_namespace_are_never_switched(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.context = "kind-local-dev-insurance-hub"
        target.namespace = "local-dev-all"
        target.data_namespace = "local-dev-all"
        with patch.object(deployment, "run", return_value="qa-insurance-hub"), \
             patch.object(target, "json") as kubernetes:
            with self.assertRaisesRegex(deployment.DeploymentError, "active Kubernetes context"):
                target.coordinates()
            kubernetes.assert_not_called()
        with patch.object(deployment, "run", side_effect=[target.context, "default"]), \
             patch.object(target, "json") as kubernetes:
            with self.assertRaisesRegex(deployment.DeploymentError, "active Kubernetes namespace"):
                target.coordinates()
            kubernetes.assert_not_called()

    def test_snapshot_rejects_unexpected_output_fields(self) -> None:
        safe = {"rows": 4, "catalog": "a" * 32, "columns": "b" * 32, "tableOwner": "product"}
        with patch.object(deployment, "role_action", return_value=deployment.json.dumps(safe)):
            self.assertEqual(deployment.role_snapshot(object()), safe)
        with patch.object(deployment, "role_action", return_value=deployment.json.dumps(
                {**safe, "database-url": "synthetic-secret"})):
            with self.assertRaisesRegex(deployment.DeploymentError, "unexpected catalog identity"):
                deployment.role_snapshot(object())

    def test_volume_guard_accepts_kubernetes_default_and_rejects_other_secret(self) -> None:
        desired = [{"name": "postgres-ca", "secret": {"secretName": "reader", "items":
                    [{"key": "ca.crt", "path": "ca.crt"}]}}]
        actual = [{"name": "postgres-ca", "secret": {**desired[0]["secret"], "defaultMode": 420}}]
        self.assertTrue(deployment.matching_volumes(actual, desired))
        actual[0]["secret"]["secretName"] = "java-owner"
        self.assertFalse(deployment.matching_volumes(actual, desired))

    def test_reader_secret_guard_rejects_extra_key_before_docker(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.secret = "local-dev-postgres-product-reader-creds"
        target.environment = "local-dev"
        target.json = lambda *args: {"kind": "Secret", "data": {
            "database-url": "synthetic-secret", "ca.crt": "test-ca", "owner-password": "synthetic-secret"}}
        with patch.object(deployment, "run") as command:
            with self.assertRaisesRegex(deployment.DeploymentError, "unexpected keys"):
                deployment.secret_and_image(target, "insurance-hub-product-api:git-123456789abc")
            command.assert_not_called()

    def test_qa_mutation_rejected_before_cluster_access(self) -> None:
        values = {"DEPLOY_ENV": "qa", "DEPLOY_CONTEXT": "qa-insurance-hub",
                  "DEPLOY_SVC_NS": "qa-svc", "DEPLOY_DATA_NS": "qa-data"}
        with patch.dict(os.environ, values, clear=True), patch.object(deployment.Target, "coordinates") as coordinates:
            for operation in ("image-load", "deploy", "stop"):
                with patch.object(sys, "argv", ["deployment.py", operation]):
                    with self.assertRaisesRegex(deployment.DeploymentError, "forbidden"):
                        deployment.main()
            coordinates.assert_not_called()

    def test_route_reference_is_exact(self) -> None:
        names = {"qa-product-api", "qa-product-api-management"}
        self.assertFalse(deployment.refers_to_go({"backendRefs": [{"name": "qa-product-api-legacy"}]}, names))
        self.assertTrue(deployment.refers_to_go({"rules": [{"backendRefs": [{"name": "qa-product-api"}]}]}, names))
        self.assertTrue(deployment.refers_to_go({"serviceName": "qa-product-api-management"}, names))

    def test_effective_route_scan_rejects_go_backend(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.context = "qa-insurance-hub"
        target.business = "qa-product-api"
        target.management = "qa-product-api-management"
        seen = []

        def listing(*args):
            seen.append(args)
            if args[1] == "httproutes":
                return {"items": [{"spec": {"rules": [{"backendRefs": [{"name": "qa-product-api"}]}]}}]}
            return {"items": []}

        target.json = listing
        resources = "ingresses.networking.k8s.io\ngateways.gateway.networking.k8s.io\nhttproutes.gateway.networking.k8s.io\n"
        with patch.object(deployment, "run", return_value=resources):
            with self.assertRaisesRegex(deployment.DeploymentError, "httproute routes"):
                target.routes()
        self.assertIn(("get", "httproutes", "--all-namespaces"), seen)

    def test_owned_forwards_close_after_partial_failure(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.context = "kind-local-dev-insurance-hub"
        target.namespace = "local-dev-all"
        target.business = "local-dev-product-api"
        target.management = "local-dev-product-api-management"

        class Child:
            def __init__(self, failed: bool) -> None:
                self.failed = failed
                self.terminated = False
                self.waited = False

            def poll(self) -> int | None:
                return 1 if self.failed else (0 if self.terminated else None)

            def terminate(self) -> None:
                self.terminated = True

            def wait(self, timeout: int) -> int:
                self.waited = True
                return 0

        first, second = Child(False), Child(True)
        with patch.object(deployment.subprocess, "Popen", side_effect=[first, second]), \
             patch.object(deployment.socket, "create_connection", return_value=nullcontext()):
            with self.assertRaisesRegex(deployment.DeploymentError, "port-forward for grpc exited"):
                with deployment.forwards(target):
                    pass
        self.assertTrue(first.terminated)
        self.assertTrue(first.waited and second.waited)

    def test_live_rejects_selector_overlap_and_external_exposure(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.environment = "local-dev"
        target.prefix = "local-dev-"
        target.deployment = "local-dev-product-api"
        target.business = target.deployment
        target.management = target.deployment + "-management"
        labels = {"app.kubernetes.io/name": "product-api", "app.kubernetes.io/component": "api"}
        legacy_labels = {"app.kubernetes.io/name": "product-api-legacy"}
        deployment_doc = {"kind": "Deployment", "metadata": {"name": target.deployment,
                          "labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"}},
                          "spec": {"selector": {"matchLabels": labels}, "template": {"metadata":
                          {"labels": {**labels, "app.kubernetes.io/instance": "insurance-hub-product-api"}}, "spec":
                          {"containers": [{"image": "insurance-hub-product-api:git-123456789abc"}]}}}}
        services = [{"kind": "Service", "metadata": {"name": name,
                     "labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"}},
                     "spec": {"type": "ClusterIP", "selector": labels, "ports": [{"port": port}]}}
                    for name, port in ((target.business, 8081), (target.management, 8080))]
        config = {"kind": "ConfigMap", "metadata": {"name": "local-dev-product-config-hash"},
                  "data": {"SERVICE_NAME": "product-service"}}
        items = [deployment_doc, *services, config]
        state = {
            ("deployment", "local-dev-product-api", "--ignore-not-found"): {},
            ("deployment", "local-dev-product-api-legacy"): {"spec": {"selector": {"matchLabels": legacy_labels}}},
            ("service", target.business, "--ignore-not-found"): {"kind": "Service", "metadata":
                {"labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"}},
                "spec": services[0]["spec"]},
            ("service", target.management, "--ignore-not-found"): {"kind": "Service", "metadata":
                {"labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"}},
                "spec": services[1]["spec"]},
            ("pods", "-l", "app.kubernetes.io/name=product-api"): {"items": []},
            ("endpointslices", "-l", "kubernetes.io/service-name=local-dev-product-api"): {"items": []},
            ("service", "local-dev-product-api-legacy"): {"spec": {"selector": legacy_labels}},
            ("endpointslices", "-l", "kubernetes.io/service-name=local-dev-product-api-legacy"):
                {"items": [{"endpoints": [{"conditions": {"ready": True}}]}]},
            ("configmap", "local-dev-product-config-hash", "--ignore-not-found"):
                {"kind": "ConfigMap", "data": config["data"]},
            ("services",): {"items": []},
            ("deployment", "local-dev-agent-portal-gateway-legacy"):
                {"spec": {"template": {"spec": {"containers": [{"env": [
                    {"name": "MICRONAUT_ENVIRONMENTS", "value": "local-dev"}]}]}}},
                 "status": {"readyReplicas": 1}},
        }
        target.json = lambda *args: state[args[1:]]
        target.routes = lambda: None
        target.live(items, require_deployment=False)
        state[("deployment", "local-dev-product-api-legacy")]["spec"]["selector"]["matchLabels"] = labels
        with self.assertRaisesRegex(deployment.DeploymentError, "selectors overlap"):
            target.live(items, require_deployment=False)
        state[("deployment", "local-dev-product-api-legacy")]["spec"]["selector"]["matchLabels"] = legacy_labels
        state[("service", target.business, "--ignore-not-found")]["spec"] = {
            **services[0]["spec"], "type": "LoadBalancer"}
        with self.assertRaisesRegex(deployment.DeploymentError, "external exposure"):
            target.live(items, require_deployment=False)
        state[("service", target.business, "--ignore-not-found")]["spec"] = services[0]["spec"]
        state[("services",)] = {"items": [{"metadata": {"name": "extra-public"},
            "spec": {"type": "LoadBalancer", "selector": labels}}]}
        with self.assertRaisesRegex(deployment.DeploymentError, "unexpected Service"):
            target.live(items, require_deployment=False)


if __name__ == "__main__":
    unittest.main()
