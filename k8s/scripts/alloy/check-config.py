#!/usr/bin/env python3
"""Validate the repository-owned QA Alloy configuration and bounded policies.

Python 3.9 or newer is required. The checker is read-only and uses only the
standard library; Helm and Alloy execution remain owned by the k8s Makefile.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path
from typing import Iterable, Mapping, Optional, Sequence


ALLOY_CHART_VERSION = "1.13.1"
ALLOY_IMAGE_VERSION = "v1.20.1"

# Repository-wide pins, schemas, and safety budgets are intentionally explicit here.
# Service names, ports, metric domains, and stream budgets are discovered from the
# Alloy source or supplied by a test fixture; do not add a per-service registry.
QA_SERVICE_NAMESPACE = "qa-svc"

RESOURCE_ATTRIBUTES = (
    "service.namespace",
    "service.name",
    "service.version",
    "service.instance.id",
    "deployment.environment.name",
    "insurance_hub.service.implementation",
    "k8s.cluster.name",
    "k8s.namespace.name",
    "k8s.pod.uid",
    "k8s.container.name",
)

METRIC_RESOURCE_MAPPING = (
    ("service_name", "service.name"),
    ("service_namespace", "service.namespace"),
    ("service_version", "service.version"),
    ("service_instance_id", "service.instance.id"),
    ("deployment_environment_name", "deployment.environment.name"),
    ("service_implementation", "insurance_hub.service.implementation"),
    ("k8s_cluster_name", "k8s.cluster.name"),
    ("k8s_namespace_name", "k8s.namespace.name"),
)

METRIC_RESOURCE_LABELS = tuple(flat_key for flat_key, _ in METRIC_RESOURCE_MAPPING)

LOKI_INDEX_LABELS = (
    "service_namespace",
    "service_name",
    "deployment_environment_name",
    "service_implementation",
    "service_version",
    "k8s_namespace_name",
    "k8s_container_name",
    "level",
)

COMMON_SPAN_ATTRIBUTES = (
    "http.request.method",
    "http.route",
    "http.response.status_code",
    "url.scheme",
    "network.protocol.version",
    "rpc.system.name",
    "rpc.method",
    "rpc.response.status_code",
    "db.system.name",
    "db.namespace",
    "db.collection.name",
    "db.operation.name",
    "db.query.summary",
    "error.type",
)

COMMON_METRIC_DIMENSIONS = {
    "db.client.operation.duration": (
        "db.system.name",
        "db.namespace",
        "db.collection.name",
        "db.operation.name",
        "error.type",
    ),
    "db.client.connection.count": (
        "db.client.connection.pool.name",
        "db.client.connection.state",
    ),
    "db.client.connection.max": ("db.client.connection.pool.name",),
    "db.client.connection.timeouts": ("db.client.connection.pool.name",),
    "db.client.connection.wait_time": ("db.client.connection.pool.name",),
    "go.memory.used": ("go.memory.type",),
    "go.memory.limit": (),
    "go.memory.allocated": (),
    "go.memory.allocations": (),
    "go.memory.gc.goal": (),
    "go.memory.gc.cycles": (),
    "go.goroutine.count": (),
    "go.processor.limit": (),
}


class ValidationError(Exception):
    """A deterministic source or policy validation failure."""


def _block(config: str, marker: str) -> str:
    start = config.find(marker)
    if start < 0:
        raise ValidationError(f"missing configuration block: {marker}")
    opening = config.find("{", start)
    depth = 0
    for index in range(opening, len(config)):
        if config[index] == "{":
            depth += 1
        elif config[index] == "}":
            depth -= 1
            if depth == 0:
                return config[start : index + 1]
    raise ValidationError(f"unterminated configuration block: {marker}")


def _string_assignment(block: str, field: str) -> str:
    matches = re.findall(
        rf'(?m)^\s*{re.escape(field)}\s*=\s*"([^"]+)"\s*$', block
    )
    if len(matches) != 1:
        raise ValidationError(f"service log registration must set {field} exactly once")
    return matches[0]


def validate_service_log_registrations(config: str) -> None:
    names = re.findall(r'(?m)^service_json_logs\s+"([^"]+)"\s*\{', config)
    if not names:
        raise ValidationError("at least one service log registration is required")
    if len(names) != len(set(names)):
        raise ValidationError("service log registration names must be unique")

    for name in names:
        if not re.fullmatch(r"[a-z0-9-]+", name):
            raise ValidationError(f"invalid service log registration name: {name}")
        block = _block(config, f'service_json_logs "{name}"')
        if not re.search(
            r"(?m)^\s*targets\s*=\s*discovery\.kubernetes\.qa_service_pods\.targets\s*$",
            block,
        ):
            raise ValidationError(
                f"{name} log registration must use shared qa_service_pods targets"
            )

        container_name = _string_assignment(block, "container_name")
        port_name = _string_assignment(block, "port_name")
        service_name = _string_assignment(block, "service_name")
        if container_name != f"{name}-api":
            raise ValidationError(
                f"{name} log container is {container_name}, expected {name}-api"
            )
        if service_name != f"{name}-service":
            raise ValidationError(
                f"{name} log service is {service_name}, expected {name}-service"
            )
        if not re.fullmatch(r"[a-z][a-z0-9-]*", port_name):
            raise ValidationError(f"{name} log port name is invalid: {port_name}")

        stream_matches = re.findall(r"(?m)^\s*max_streams\s*=\s*([0-9]+)\s*$", block)
        if len(stream_matches) != 1 or int(stream_matches[0]) < 1:
            raise ValidationError(
                f"{name} log registration must set a positive max_streams once"
            )


def validate_source_policy(config: str, values: str, makefile: str) -> None:
    required_fragments = (
        'otelcol.receiver.zipkin "legacy_java"',
        'otelcol.receiver.otlp "otlp_ingress"',
        'otelcol.processor.k8sattributes "legacy_java"',
        'set(attributes["insurance_hub.pipeline.source"], "legacy_java")',
        'otelcol.processor.k8sattributes "qa_services"',
        'otelcol.processor.transform "qa_resource_identity"',
        'attributes["insurance_hub.expected.service.name"]',
        'replace_pattern(attributes["insurance_hub.expected.service.name"], "-api$", "-service")',
        'attributes["insurance_hub.expected.service.domain"]',
        'replace_pattern(attributes["insurance_hub.expected.service.domain"], "-", "_")',
        'otelcol.processor.filter "service_identity_policies"',
        'otelcol.processor.transform "service_trace_policies"',
        'otelcol.processor.filter "service_metric_policies"',
        r'Concat(["^insurance_hub\\.", resource.attributes["insurance_hub.expected.service.domain"]',
        'keep_matching_keys(attributes',
        "set(events, nil)",
        "set(links, nil)",
        'discovery.kubernetes "qa_service_pods"',
        'declare "service_json_logs"',
        "regex = argument.container_name.value",
        "regex = argument.port_name.value",
        '(eq .record_service_name .target_service_name)',
        "forward_to = [loki.write.service.receiver]",
        'loki.write "service"',
        'source_labels = ["__meta_kubernetes_pod_container_port_name"]',
        'format            = "RFC3339Nano"',
        'selector = `{level!~"INFO|WARN|ERROR"}`',
        r'template = "\u007b\u007b if and',
        "stage.pack {",
        'target_service_namespace               = "service_namespace"',
        'target_service_version                 = "service_version"',
        'service_namespace               = "target_service_namespace"',
        'service_version                 = "target_service_version"',
        'trace_id             = "record_trace_id"',
        'span_id              = "record_span_id"',
        '(ne .target_service_version "latest")',
        '(ne .target_service_version "unconfigured")',
        '(ne .target_service_version "sha-unpublished")',
        'otelcol.exporter.otlp "tempo"',
        'otelcol.exporter.otlp "zipkin_bridge"',
        'keep_identifying_resource_attributes = true',
        'resource_to_telemetry_conversion     = false',
        'limit          = "800MiB"',
        'spike_limit    = "160MiB"',
        'queue_size        = 2048',
        'truncate_frequency = "5m"',
        'max_keepalive_time = "15m"',
    )
    comparison_config = config.replace(r'\"', '"')
    normalized_config = re.sub(r"\s+", " ", comparison_config)
    missing = [
        fragment
        for fragment in required_fragments
        if re.sub(r"\s+", " ", fragment) not in normalized_config
    ]
    if missing:
        raise ValidationError("missing required Alloy policy: " + ", ".join(missing))
    if "set(events, [])" in config or "set(links, [])" in config:
        raise ValidationError("span events and links must use typed nil clearing")

    forbidden_fragments = (
        'attributes["deployment.environment"]',
        'attributes["cluster.name"]',
        'container.image.tag"',
        "deployment_name_from_replicaset",
    )
    present = [fragment for fragment in forbidden_fragments if fragment in config]
    if present:
        raise ValidationError("deprecated Alloy policy remains: " + ", ".join(present))

    qa_association = _block(config, 'otelcol.processor.k8sattributes "qa_services"')
    if 'key   = "app.kubernetes.io/name"' in qa_association:
        raise ValidationError("shared QA service association must not select one service")
    pod_discovery = _block(config, 'discovery.kubernetes "qa_service_pods"')
    if "selectors {" in pod_discovery:
        raise ValidationError("shared QA service Pod discovery must not select one service")

    mapped = tuple(
        re.findall(
            r'set\(attributes\["([^"]+)"\], resource\.attributes\["[^"]+"\]\)',
            _block(config, 'otelcol.processor.transform "service_metric_policies"'),
        )
    )
    if mapped != METRIC_RESOURCE_LABELS:
        raise ValidationError(
            f"metric resource labels are {mapped}, expected {METRIC_RESOURCE_LABELS}"
        )

    metric_transform = _block(config, 'otelcol.processor.transform "service_metric_policies"')
    metric_dimension_allowlists = [
        line.strip()
        for line in metric_transform.splitlines()
        if "keep_matching_keys(attributes" in line and "metric.name" in line
    ]
    if len(metric_dimension_allowlists) != 7:
        raise ValidationError("shared Go metric policy must contain seven dimension allowlists")
    if any(
        'resource.attributes["insurance_hub.service.implementation"] == "go"'
        not in line
        for line in metric_dimension_allowlists
    ):
        raise ValidationError("metric datapoint allowlists must remain Go-service scoped")
    if re.search(r"insurance_hub\\\\\.[a-z0-9_]+", metric_transform):
        raise ValidationError("shared Go metric policy must not encode one service namespace")
    if metric_transform.count('resource.attributes["insurance_hub.expected.service.domain"]') < 5:
        raise ValidationError("metric policy must bind instruments and dimensions to service domain")

    for marker in (
        'otelcol.processor.transform "service_trace_policies"',
        'otelcol.processor.transform "service_metric_policies"',
    ):
        allowlist_statements = re.findall(
            r'keep_keys\(attributes, \[([^]]+)\]\)[^`]*', _block(config, marker)
        )
        resource_allowlists = [
            tuple(re.findall(r'"([^"]+)"', statement))
            for statement in allowlist_statements
            if tuple(re.findall(r'"([^"]+)"', statement)) == RESOURCE_ATTRIBUTES
        ]
        if len(resource_allowlists) != 1:
            raise ValidationError(f"{marker} must contain one shared resource allowlist")

    java_routes = (
        (
            'otelcol.receiver.zipkin "legacy_java"',
            "otelcol.processor.memory_limiter.legacy_java.input",
        ),
        (
            'otelcol.processor.memory_limiter "legacy_java"',
            "otelcol.processor.k8sattributes.legacy_java.input",
        ),
        (
            'otelcol.processor.k8sattributes "legacy_java"',
            "otelcol.processor.transform.legacy_java_identity.input",
        ),
        (
            'otelcol.processor.transform "legacy_java_identity"',
            "otelcol.processor.transform.service_trace_policies.input",
        ),
    )
    broken_java_routes = [
        marker for marker, destination in java_routes if destination not in _block(config, marker)
    ]
    trace_batch = _block(config, 'otelcol.processor.batch "traces"')
    for exporter in (
        "otelcol.exporter.otlp.tempo.input",
        "otelcol.exporter.otlp.zipkin_bridge.input",
    ):
        if exporter not in trace_batch:
            broken_java_routes.append(exporter)
    if broken_java_routes:
        raise ValidationError(
            "legacy Java trace route is incomplete: " + ", ".join(broken_java_routes)
        )

    label_keep = tuple(re.findall(r'"([^"]+)"', _block(config, "stage.label_keep")))
    if label_keep != LOKI_INDEX_LABELS:
        raise ValidationError(
            f"Loki index labels are {label_keep}, expected {LOKI_INDEX_LABELS}"
        )

    if f"tag: {ALLOY_IMAGE_VERSION}" not in values:
        raise ValidationError(f"Alloy image must remain pinned to {ALLOY_IMAGE_VERSION}")
    if "storagePath: /tmp/alloy" not in values:
        raise ValidationError("Alloy position/WAL storage must remain explicitly ephemeral")
    if "persistentVolumeClaim" in values or "storagePath: /var/" in values:
        raise ValidationError("Alloy position/WAL storage must not become persistent in this pilot")
    chart_pin = f"ALLOY_HELM_CHART_VERSION := {ALLOY_CHART_VERSION}"
    if chart_pin not in makefile:
        raise ValidationError(f"Alloy chart must remain pinned with {chart_pin}")
    image_pin = f"ALLOY_IMAGE := grafana/alloy:{ALLOY_IMAGE_VERSION}"
    if image_pin not in makefile:
        raise ValidationError(f"Alloy validator must remain pinned with {image_pin}")
    if '--values "$(ALLOY_VALUES_PATH)"' not in makefile:
        raise ValidationError("Alloy commands must consume the validated QA values path")
    config_injection = '--set-file alloy.configMap.content="$(ALLOY_CONFIG_PATH)"'
    if makefile.count(config_injection) != 4:
        raise ValidationError(
            "every Alloy render and install must inject config.alloy with --set-file"
        )
    if "    content: |-" in values:
        raise ValidationError("values.yaml must not duplicate the Alloy configuration source")
    if not re.search(
        r"(?m)^  configMap:\n(?:    #.*\n)*    create: true$", values
    ):
        raise ValidationError("values.yaml must keep chart-owned ConfigMap creation enabled")

    validate_service_log_registrations(config)

    existing_loki_writer = _block(config, 'loki.write "qa_loki"')
    service_loki_writer = _block(config, 'loki.write "service"')
    if "max_streams" in existing_loki_writer:
        raise ValidationError("service stream limits must not constrain the existing OTLP writer")
    if "max_streams = argument.max_streams.value" not in service_loki_writer:
        raise ValidationError("service log stream limits must be supplied by each registration")
    for loki_writer in (existing_loki_writer, service_loki_writer):
        if "queue_config" in loki_writer or re.search(r"\n\s*wal\s*{", loki_writer):
            raise ValidationError("experimental Loki queue/WAL must remain disabled")


def _document(rendered: str, kind: str) -> str:
    matches = [
        document
        for document in re.split(r"^---\s*$", rendered, flags=re.MULTILINE)
        if re.search(rf"^kind:\s+{re.escape(kind)}\s*$", document, flags=re.MULTILINE)
    ]
    if len(matches) != 1:
        raise ValidationError(
            f"rendered RBAC must contain exactly one {kind}, found {len(matches)}"
        )
    return matches[0]


def parse_role(rendered: str) -> tuple[str, list[tuple[set[str], set[str]]]]:
    if re.search(r"^kind:\s+ClusterRole(?:Binding)?\s*$", rendered, flags=re.MULTILINE):
        raise ValidationError("rendered Alloy RBAC must not contain cluster-wide roles")
    role = _document(rendered, "Role")
    namespace_match = re.search(r"^\s*namespace:\s+(\S+)\s*$", role, flags=re.MULTILINE)
    if not namespace_match:
        raise ValidationError("rendered Alloy Role has no namespace")

    rules: list[tuple[set[str], set[str]]] = []
    current: Optional[dict[str, set[str]]] = None
    field = ""
    for line in role.splitlines():
        if line.startswith("  - apiGroups:"):
            if current is not None:
                rules.append((current["resources"], current["verbs"]))
            current = {"resources": set(), "verbs": set()}
            field = "apiGroups"
        elif current is not None and line.strip() in {"resources:", "verbs:"}:
            field = line.strip()[:-1]
        elif current is not None and line.startswith("    - ") and field in current:
            current[field].add(line.strip()[2:].strip('"'))
    if current is not None:
        rules.append((current["resources"], current["verbs"]))
    return namespace_match.group(1), rules


def role_allows(
    namespace: str,
    rules: Sequence[tuple[set[str], set[str]]],
    requested_namespace: str,
    resource: str,
    verb: str,
) -> bool:
    return namespace == requested_namespace and any(
        resource in resources and verb in verbs for resources, verbs in rules
    )


def validate_rendered_rbac(rendered: str) -> None:
    namespace, rules = parse_role(rendered)
    binding = _document(rendered, "RoleBinding")
    if namespace != QA_SERVICE_NAMESPACE:
        raise ValidationError(
            f"Alloy Role namespace is {namespace}, expected {QA_SERVICE_NAMESPACE}"
        )
    if len(rules) != 2:
        raise ValidationError(f"Alloy Role must contain two rules, found {len(rules)}")

    expected = [({"pods"}, {"get", "list", "watch"}), ({"pods/log"}, {"get"})]
    if rules != expected:
        raise ValidationError(f"Alloy Role rules are {rules}, expected {expected}")
    if "kind: Role\n  name: qa-alloy" not in binding:
        raise ValidationError("Alloy RoleBinding does not reference the qa-alloy Role")
    if "name: qa-alloy\n    namespace: qa-monitoring" not in binding:
        raise ValidationError("Alloy RoleBinding does not bind the qa-monitoring ServiceAccount")

    denied = (
        (QA_SERVICE_NAMESPACE, "secrets", "get"),
        (QA_SERVICE_NAMESPACE, "pods/log", "list"),
        ("qa-monitoring", "pods/log", "get"),
        ("default", "pods", "list"),
    )
    unexpectedly_allowed = [case for case in denied if role_allows(namespace, rules, *case)]
    if unexpectedly_allowed:
        raise ValidationError(f"rendered Alloy RBAC permits denied cases: {unexpectedly_allowed}")


def enrich_legacy_identity(
    resource: Mapping[str, str],
    kubernetes: Mapping[str, str],
    service_name: str,
    container_name: str,
) -> dict[str, str]:
    enriched = dict(resource)
    is_legacy_service = (
        enriched.get("service.name") == service_name
        or kubernetes.get("k8s.container.name") == container_name
    )
    if not is_legacy_service:
        return enriched
    for key, value in (
        ("service.namespace", "insurance-hub"),
        ("service.version", kubernetes.get("service.version")),
        ("service.instance.id", kubernetes.get("k8s.pod.uid")),
        ("insurance_hub.service.implementation", "java"),
    ):
        if value is not None:
            enriched.setdefault(key, value)
    enriched["deployment.environment.name"] = "qa"
    enriched["k8s.cluster.name"] = "qa-insurance-hub"
    for key in ("k8s.namespace.name", "k8s.pod.uid", "k8s.container.name"):
        if key in kubernetes:
            enriched[key] = kubernetes[key]
    return enriched


def valid_go_identity(
    resource: Mapping[str, str], kubernetes_version: Optional[str]
) -> bool:
    version = resource.get("service.version")
    container_name = resource.get("k8s.container.name", "")
    expected_service_name = (
        container_name.removesuffix("-api") + "-service"
        if re.fullmatch(r"[a-z0-9-]+-api", container_name)
        else None
    )
    return all(
        (
            resource.get("service.namespace") == "insurance-hub",
            resource.get("service.name") == expected_service_name,
            bool(version),
            version not in {"latest", "unconfigured", "sha-unpublished"},
            version == kubernetes_version,
            resource.get("service.instance.id") == resource.get("k8s.pod.uid"),
            resource.get("insurance_hub.service.implementation") == "go",
            resource.get("k8s.namespace.name") == QA_SERVICE_NAMESPACE,
            expected_service_name is not None,
        )
    )


def sanitize_legacy_http_span(
    name: str,
    attributes: Mapping[str, object],
    service_domain: str,
    route_rules: Sequence[tuple[str, str, str, str]],
) -> tuple[str, dict[str, object]]:
    normalized = dict(attributes)
    method = normalized.get("http.method")
    if method is not None:
        normalized["http.request.method"] = "GET" if method == "GET" else "_OTHER"
    if "http.status_code" in normalized:
        normalized["http.response.status_code"] = normalized["http.status_code"]

    path = normalized.get("http.path")
    matched_route = False
    for path_pattern, name_pattern, route, span_name in route_rules:
        if (
            isinstance(path, str) and re.fullmatch(path_pattern, path)
        ) or re.fullmatch(name_pattern, name):
            normalized["http.route"] = route
            name = span_name
            matched_route = True
            break
    if not matched_route:
        normalized.pop("http.route", None)
        name = "HTTP _OTHER"

    service_attributes = {
        f"insurance_hub.{service_domain}.operation",
        f"insurance_hub.{service_domain}.transport",
        f"insurance_hub.{service_domain}.outcome",
        f"insurance_hub.{service_domain}.database.stage",
    }
    allowed = set(COMMON_SPAN_ATTRIBUTES) | service_attributes
    return name, {
        key: value
        for key, value in normalized.items()
        if key in allowed
    }


def sanitize_go_span(
    service_domain: str, attributes: Mapping[str, object]
) -> dict[str, object]:
    service_attributes = {
        f"insurance_hub.{service_domain}.operation",
        f"insurance_hub.{service_domain}.transport",
        f"insurance_hub.{service_domain}.outcome",
        f"insurance_hub.{service_domain}.database.stage",
    }
    allowed = set(COMMON_SPAN_ATTRIBUTES) | service_attributes
    return {key: value for key, value in attributes.items() if key in allowed}


def sanitize_resource(resource: Mapping[str, str]) -> dict[str, str]:
    return {key: value for key, value in resource.items() if key in RESOURCE_ATTRIBUTES}


def sanitize_datapoint(
    metric_name: str, attributes: Mapping[str, object], resource: Mapping[str, str]
) -> dict[str, object]:
    if not (
        resource.get("insurance_hub.service.implementation") == "go"
        and resource.get("k8s.namespace.name") == QA_SERVICE_NAMESPACE
    ):
        return dict(attributes)
    service_name = resource.get("service.name", "")
    if not re.fullmatch(r"[a-z0-9-]+-service", service_name):
        return {}
    service_domain = service_name.removesuffix("-service").replace("-", "_")
    application_prefix = f"insurance_hub.{service_domain}"
    if metric_name in {
        f"{application_prefix}.requests",
        f"{application_prefix}.request.duration",
    }:
        dimensions = (
            f"{application_prefix}.operation",
            f"{application_prefix}.transport",
            f"{application_prefix}.outcome",
            "http.response.status_code",
            "rpc.response.status_code",
        )
    elif metric_name == f"{application_prefix}.database.operations":
        dimensions = (
            f"{application_prefix}.operation",
            f"{application_prefix}.database.stage",
            f"{application_prefix}.database.outcome",
        )
    elif metric_name == "db.client.operation.duration":
        dimensions = (
            f"{application_prefix}.operation",
            *COMMON_METRIC_DIMENSIONS[metric_name],
        )
    elif metric_name in COMMON_METRIC_DIMENSIONS:
        dimensions = COMMON_METRIC_DIMENSIONS[metric_name]
    else:
        return {}
    allowed = set(dimensions)
    sanitized = {key: value for key, value in attributes.items() if key in allowed}
    for flat_key, resource_key in METRIC_RESOURCE_MAPPING:
        if resource_key in resource:
            sanitized[flat_key] = resource[resource_key]
    return sanitized


def select_service_targets(
    targets: Iterable[Mapping[str, str]],
    container_name: str,
    port_name: str,
    service_name: str,
) -> list[dict[str, str]]:
    selected: dict[tuple[str, str], dict[str, str]] = {}
    for target in targets:
        if (
            target.get("namespace") == QA_SERVICE_NAMESPACE
            and target.get("container") == container_name
            and target.get("port") == port_name
        ):
            key = (target.get("pod_uid", ""), target.get("container", ""))
            selected[key] = dict(target, service_name=service_name)
    return list(selected.values())


def log_labels(
    record: Mapping[str, str], target: Mapping[str, str]
) -> Optional[dict[str, str]]:
    version = target.get("service_version")
    if not (
        record.get("service_namespace") == "insurance-hub"
        and record.get("service_name") == target.get("service_name")
        and record.get("deployment_environment_name") == "qa"
        and record.get("service_implementation") == "go"
        and bool(version)
        and version not in {"latest", "unconfigured", "sha-unpublished"}
        and record.get("service_version") == version
        and record.get("service_instance_id") == target.get("pod_uid")
        and record.get("level") in {"INFO", "WARN", "ERROR"}
    ):
        return None
    combined = dict(record)
    combined["k8s_namespace_name"] = target.get("namespace", "")
    combined["k8s_container_name"] = target.get("container", "")
    return {key: combined[key] for key in LOKI_INDEX_LABELS if key in combined}


def position_key(target: Mapping[str, str]) -> tuple[Optional[str], Optional[str]]:
    return target.get("pod_uid"), target.get("container")


def run_check(args: argparse.Namespace) -> None:
    values = args.values.read_text(encoding="utf-8")
    config = args.config.read_text(encoding="utf-8")
    makefile = args.makefile.read_text(encoding="utf-8")
    validate_source_policy(config, values, makefile)
    rendered = (
        sys.stdin.read()
        if str(args.rendered_rbac) == "-"
        else args.rendered_rbac.read_text(encoding="utf-8")
    )
    validate_rendered_rbac(rendered)


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--values", type=Path, required=True)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--makefile", type=Path, required=True)
    parser.add_argument("--rendered-rbac", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    try:
        run_check(parse_args(sys.argv[1:] if argv is None else argv))
    except (OSError, ValidationError) as error:
        print(f"alloy check failed: {error}", file=sys.stderr)
        return 1
    print("Alloy source injection, policy, and scoped RBAC checks passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
