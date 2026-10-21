#!/usr/bin/env python3
"""Pytest coverage for the repository-owned Alloy policy checker."""

from __future__ import annotations

from pathlib import Path

import pytest

import check_config as alloy_check

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]


def test_source_policy_requires_python_chart_injection_after_migration() -> None:
    # given
    values = (REPOSITORY_ROOT / "k8s/overlays/qa/infra/alloy/values.yaml").read_text(
        encoding="utf-8"
    )
    config = (REPOSITORY_ROOT / "k8s/overlays/qa/infra/alloy/config.alloy").read_text(
        encoding="utf-8"
    )
    makefiles = "\n".join(
        (REPOSITORY_ROOT / name).read_text(encoding="utf-8")
        for name in ("k8s/Makefile", "k8s/tests/validation.mk")
    )
    operations = (REPOSITORY_ROOT / "k8s/scripts/monitoring/operations.py").read_text(
        encoding="utf-8"
    )

    # when / then
    alloy_check.validate_source_policy(config, values, makefiles, operations)
    with pytest.raises(alloy_check.ValidationError, match="every Alloy render"):
        alloy_check.validate_source_policy(
            config,
            values,
            makefiles,
            operations.replace('f"alloy.configMap.content={args.alloy_config}"', '"missing"'),
        )


# Product data below is limited to the transitional legacy-Java contract. Shared Go
# behavior uses neutral fixtures and is not extended when another service is added.
PRODUCT_ROUTE_RULES = (
    (r"/products", r"(?i)get /products/?", "/products", "GET /products"),
    (
        r"/products/[^/]+",
        r"(?i)get /products/([^/]+|\{productCode\})",
        "/products/{productCode}",
        "GET /products/{productCode}",
    ),
)

GO_SERVICE_CASES = (
    ("alpha", "alpha"),
    ("multi-part", "multi_part"),
)


def go_resource(
    service_slug: str,
    version: str = "2.0.0",
    pod_uid: str = "pod-a",
) -> dict[str, str]:
    return {
        "service.namespace": "insurance-hub",
        "service.name": f"{service_slug}-service",
        "service.version": version,
        "service.instance.id": pod_uid,
        "deployment.environment.name": "qa",
        "insurance_hub.service.implementation": "go",
        "k8s.cluster.name": "qa-insurance-hub",
        "k8s.namespace.name": "qa-svc",
        "k8s.pod.uid": pod_uid,
        "k8s.container.name": f"{service_slug}-api",
    }


class TestIdentityPolicy:
    def test_java_enrichment_preserves_conflicts_and_misses(self) -> None:
        conflict = alloy_check.enrich_legacy_identity(
            {
                "service.name": "product-service",
                "service.version": "unexpected",
                "service.instance.id": "existing-instance",
                "insurance_hub.service.implementation": "unexpected",
            },
            {
                "service.version": "1.7.0",
                "k8s.pod.uid": "pod-a",
                "k8s.namespace.name": "qa-svc",
                "k8s.container.name": "product-api-legacy",
            },
            "product-service",
            "product-api-legacy",
        )
        assert "unexpected" == conflict["service.version"]
        assert "existing-instance" == conflict["service.instance.id"]
        assert "unexpected" == conflict["insurance_hub.service.implementation"]

        missed = alloy_check.enrich_legacy_identity(
            {"service.name": "product-service"},
            {},
            "product-service",
            "product-api-legacy",
        )
        assert "service.version" not in missed
        assert "service.instance.id" not in missed
        assert "java" == missed["insurance_hub.service.implementation"]

        wrong_name = alloy_check.enrich_legacy_identity(
            {"service.name": "conflicting-service"},
            {
                "service.version": "1.7.0",
                "k8s.pod.uid": "pod-a",
                "k8s.container.name": "product-api-legacy",
            },
            "product-service",
            "product-api-legacy",
        )
        assert "conflicting-service" == wrong_name["service.name"]
        assert "java" == wrong_name["insurance_hub.service.implementation"]

    def test_go_identity_rejects_conflicts_misses_and_mutable_versions(self) -> None:
        valid = go_resource("alpha")
        assert alloy_check.valid_go_identity(valid, "2.0.0")
        for key in (
            "service.namespace",
            "service.name",
            "service.version",
            "service.instance.id",
            "insurance_hub.service.implementation",
            "k8s.pod.uid",
        ):
            invalid = dict(valid)
            invalid.pop(key)
            assert not alloy_check.valid_go_identity(invalid, "2.0.0"), key
        for version in ("latest", "unconfigured", "sha-unpublished"):
            invalid = dict(valid)
            invalid["service.version"] = version
            assert not alloy_check.valid_go_identity(invalid, version)
        assert not alloy_check.valid_go_identity(valid, "2.1.0")

    def test_go_identity_uses_the_shared_container_service_convention(self) -> None:
        for service_slug, _ in GO_SERVICE_CASES:
            resource = go_resource(service_slug, pod_uid=f"pod-{service_slug}")
            assert alloy_check.valid_go_identity(resource, "2.0.0")
            assert not alloy_check.valid_go_identity(
                dict(resource, **{"service.name": "other-service"}), "2.0.0"
            )

    def test_resource_keeps_only_approved_attributes(self) -> None:
        resource = {
            key: f"value-{index}" for index, key in enumerate(alloy_check.RESOURCE_ATTRIBUTES)
        }
        resource["authorization"] = "forbidden"
        resource["enduser.id"] = "forbidden"
        assert set(alloy_check.RESOURCE_ATTRIBUTES) == set(alloy_check.sanitize_resource(resource))


