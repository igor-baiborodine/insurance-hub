#!/usr/bin/env python3
"""Pure deployment guard tests; no shared Kubernetes target is contacted."""

from __future__ import annotations

import os
import signal
import sys
import unittest
from contextlib import nullcontext
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import deployment_lifecycle  # noqa: E402
import product_deployment_checks as deployment  # noqa: E402


class DeploymentGuardTests(unittest.TestCase):
    def test_cli_signal_boundaries_preserve_status_and_unwind(self) -> None:
        for owner in (deployment, deployment_lifecycle):
            for requested_signal, expected_status in (
                (signal.SIGINT, 130),
                (signal.SIGTERM, 143),
            ):
                cleaned = False

                def interrupted(_argv=None, selected_signal=requested_signal):
                    nonlocal cleaned
                    try:
                        os.kill(os.getpid(), selected_signal)
                    finally:
                        cleaned = True
                    return 0

                with patch.object(owner, "main", interrupted):
                    self.assertEqual(owner.cli([]), expected_status)
                self.assertTrue(cleaned)

    def test_probe_comparison_accepts_only_the_known_server_default(self) -> None:
        declared = {"httpGet": {"path": "/livez", "port": "management"}, "failureThreshold": 30}
        self.assertTrue(deployment.matching_probe(declared, declared))
        self.assertTrue(deployment.matching_probe({**declared, "successThreshold": 1}, declared))
        self.assertFalse(deployment.matching_probe({**declared, "successThreshold": 2}, declared))
        self.assertFalse(deployment.matching_probe({**declared, "periodSeconds": 3}, declared))

    def test_stopped_endpoint_slice_may_have_null_endpoints(self) -> None:
        endpoints = {"items": [{"endpoints": None}]}
        self.assertFalse(
            any(
                endpoint.get("conditions", {}).get("ready") is True
                for item in endpoints["items"]
                for endpoint in (item.get("endpoints") or [])
            )
        )

    def test_rendered_local_inventory_and_image_pin(self) -> None:
        values = {
            "DEPLOY_ENV": "local-dev",
            "DEPLOY_CONTEXT": "kind-local-dev-insurance-hub",
            "DEPLOY_SVC_NS": "local-dev-all",
            "DEPLOY_DATA_NS": "local-dev-all",
            "DEPLOY_IMAGE_REF": "insurance-hub-product-api:1.9.0-local.c977bbcd879d",
        }
        with patch.dict(os.environ, values):
            target = deployment.Target()
            with patch.object(target, "kubectl", wraps=target.kubectl) as kubectl:
                yaml, items, image = target.rendered()
                self.assertIn("kind: Deployment", yaml)
                self.assertEqual(image, values["DEPLOY_IMAGE_REF"])
                self.assertEqual(len(items), 4)
                os.environ["DEPLOY_IMAGE_REF"] = (
                    "insurance-hub-product-api:1.9.0-local.000000000000"
                )
                with self.assertRaisesRegex(deployment.DeploymentError, "DEPLOY_IMAGE_REF"):
                    target.rendered()
                self.assertTrue(all(call.args[0] == "kustomize" for call in kubectl.call_args_list))

    def test_target_requires_exact_coordinates_before_kubectl(self) -> None:
        with patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(deployment.DeploymentError, "DEPLOY_ENV"):
                deployment.Target()
        for bad in ("qa", "../product", ""):
            with patch.dict(
                os.environ, {"DEPLOY_ENV": "local-dev", "DEPLOY_CONTEXT": bad}, clear=True
            ):
                with self.assertRaisesRegex(deployment.DeploymentError, "DEPLOY_CONTEXT"):
                    deployment.Target()

    def test_active_context_and_namespace_are_never_switched(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.context = "kind-local-dev-insurance-hub"
        target.namespace = "local-dev-all"
        target.data_namespace = "local-dev-all"
        with (
            patch.object(deployment, "run", return_value="qa-insurance-hub"),
            patch.object(target, "json") as kubernetes,
        ):
            with self.assertRaisesRegex(deployment.DeploymentError, "active Kubernetes context"):
                target.coordinates()
            kubernetes.assert_not_called()
        with (
            patch.object(deployment, "run", side_effect=[target.context, "default"]),
            patch.object(target, "json") as kubernetes,
        ):
            with self.assertRaisesRegex(deployment.DeploymentError, "active Kubernetes namespace"):
                target.coordinates()
            kubernetes.assert_not_called()

    def test_snapshot_rejects_unexpected_output_fields(self) -> None:
        safe = {"rows": 4, "catalog": "a" * 32, "columns": "b" * 32, "tableOwner": "product"}
        with patch.object(deployment, "role_action", return_value=deployment.json.dumps(safe)):
            self.assertEqual(deployment.role_snapshot(object()), safe)
        with patch.object(
            deployment,
            "role_action",
            return_value=deployment.json.dumps({**safe, "database-url": "synthetic-secret"}),
        ):
            with self.assertRaisesRegex(deployment.DeploymentError, "unexpected catalog identity"):
                deployment.role_snapshot(object())

    def test_volume_guard_accepts_kubernetes_default_and_rejects_other_secret(self) -> None:
        desired = [
            {
                "name": "postgres-ca",
                "secret": {"secretName": "reader", "items": [{"key": "ca.crt", "path": "ca.crt"}]},
            }
        ]
        actual = [{"name": "postgres-ca", "secret": {**desired[0]["secret"], "defaultMode": 420}}]
        self.assertTrue(deployment.matching_volumes(actual, desired))
        actual[0]["secret"]["secretName"] = "java-owner"
        self.assertFalse(deployment.matching_volumes(actual, desired))

    def test_declared_previous_image_is_allowed_only_during_local_termination(self) -> None:
        desired = "insurance-hub-product-api:git-222222222222"
        previous = "insurance-hub-product-api:git-111111111111"
        pod = {"metadata": {}, "spec": {"containers": [{"image": previous}]}}
        self.assertTrue(deployment.pod_image_allowed(pod, desired, previous, "local-dev", False))
        self.assertFalse(deployment.pod_image_allowed(pod, desired, previous, "local-dev", True))
        pod["metadata"]["deletionTimestamp"] = "2026-10-13T18:30:00Z"
        self.assertTrue(deployment.pod_image_allowed(pod, desired, previous, "local-dev", True))
        self.assertFalse(deployment.pod_image_allowed(pod, desired, previous, "qa", True))
        self.assertFalse(deployment.pod_image_allowed(pod, desired, "", "local-dev", True))

    def test_reader_secret_guard_rejects_extra_key_before_docker(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.secret = "local-dev-postgres-product-reader-creds"
        target.environment = "local-dev"
        target.json = lambda *args: {
            "kind": "Secret",
            "data": {
                "database-url": "synthetic-secret",
                "ca.crt": "test-ca",
                "owner-password": "synthetic-secret",
            },
        }
        with patch.object(deployment, "run") as command:
            with self.assertRaisesRegex(deployment.DeploymentError, "unexpected keys"):
                deployment.secret_and_image(target, "insurance-hub-product-api:git-123456789abc")
            command.assert_not_called()

    def test_versioned_local_image_must_match_its_source_label(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.secret = "local-dev-postgres-product-reader-creds"
        target.environment = "local-dev"
        target.json = lambda *args: {
            "kind": "Secret",
            "data": {"database-url": "synthetic-secret", "ca.crt": "test-ca"},
        }
        image = "insurance-hub-product-api:1.9.0-local.c977bbcd879d"
        details = {
            "Config": {
                "Labels": {
                    "org.opencontainers.image.revision": "c977bbcd879d620e64855f46a85bc717362d5f81"
                }
            }
        }
        with patch.object(deployment, "run", return_value=deployment.json.dumps([details])):
            deployment.secret_and_image(target, image)
            with self.assertRaisesRegex(deployment.DeploymentError, "source revision differs"):
                deployment.secret_and_image(
                    target, "insurance-hub-product-api:1.9.0-local.000000000000"
                )
            with self.assertRaisesRegex(deployment.DeploymentError, "versioned tag"):
                deployment.secret_and_image(target, "insurance-hub-product-api:1.9.0")

    def test_qa_mutation_rejected_before_cluster_access(self) -> None:
        values = {
            "DEPLOY_ENV": "qa",
            "DEPLOY_CONTEXT": "qa-insurance-hub",
            "DEPLOY_SVC_NS": "qa-svc",
            "DEPLOY_DATA_NS": "qa-data",
        }
        with (
            patch.dict(os.environ, values, clear=True),
            patch.object(deployment.Target, "coordinates") as coordinates,
        ):
            for operation in ("image-load", "deploy", "stop"):
                with patch.object(sys, "argv", ["deployment.py", operation]):
                    with self.assertRaisesRegex(deployment.DeploymentError, "forbidden"):
                        deployment.main()
            coordinates.assert_not_called()

    def test_qa_release_owner_requires_matching_source_dependency_and_digest(self) -> None:
        with patch.dict(
            os.environ,
            {
                "DEPLOY_ENV": "qa",
                "DEPLOY_CONTEXT": "qa-insurance-hub",
                "DEPLOY_SVC_NS": "qa-svc",
                "DEPLOY_DATA_NS": "qa-data",
            },
        ):
            target = deployment.Target()
        revision = "a" * 40
        release_id = "0123456789abcdef"
        identity = release_id + "0" * 48
        ready = {"conditions": [{"type": "Ready", "status": "True"}]}
        annotation = {"insurance-hub.io/release-identity": identity}
        source_ref = {"kind": "GitRepository", "name": f"qa-product-release-{release_id}"}
        shared_revision = "sha1:" + "b" * 40
        state = {
            ("gitrepository", "insurance-hub", "-n", "flux-system"): {
                "spec": {"ref": {"branch": "main"}},
                "status": {**ready, "artifact": {"revision": shared_revision}},
            },
            ("kustomization", "qa-svc", "-n", "flux-system"): {
                "spec": {
                    "path": "./k8s/overlays/qa/svc",
                    "sourceRef": {"kind": "GitRepository", "name": "insurance-hub"},
                    "prune": True,
                    "wait": True,
                },
                "status": {**ready, "lastAppliedRevision": shared_revision},
            },
            ("kustomization", "qa-product-api", "-n", "flux-system", "--ignore-not-found"): {
                "kind": "Kustomization",
                "metadata": {"annotations": annotation},
                "spec": {
                    "path": "./k8s/overlays/qa/svc/product",
                    "sourceRef": source_ref,
                    "dependsOn": [{"name": f"qa-product-init-{release_id}-a2"}],
                    "images": [
                        {
                            "name": "insurance-hub-product-api",
                            "newName": "ghcr.io/igor-baiborodine/insurance-hub/product-api",
                            "digest": "sha256:" + "c" * 64,
                        }
                    ],
                },
                "status": ready,
            },
            ("gitrepository", f"qa-product-release-{release_id}", "-n", "flux-system"): {
                "metadata": {"annotations": annotation},
                "spec": {"ref": {"commit": revision}},
                "status": {**ready, "artifact": {"revision": "sha1:" + revision}},
            },
            ("kustomization", f"qa-product-init-{release_id}-a2", "-n", "flux-system"): {
                "metadata": {"annotations": annotation},
                "spec": {
                    "path": "./k8s/overlays/qa/svc/product/administration",
                    "sourceRef": source_ref,
                    "images": [
                        {
                            "name": "insurance-hub-product-api",
                            "newName": "ghcr.io/igor-baiborodine/insurance-hub/product-api",
                            "digest": "sha256:" + "c" * 64,
                        }
                    ],
                },
                "status": ready,
            },
        }
        target.json = lambda *args: deepcopy(state[args[1:]])
        with patch.dict(os.environ, {"DEPLOY_SOURCE_REVISION": revision}, clear=False):
            target.flux_source(require_release=True)
            state[("kustomization", "qa-product-api", "-n", "flux-system", "--ignore-not-found")][
                "spec"
            ]["dependsOn"] = [{"name": "qa-product-init-stale-a1"}]
            with self.assertRaisesRegex(deployment.DeploymentError, "prerequisite identity"):
                target.flux_source(require_release=True)

    def test_route_reference_is_exact(self) -> None:
        names = {"qa-product-api", "qa-product-api-management"}
        self.assertFalse(
            deployment.refers_to_go({"backendRefs": [{"name": "qa-product-api-legacy"}]}, names)
        )
        self.assertTrue(
            deployment.refers_to_go(
                {"rules": [{"backendRefs": [{"name": "qa-product-api"}]}]}, names
            )
        )
        self.assertTrue(
            deployment.refers_to_go({"serviceName": "qa-product-api-management"}, names)
        )

    def test_effective_route_scan_rejects_go_backend(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.context = "qa-insurance-hub"
        target.business = "qa-product-api"
        target.management = "qa-product-api-management"
        seen = []

        def listing(*args):
            seen.append(args)
            if args[1] == "httproutes":
                return {
                    "items": [{"spec": {"rules": [{"backendRefs": [{"name": "qa-product-api"}]}]}}]
                }
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
        with (
            patch.object(deployment.subprocess, "Popen", side_effect=[first, second]),
            patch.object(deployment.socket, "create_connection", return_value=nullcontext()),
        ):
            with self.assertRaisesRegex(deployment.DeploymentError, "port-forward for grpc exited"):
                with deployment.forwards(target):
                    pass
        self.assertTrue(first.terminated)
        self.assertTrue(first.waited and second.waited)

    def test_forwards_use_business_80_and_management_8081(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.context = "kind-local-dev-insurance-hub"
        target.namespace = "local-dev-all"
        target.business = "local-dev-product-api"
        target.management = "local-dev-product-api-management"
        commands = []

        class Child:
            terminated = False

            def poll(self) -> int | None:
                return 0 if self.terminated else None

            def terminate(self) -> None:
                self.terminated = True

            def wait(self, timeout: int) -> int:
                return 0

        def start(command, **_kwargs):
            commands.append(command)
            return Child()

        with (
            patch.object(deployment.subprocess, "Popen", side_effect=start),
            patch.object(deployment.socket, "create_connection", return_value=nullcontext()),
        ):
            with deployment.forwards(target) as ports:
                self.assertEqual(set(ports), {"http", "grpc", "health"})
        self.assertEqual(
            [(command[8], command[9].split(":")[-1]) for command in commands],
            [
                ("service/local-dev-product-api", "80"),
                ("service/local-dev-product-api", "9090"),
                ("service/local-dev-product-api-management", "8081"),
            ],
        )

    def test_gateway_route_requires_serving_mounted_override_and_phase(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.prefix = "qa-"
        target.namespace = "qa-svc"
        target.business = "qa-product-api"
        config_name = "qa-gateway-product-route-config-h5t75t5b9d"
        config = (
            "micronaut:\n  http:\n    services:\n      product-service:\n"
            "        urls:\n"
            "          - http://qa-product-api-legacy.qa-svc.svc.cluster.local:80\n"
        )
        spec = {
            "containers": [
                {
                    "env": [
                        {
                            "name": "MICRONAUT_CONFIG_FILES",
                            "value": "/etc/product-route/application.yml",
                        }
                    ],
                    "volumeMounts": [
                        {
                            "name": "product-route-config",
                            "mountPath": "/etc/product-route",
                            "readOnly": True,
                        }
                    ],
                }
            ],
            "volumes": [{"name": "product-route-config", "configMap": {"name": config_name}}],
        }
        state = {
            ("deployment", "qa-agent-portal-gateway-legacy"): {
                "metadata": {"generation": 2},
                "spec": {"replicas": 1, "template": {"spec": spec}},
                "status": {"observedGeneration": 2, "readyReplicas": 1, "updatedReplicas": 1},
            },
            ("configmap", config_name): {"data": {"application.yml": config}},
            ("pods", "-l", "app.kubernetes.io/name=agent-portal-gateway-legacy"): {
                "items": [
                    {
                        "metadata": {"name": "qa-gateway-1", "uid": "one"},
                        "spec": spec,
                        "status": {
                            "phase": "Running",
                            "podIP": "10.0.0.1",
                            "containerStatuses": [{"ready": True, "imageID": "gateway@sha256:abc"}],
                        },
                    }
                ]
            },
            ("endpointslices", "-l", "kubernetes.io/service-name=qa-agent-portal-gateway-legacy"): {
                "items": [
                    {"endpoints": [{"conditions": {"ready": True}, "addresses": ["10.0.0.1"]}]}
                ]
            },
        }
        target.json = lambda _verb, *args: state[args]
        target.kubectl = lambda *_args, **_kwargs: config
        self.assertEqual(target.gateway_route("java")["gateway_pods"][0]["uid"], "one")
        pod_spec = deepcopy(spec)
        pod_spec["volumes"].append(
            {"name": "kube-api-access-example", "projected": {"sources": []}}
        )
        pod_spec["containers"][0]["volumeMounts"].append(
            {
                "name": "kube-api-access-example",
                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
                "readOnly": True,
            }
        )
        state[("pods", "-l", "app.kubernetes.io/name=agent-portal-gateway-legacy")]["items"][0][
            "spec"
        ] = pod_spec
        self.assertEqual(target.gateway_route("java")["gateway_pods"][0]["uid"], "one")
        pod_spec["volumes"][0]["configMap"]["name"] = "stale-product-route"
        with self.assertRaisesRegex(deployment.DeploymentError, "stale override"):
            target.gateway_route("java")
        pod_spec["volumes"][0]["configMap"]["name"] = config_name
        pod_spec["containers"][0]["volumeMounts"][0]["mountPath"] = "/stale-product-route"
        with self.assertRaisesRegex(deployment.DeploymentError, "stale override"):
            target.gateway_route("java")
        pod_spec["containers"][0]["volumeMounts"][0]["mountPath"] = "/etc/product-route"
        with self.assertRaisesRegex(deployment.DeploymentError, "wrong phase"):
            target.gateway_route("go")
        target.kubectl = lambda *_args, **_kwargs: "wrong mounted bytes"
        with self.assertRaisesRegex(deployment.DeploymentError, "mounted Product override differs"):
            target.gateway_route("java")

    def test_live_rejects_stale_java_and_external_exposure(self) -> None:
        target = deployment.Target.__new__(deployment.Target)
        target.environment = "local-dev"
        target.prefix = "local-dev-"
        target.deployment = "local-dev-product-api"
        target.business = target.deployment
        target.management = target.deployment + "-management"
        labels = {"app.kubernetes.io/name": "product-api", "app.kubernetes.io/component": "api"}
        deployment_doc = {
            "kind": "Deployment",
            "metadata": {
                "name": target.deployment,
                "labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"},
            },
            "spec": {
                "selector": {"matchLabels": labels},
                "template": {
                    "metadata": {
                        "labels": {
                            **labels,
                            "app.kubernetes.io/instance": "insurance-hub-product-api",
                        }
                    },
                    "spec": {
                        "containers": [{"image": "insurance-hub-product-api:git-123456789abc"}]
                    },
                },
            },
        }
        services = [
            {
                "kind": "Service",
                "metadata": {
                    "name": name,
                    "labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"},
                },
                "spec": {"type": "ClusterIP", "selector": labels, "ports": [{"port": port}]},
            }
            for name, port in ((target.business, 80), (target.management, 8081))
        ]
        config = {
            "kind": "ConfigMap",
            "metadata": {"name": "local-dev-product-config-hash"},
            "data": {"SERVICE_NAME": "product-service"},
        }
        items = [deployment_doc, *services, config]
        state = {
            ("deployment", "local-dev-product-api", "--ignore-not-found"): {},
            ("deployment", "local-dev-product-api-legacy", "--ignore-not-found"): {},
            ("service", target.business, "--ignore-not-found"): {
                "kind": "Service",
                "metadata": {"labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"}},
                "spec": services[0]["spec"],
            },
            ("service", target.management, "--ignore-not-found"): {
                "kind": "Service",
                "metadata": {"labels": {"app.kubernetes.io/instance": "insurance-hub-product-api"}},
                "spec": services[1]["spec"],
            },
            ("pods", "-l", "app.kubernetes.io/name=product-api"): {"items": []},
            ("endpointslices", "-l", "kubernetes.io/service-name=local-dev-product-api"): {
                "items": []
            },
            ("service", "local-dev-product-api-legacy", "--ignore-not-found"): {},
            ("configmap", "local-dev-product-config-hash", "--ignore-not-found"): {
                "kind": "ConfigMap",
                "data": config["data"],
            },
            ("services",): {"items": []},
            ("deployment", "local-dev-agent-portal-gateway-legacy"): {
                "spec": {
                    "template": {
                        "spec": {
                            "containers": [
                                {"env": [{"name": "MICRONAUT_ENVIRONMENTS", "value": "local-dev"}]}
                            ]
                        }
                    }
                },
                "status": {"readyReplicas": 1},
            },
        }
        target.json = lambda *args: state[args[1:]]
        target.routes = lambda: None
        target.live(items, require_deployment=False)
        state[("deployment", "local-dev-product-api-legacy", "--ignore-not-found")] = {
            "kind": "Deployment"
        }
        with self.assertRaisesRegex(deployment.DeploymentError, "stale Java"):
            target.live(items, require_deployment=False)
        state[("deployment", "local-dev-product-api-legacy", "--ignore-not-found")] = {}
        state[("service", target.business, "--ignore-not-found")]["spec"] = {
            **services[0]["spec"],
            "type": "LoadBalancer",
        }
        with self.assertRaisesRegex(deployment.DeploymentError, "external exposure"):
            target.live(items, require_deployment=False)
        state[("service", target.business, "--ignore-not-found")]["spec"] = services[0]["spec"]
        state[("services",)] = {
            "items": [
                {
                    "metadata": {"name": "extra-public"},
                    "spec": {"type": "LoadBalancer", "selector": labels},
                }
            ]
        }
        with self.assertRaisesRegex(deployment.DeploymentError, "unexpected Service"):
            target.live(items, require_deployment=False)


if __name__ == "__main__":
    unittest.main()