class TestMetricPolicy:
    def setup_method(self) -> None:
        self.resource = go_resource("alpha")
        self.resource["authorization"] = "forbidden"

    def test_datapoint_keeps_only_approved_resource_and_metric_labels(self) -> None:
        result = alloy_check.sanitize_datapoint(
            "insurance_hub.alpha.requests",
            {
                "insurance_hub.alpha.operation": "list",
                "insurance_hub.alpha.transport": "http",
                "insurance_hub.alpha.outcome": "success",
                "http.response.status_code": 200,
                "record_id": "forbidden",
                "trace_id": "forbidden",
                "request_id": "forbidden",
            },
            self.resource,
        )
        assert set(alloy_check.METRIC_RESOURCE_LABELS) | {
            "insurance_hub.alpha.operation",
            "insurance_hub.alpha.transport",
            "insurance_hub.alpha.outcome",
            "http.response.status_code",
        } == set(result)
        assert "k8s.pod.uid" not in result
        assert "authorization" not in result

    def test_unknown_service_metric_is_removed(self) -> None:
        assert {} == alloy_check.sanitize_datapoint(
            "insurance_hub.alpha.unbounded", {"record_id": "x"}, self.resource
        )

    def test_services_use_the_shared_dimension_policy(self) -> None:
        for service_slug, service_domain in GO_SERVICE_CASES:
            resource = go_resource(service_slug)
            result = alloy_check.sanitize_datapoint(
                f"insurance_hub.{service_domain}.requests",
                {
                    f"insurance_hub.{service_domain}.operation": "read",
                    f"insurance_hub.{service_domain}.transport": "grpc",
                    f"insurance_hub.{service_domain}.outcome": "success",
                    "record_id": "forbidden",
                },
                resource,
            )
            assert f"insurance_hub.{service_domain}.operation" in result
            assert "record_id" not in result
            assert f"{service_slug}-service" == result["service_name"]
            assert {} == alloy_check.sanitize_datapoint(
                "insurance_hub.other.requests",
                {"insurance_hub.other.operation": "read"},
                resource,
            )

    def test_unrelated_metric_attributes_are_preserved(self) -> None:
        attributes = {"worker": "one", "authorization": "owned-by-other-route"}
        resource = {"service.name": "another-service"}
        assert attributes == alloy_check.sanitize_datapoint("go.memory.used", attributes, resource)


class TestSpanPolicy:
    def test_java_span_is_normalized_and_forbidden_fields_are_removed(self) -> None:
        name, attributes = alloy_check.sanitize_legacy_http_span(
            "get /products/PERSONAL_AUTO",
            {
                "http.method": "GET",
                "http.path": "/products/PERSONAL_AUTO",
                "http.status_code": 200,
                "authorization": "forbidden",
                "request_id": "forbidden",
                "error.message": "forbidden",
            },
            "product",
            PRODUCT_ROUTE_RULES,
        )
        assert "GET /products/{productCode}" == name
        assert "GET" == attributes["http.request.method"]
        assert "/products/{productCode}" == attributes["http.route"]
        assert 200 == attributes["http.response.status_code"]
        assert "http.path" not in attributes
        assert "authorization" not in attributes
        assert "error.message" not in attributes

    def test_unknown_java_route_is_bounded(self) -> None:
        name, attributes = alloy_check.sanitize_legacy_http_span(
            "get /products/raw/customer/path",
            {"http.method": "POST", "http.path": "/products/raw/customer/path"},
            "product",
            PRODUCT_ROUTE_RULES,
        )
        assert "HTTP _OTHER" == name
        assert "_OTHER" == attributes["http.request.method"]
        assert "http.route" not in attributes

    def test_go_span_uses_the_shared_safe_field_policy(self) -> None:
        attributes = alloy_check.sanitize_go_span(
            "alpha",
            {
                "insurance_hub.alpha.operation": "read",
                "insurance_hub.alpha.outcome": "success",
                "http.route": "/records",
                "record_id": "forbidden",
                "authorization": "forbidden",
            },
        )
        assert "read" == attributes["insurance_hub.alpha.operation"]
        assert "/records" == attributes["http.route"]
        assert "record_id" not in attributes
        assert "authorization" not in attributes


class TestLogPolicy:
    def test_log_target_policy_requires_each_collection_boundary(self) -> None:
        config = """
discovery.relabel "logs" {
  targets = argument.targets.value

  rule {
    source_labels = ["__meta_kubernetes_pod_label_app_kubernetes_io_name"]
    action = "keep"
    regex = argument.container_name.value
  }
  rule {
    source_labels = ["__meta_kubernetes_pod_label_environment"]
    action = "keep"
    regex = "qa"
  }
  rule {
    source_labels = ["__meta_kubernetes_pod_container_name"]
    action = "keep"
    regex = argument.container_name.value
  }
  rule {
    source_labels = ["__meta_kubernetes_pod_container_port_name"]
    action = "keep"
    regex = argument.port_name.value
  }
}
"""
        alloy_check.validate_log_target_policy(config)
        for source_label in (
            "__meta_kubernetes_pod_label_app_kubernetes_io_name",
            "__meta_kubernetes_pod_label_environment",
            "__meta_kubernetes_pod_container_name",
            "__meta_kubernetes_pod_container_port_name",
        ):
            invalid = config.replace(source_label, f"{source_label}_wrong", 1)
            with pytest.raises(alloy_check.ValidationError):
                alloy_check.validate_log_target_policy(invalid)

    def test_duplicate_port_targets_reduce_to_one_reader(self) -> None:
        common = {
            "namespace": "qa-svc",
            "app_name": "alpha-api",
            "environment": "qa",
            "container": "alpha-api",
            "pod_uid": "pod-a",
        }
        targets = [dict(common, port=port) for port in ("http", "grpc", "management")]
        targets.append(dict(common, port="http"))
        selected = alloy_check.select_service_targets(targets, "alpha-api", "http", "alpha-service")
        assert 1 == len(selected)
        assert "http" == selected[0]["port"]

    def test_workload_target_mismatches_are_rejected_before_log_read(self) -> None:
        allowed = {
            "namespace": "qa-svc",
            "app_name": "alpha-api",
            "environment": "qa",
            "container": "alpha-api",
            "port": "http",
            "pod_uid": "pod-a",
        }
        selected = alloy_check.select_service_targets(
            [allowed], "alpha-api", "http", "alpha-service"
        )
        assert 1 == len(selected)

        mismatches = {
            "namespace": "qa-monitoring",
            "app_name": "other-api",
            "environment": "local-dev",
            "container": "other-api",
            "port": "management",
        }
        for field, value in mismatches.items():
            rejected = alloy_check.select_service_targets(
                [dict(allowed, **{field: value})],
                "alpha-api",
                "http",
                "alpha-service",
            )
            assert [] == rejected, field

    def test_log_identity_and_index_allowlist(self) -> None:
        record = {
            "service_namespace": "insurance-hub",
            "service_name": "alpha-service",
            "service_version": "2.0.0",
            "service_instance_id": "pod-a",
            "deployment_environment_name": "qa",
            "service_implementation": "go",
            "level": "INFO",
            "trace_id": "forbidden-index-label",
            "record_id": "forbidden-index-label",
        }
        target = {
            "namespace": "qa-svc",
            "container": "alpha-api",
            "pod_uid": "pod-a",
            "service_version": "2.0.0",
        }
        target["service_name"] = "alpha-service"
        labels = alloy_check.log_labels(record, target)
        assert labels is not None
        assert set(alloy_check.LOKI_INDEX_LABELS) == set(labels or {})
        assert "trace_id" not in (labels or {})

        conflict = dict(record, service_version="latest")
        assert alloy_check.log_labels(conflict, target) is None
        for version in ("", "latest", "unconfigured", "sha-unpublished"):
            assert (
                alloy_check.log_labels(
                    dict(record, service_version=version), dict(target, service_version=version)
                )
                is None
            ), version
        assert alloy_check.log_labels(dict(record, level="DEBUG"), target) is None
        missing = dict(record)
        missing.pop("service_instance_id")
        assert alloy_check.log_labels(missing, target) is None

    def test_log_policy_reuses_service_registrations(self) -> None:
        registration_config = """
service_json_logs "multi-part" {
  targets        = discovery.kubernetes.qa_service_pods.targets
  container_name = "multi-part-api"
  port_name      = "http"
  service_name   = "multi-part-service"
  max_streams    = 8
}
"""
        alloy_check.validate_service_log_registrations(registration_config)
        with pytest.raises(alloy_check.ValidationError):
            alloy_check.validate_service_log_registrations(
                registration_config.replace(
                    'service_name   = "multi-part-service"',
                    'service_name   = "other-service"',
                )
            )

        targets = [
            {
                "namespace": "qa-svc",
                "app_name": "multi-part-api",
                "environment": "qa",
                "container": "multi-part-api",
                "port": port,
                "pod_uid": "pod-search",
                "service_version": "2.0.0",
            }
            for port in ("http", "grpc")
        ]
        selected = alloy_check.select_service_targets(
            targets, "multi-part-api", "http", "multi-part-service"
        )
        assert 1 == len(selected)

        record = {
            "service_namespace": "insurance-hub",
            "service_name": "multi-part-service",
            "service_version": "2.0.0",
            "service_instance_id": "pod-search",
            "deployment_environment_name": "qa",
            "service_implementation": "go",
            "level": "WARN",
        }
        labels = alloy_check.log_labels(record, selected[0])
        assert labels is not None
        assert "multi-part-service" == (labels or {}).get("service_name")

    def test_restart_position_identity_and_ephemeral_replacement(self) -> None:
        initial = {"pod_uid": "pod-a", "container": "alpha-api", "restart": "0"}
        restarted = {"pod_uid": "pod-a", "container": "alpha-api", "restart": "1"}
        replacement = {"pod_uid": "pod-b", "container": "alpha-api", "restart": "0"}
        assert alloy_check.position_key(initial) == alloy_check.position_key(restarted)
        assert alloy_check.position_key(initial) != alloy_check.position_key(replacement)


class TestRBACPolicy:
    def test_scoped_role_denies_unrelated_access(self) -> None:
        rendered = """
kind: Role
metadata:
  name: qa-alloy
  namespace: qa-svc
rules:
  - apiGroups:
    - ""
    resources:
    - pods
    verbs:
    - get
    - list
    - watch
  - apiGroups:
    - ""
    resources:
    - pods/log
    verbs:
    - get
---
kind: RoleBinding
metadata:
  name: qa-alloy
  namespace: qa-svc
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: qa-alloy
subjects:
  - kind: ServiceAccount
    name: qa-alloy
    namespace: qa-monitoring
"""
        alloy_check.validate_rendered_rbac(rendered)
        namespace, rules = alloy_check.parse_role(rendered)
        assert alloy_check.role_allows(namespace, rules, "qa-svc", "pods/log", "get")
        assert not alloy_check.role_allows(namespace, rules, "qa-svc", "secrets", "get")
        assert not alloy_check.role_allows(namespace, rules, "qa-monitoring", "pods/log", "get")

    def test_cluster_role_is_rejected(self) -> None:
        with pytest.raises(alloy_check.ValidationError):
            alloy_check.parse_role("kind: ClusterRole\nmetadata:\n  name: qa-alloy\n")
