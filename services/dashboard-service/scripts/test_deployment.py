"""Dashboard deployment guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import base64
import json
import os
import signal
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import Mock, patch

import dashboard_deployment_checks as checks
import pytest
import yaml
from scripts.go.service import deployment as lifecycle

import deployment


def target_environment(name: str = "local-dev") -> dict[str, str]:
    if name == "local-dev":
        return {
            "DEPLOY_ENV": name,
            "DEPLOY_CONTEXT": "kind-local-dev-insurance-hub",
            "DEPLOY_SVC_NS": "local-dev-all",
            "DEPLOY_DATA_NS": "local-dev-all",
        }
    return {
        "DEPLOY_ENV": name,
        "DEPLOY_CONTEXT": "qa-insurance-hub",
        "DEPLOY_SVC_NS": "qa-svc",
        "DEPLOY_DATA_NS": "qa-data",
    }


def test_target_rejects_wrong_coordinates_before_cluster_access() -> None:
    for target, name, value in (
        ("local-dev", "DEPLOY_ENV", "unknown"),
        ("local-dev", "DEPLOY_CONTEXT", "qa-insurance-hub"),
        ("qa", "DEPLOY_DATA_NS", "qa-svc"),
    ):
        environment = target_environment(target)
        environment[name] = value
        with patch.dict(os.environ, environment, clear=True), patch.object(checks, "run") as run:
            with pytest.raises(checks.DeploymentError):
                checks.Target()
            run.assert_not_called()


def test_qa_target_separates_workload_and_data_coordinates() -> None:
    with (
        patch.dict(os.environ, target_environment("qa"), clear=True),
        patch.object(checks, "run") as run,
    ):
        target = checks.Target()

    assert target.namespace == "qa-svc"
    assert target.data_namespace == target.configuration["dataNamespace"] == "qa-data"
    run.assert_not_called()


def test_qa_mutation_is_rejected_before_coordinates() -> None:
    with patch.dict(os.environ, target_environment("qa"), clear=True):
        fake = Mock()
        fake.configuration = {"mode": "flux"}
        with patch.object(checks, "Target", return_value=fake):
            for operation in ("image-load", "deploy", "stop"):
                with pytest.raises(lifecycle.DeploymentError, match="forbidden"):
                    lifecycle.execute(checks, [operation])
        fake.coordinates.assert_not_called()


def test_unknown_operation_fails_before_target_selection() -> None:
    with patch.object(checks, "Target") as target:
        with pytest.raises(lifecycle.DeploymentError, match="unsupported operation"):
            lifecycle.execute(checks, ["initialize"])
        target.assert_not_called()


def test_dashboard_has_no_administration_or_initialization_owner() -> None:
    target = Mock()
    for owner in (
        checks.administration_snapshot,
        checks.initialization_check,
        checks.initialization_complete,
    ):
        with pytest.raises(checks.DeploymentError, match="no (administration|initialization)"):
            owner(target)


def test_secret_is_rejected_before_image_inspection() -> None:
    target = SimpleNamespace(configuration={"mode": "direct"})
    with (
        patch.dict(os.environ, {"DEPLOY_SECRET": "invented"}, clear=True),
        patch.object(checks, "run") as run,
        pytest.raises(checks.DeploymentError, match="no deployment Secret"),
    ):
        checks.secret_and_image(target, "insurance-hub-dashboard-api:1.0.0-local." + "a" * 12)
    run.assert_not_called()


def test_local_image_requires_exact_source_identity() -> None:
    image = "insurance-hub-dashboard-api:1.0.0-local." + "a" * 12
    target = SimpleNamespace(configuration={"mode": "direct"})
    inspected = json.dumps(
        [{"Config": {"Labels": {"org.opencontainers.image.revision": "b" * 40}}}]
    )
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "run", return_value=inspected),
        patch.object(checks, "workload_dependencies"),
    ):
        with pytest.raises(checks.DeploymentError, match="source revision"):
            checks.secret_and_image(target, image)


@pytest.mark.parametrize(
    "image",
    [
        "insurance-hub-dashboard-api:1.0.0-local.aaaaaaaaaaaa",
        "ghcr.io/igor-baiborodine/insurance-hub/dashboard-api:1.2.3",
    ],
)
def test_image_shape_is_bound_to_target_ownership(image: str) -> None:
    local = image.startswith("insurance-hub-")
    target = SimpleNamespace(configuration={"mode": "direct" if local else "flux"})
    inspected = json.dumps(
        [{"Config": {"Labels": {"org.opencontainers.image.revision": "a" * 40}}}]
    )
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "run", return_value=inspected),
        patch.object(checks, "workload_dependencies"),
    ):
        checks.secret_and_image(target, image)


def encoded_secret(name: str, namespace: str, values: dict[str, bytes]) -> dict[str, object]:
    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {"name": name, "namespace": namespace},
        "data": {key: base64.b64encode(value).decode() for key, value in values.items()},
    }


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_workload_dependencies_require_exact_reader_service_and_ca(environment: str) -> None:
    namespace = "local-dev-all" if environment == "local-dev" else "qa-svc"
    data_namespace = "local-dev-all" if environment == "local-dev" else "qa-data"
    reader_name = f"{environment}-dashboard-elasticsearch"
    reader = encoded_secret(
        reader_name,
        namespace,
        {
            "username": b"go_dashboard_total_sales_reader",
            "password": b"a" * 32,
        },
    )
    service_name = f"{environment}-elasticsearch-es-http"
    service = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": service_name, "namespace": data_namespace},
        "spec": {
            "type": "ClusterIP",
            "selector": {
                "elasticsearch.k8s.elastic.co/cluster-name": f"{environment}-elasticsearch"
            },
            "ports": [{"port": 9200, "protocol": "TCP"}],
        },
    }
    target = SimpleNamespace(
        environment=environment,
        namespace=namespace,
        data_namespace=data_namespace,
        json=Mock(),
        data_json=Mock(),
    )
    target.json.side_effect = lambda _get, kind, name, _ignore: {
        ("secret", reader_name): reader,
        (
            "secret",
            "qa-dashboard-elasticsearch-ca",
        ): encoded_secret("qa-dashboard-elasticsearch-ca", "qa-svc", {"ca.crt": b"ca"}),
    }[(kind, name)]
    target.data_json.side_effect = lambda _get, kind, name, _ignore: {
        ("service", service_name): service,
        (
            "secret",
            "qa-elasticsearch-es-http-certs-public",
        ): encoded_secret(
            "qa-elasticsearch-es-http-certs-public",
            "qa-data",
            {"ca.crt": b"ca", "tls.crt": b"public certificate"},
        ),
    }[(kind, name)]

    checks.workload_dependencies(target)

    reader["data"]["username"] = base64.b64encode(b"elastic").decode()
    with pytest.raises(checks.DeploymentError, match="reader identity"):
        checks.workload_dependencies(target)


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_qualification_pods_bind_allowed_denied_and_reader_cases(environment: str) -> None:
    namespace = "local-dev-all" if environment == "local-dev" else "qa-svc"
    target = SimpleNamespace(
        environment=environment,
        namespace=namespace,
        business=f"{environment}-dashboard-api",
        management=f"{environment}-dashboard-api-management",
    )

    pods = checks.qualification_pods(target, f"issue-287-dashboard-denied-{environment}")

    assert len(pods) == 15
    assert all(pod["spec"]["containers"][0]["image"] == checks.PROBE_IMAGE for pod in pods)
    assert all(
        pod["spec"]["containers"][0]["args"][0].startswith(
            f"sleep {checks.NETWORK_POLICY_CONVERGENCE_SECONDS}\n"
        )
        for pod in pods
    )
    by_name = {pod["metadata"]["name"]: pod for pod in pods}
    assert by_name["dashboard-qualification-business"]["metadata"]["labels"] == {
        "app.kubernetes.io/name": "dashboard-qualification",
        "app.kubernetes.io/component": "probe",
        "insurance-hub.io/owner": "issue-287",
        checks.BUSINESS_CLIENT_LABEL: "true",
        "insurance-hub.io/qualification": "issue-287",
    }
    assert by_name["dashboard-qualification-marker-only"]["metadata"]["labels"] == {
        checks.BUSINESS_CLIENT_LABEL: "true",
        "insurance-hub.io/qualification": "issue-287",
    }
    assert (
        by_name["dashboard-qualification-cross-namespace-denied"]["metadata"]["namespace"]
        == f"issue-287-dashboard-denied-{environment}"
    )
    assert (
        by_name["dashboard-qualification-cross-namespace-grpc-denied"]["metadata"]["namespace"]
        == f"issue-287-dashboard-denied-{environment}"
    )
    assert (
        by_name["dashboard-qualification-cross-namespace-agent-denied"]["metadata"]["namespace"]
        == f"issue-287-dashboard-denied-{environment}"
    )
    total_allowed = by_name["dashboard-qualification-business"]["spec"]["containers"][0]["args"][0]
    agent_allowed = by_name["dashboard-qualification-business-agent-sales"]["spec"]["containers"][
        0
    ]["args"][0]
    trends_allowed = by_name["dashboard-qualification-business-sales-trends"]["spec"]["containers"][
        0
    ]["args"][0]
    assert "/dashboard/totalsales" in total_allowed
    assert "grep -q '\"total\"'" in total_allowed
    assert "/dashboard/agentssales" in agent_allowed
    assert "grep -q '\"perAgentTotal\"'" in agent_allowed
    assert "/dashboard/trends" in trends_allowed
    assert "grep -Eq '^\\{\\}$|\"periodSales\":\\['" in trends_allowed
    management_allowed = by_name["dashboard-qualification-management"]["spec"]["containers"][0][
        "args"
    ][0]
    assert '= "live"' in management_allowed
    assert '= "alive"' not in management_allowed
    assert '= "ready"' in management_allowed
    grpc_reachable = by_name[checks.GRPC_REACHABLE_QUALIFICATION_POD]["spec"]["containers"][0][
        "args"
    ][0]
    assert '"num_connects"' in grpc_reachable
    assert ":9090" in grpc_reachable
    for name in checks.DENIED_QUALIFICATION_PODS:
        denial_script = by_name[name]["spec"]["containers"][0]["args"][0]
        assert "--write-out '%{http_code}'" in denial_script
        assert '"exit_code"' in denial_script
        assert "--fail" not in denial_script
    reader = by_name["dashboard-qualification-reader"]["spec"]["containers"][0]
    encoded = json.dumps(reader)
    script = reader["args"][0]
    assert f"{environment}-dashboard-elasticsearch" in encoded
    assert "go_dashboard_total_sales_reader" in script
    assert "_security/user/_privileges" in script
    assert "cat /tmp/privileges" in script
    assert "failed=0" in script
    assert "-u " not in script
    assert "ES_PASSWORD" in script
    if environment == "qa":
        assert "--cacert /etc/dashboard/elasticsearch/ca.crt" in script
        assert any(mount["name"] == "elasticsearch-ca" for mount in reader["volumeMounts"])
    else:
        assert "--cacert" not in script


def accepted_effective_reader_privileges() -> dict[str, object]:
    return {
        "cluster": [],
        "global": [],
        "indices": [
            {
                "names": ["policy_stats"],
                "privileges": ["read", "view_index_metadata"],
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "remote_indices": [],
        "remote_cluster": [],
    }


def test_effective_reader_privileges_accept_the_exact_q06_role() -> None:
    checks.validate_effective_reader_privileges(json.dumps(accepted_effective_reader_privileges()))


@pytest.mark.parametrize(
    "mutation",
    [
        lambda value: value["indices"].append(
            {"names": ["other-sensitive-index"], "privileges": ["delete_index"]}
        ),
        lambda value: value["indices"][0]["privileges"].append("create_index"),
        lambda value: value["applications"].append(
            {"application": "dashboard", "privileges": ["admin"], "resources": ["*"]}
        ),
        lambda value: value["run_as"].append("elastic"),
        lambda value: value["remote_indices"].append(
            {
                "clusters": ["remote"],
                "names": ["policy_stats"],
                "privileges": ["read"],
            }
        ),
        lambda value: value["remote_cluster"].append(
            {"clusters": ["remote"], "privileges": ["monitor_enrich"]}
        ),
    ],
)
def test_effective_reader_privileges_reject_additional_authority(mutation) -> None:
    privileges = accepted_effective_reader_privileges()
    mutation(privileges)

    with pytest.raises(checks.DeploymentError, match="effective privileges differ"):
        checks.validate_effective_reader_privileges(json.dumps(privileges))


@pytest.mark.parametrize("exit_code", [7, 28])
def test_cni_denial_accepts_the_observed_connection_rejections(exit_code: int) -> None:
    checks.validate_cni_denial(
        json.dumps({"exit_code": exit_code, "http_status": "000", "response_bytes": 0})
    )


@pytest.mark.parametrize(
    ("exit_code", "http_status", "response_bytes"),
    [
        (0, "400", 32),
        (0, "401", 32),
        (0, "404", 32),
        (0, "405", 32),
        (0, "500", 32),
        (6, "000", 0),
        (7, "200", 0),
        (28, "000", 1),
    ],
)
def test_cni_denial_rejects_reachable_http_and_other_failures(
    exit_code: int, http_status: str, response_bytes: int
) -> None:
    output = json.dumps(
        {
            "exit_code": exit_code,
            "http_status": http_status,
            "response_bytes": response_bytes,
        }
    )

    with pytest.raises(checks.DeploymentError, match="CNI denial result differs"):
        checks.validate_cni_denial(output)


@pytest.mark.parametrize("name", sorted(checks.DENIED_QUALIFICATION_PODS))
def test_denied_qualification_pods_reject_a_reachable_http_response(name: str) -> None:
    output = json.dumps({"exit_code": 0, "http_status": "405", "response_bytes": 32})

    with pytest.raises(checks.DeploymentError, match="CNI denial result differs"):
        checks.validate_qualification_output(name, output)


def test_grpc_reachability_accepts_the_expected_listener_connection() -> None:
    checks.validate_grpc_reachability(
        json.dumps(
            {
                "exit_code": 1,
                "remote_ip": "10.96.42.17",
                "remote_port": 9090,
                "num_connects": 1,
            }
        )
    )


@pytest.mark.parametrize(
    "result",
    [
        {"exit_code": 28, "remote_ip": "", "remote_port": 0, "num_connects": 0},
        {"exit_code": 6, "remote_ip": "", "remote_port": 0, "num_connects": 0},
        {"exit_code": 7, "remote_ip": "10.96.42.17", "remote_port": 0, "num_connects": 0},
        {
            "exit_code": 0,
            "remote_ip": "10.96.42.17",
            "remote_port": 9090,
            "num_connects": 1,
            "unexpected": True,
        },
    ],
)
def test_grpc_reachability_rejects_other_transport_outcomes(result: dict[str, object]) -> None:
    with pytest.raises(checks.DeploymentError, match="gRPC reachability result differs"):
        checks.validate_grpc_reachability(json.dumps(result))


def direct_rpc_output(action: str = "pass", test_name: str = "TestDeployedDirectRPC") -> str:
    return "\n".join(
        [
            json.dumps({"Action": "run", "Test": test_name}),
            json.dumps({"Action": action, "Test": test_name}),
        ]
    )


def test_direct_rpc_output_requires_the_selected_scenario_to_pass_once() -> None:
    checks.validate_direct_rpc_output(direct_rpc_output())


@pytest.mark.parametrize(
    "output",
    [
        "",
        direct_rpc_output(test_name="TestRenamedDirectRPC"),
        direct_rpc_output(action="fail"),
        direct_rpc_output()
        + "\n"
        + json.dumps({"Action": "pass", "Test": "TestDeployedDirectRPC"}),
    ],
)
def test_direct_rpc_output_rejects_missing_renamed_failed_or_duplicate_results(output: str) -> None:
    with pytest.raises(checks.DeploymentError, match="did not execute and pass exactly once"):
        checks.validate_direct_rpc_output(output)


def test_direct_rpc_invokes_the_service_owner_with_the_selected_address() -> None:
    with patch.object(
        checks,
        "run",
        return_value=direct_rpc_output(test_name="TestDeployedDirectRPCSharedSmoke"),
    ) as command:
        checks.run_deployed_direct_rpc(19090)

    command.assert_called_once_with(
        [
            "make",
            "--no-print-directory",
            "-C",
            "services/dashboard-service",
            "_deploy-test-rpc",
        ],
        timeout=60,
        environment={
            "GO": "go",
            "DASHBOARD_DEPLOYMENT_GRPC_ADDR": "127.0.0.1:19090",
            "DASHBOARD_DEPLOYMENT_RPC_SCENARIO": "shared-smoke",
        },
        activity="verify deployed direct Dashboard RPC",
    )


def test_verify_runs_rpc_and_access_after_all_http_smokes_fail() -> None:
    target = Mock(
        kubectl_binary="kubectl",
        context="kind-local-dev-insurance-hub",
        namespace="local-dev-all",
        business="local-dev-dashboard-api",
        management="local-dev-dashboard-api-management",
    )
    processes = [Mock(), Mock(), Mock()]
    for process in processes:
        process.wait.return_value = 0
    with (
        patch.object(checks, "workload_dependencies"),
        patch.object(checks, "role_snapshot", return_value={"java": "unchanged"}),
        patch.object(checks, "_free_port", side_effect=[18080, 18081, 19090]),
        patch.object(checks.subprocess, "Popen", side_effect=processes),
        patch.object(checks, "_wait_for_port_forward"),
        patch.object(
            checks,
            "_http",
            side_effect=[
                (200, b"ready\n"),
                (500, b'{"error":"total"}'),
                (403, b'{"error":"agent"}'),
                (504, b'{"error":"trends"}'),
            ],
        ),
        patch.object(checks, "run_deployed_direct_rpc") as rpc,
        patch.object(checks, "run_access_qualification") as access,
        pytest.raises(
            checks.DeploymentError,
            match="total-sales HTTP.*agent-sales HTTP.*sales-trends HTTP",
        ),
    ):
        checks.verify(target, [])

    rpc.assert_called_once_with(19090)
    access.assert_called_once_with(target)


@pytest.mark.parametrize("trend_body", [b"{}", b'{"periodSales":[]}'])
def test_verify_accepts_empty_and_populated_trend_envelopes(trend_body: bytes) -> None:
    target = Mock(
        kubectl_binary="kubectl",
        context="kind-local-dev-insurance-hub",
        namespace="local-dev-all",
        business="local-dev-dashboard-api",
        management="local-dev-dashboard-api-management",
    )
    processes = [Mock(), Mock(), Mock()]
    for process in processes:
        process.wait.return_value = 0
    with (
        patch.object(checks, "workload_dependencies"),
        patch.object(checks, "role_snapshot", return_value={"java": "unchanged"}),
        patch.object(checks, "_free_port", side_effect=[18080, 18081, 19090]),
        patch.object(checks.subprocess, "Popen", side_effect=processes),
        patch.object(checks, "_wait_for_port_forward"),
        patch.object(
            checks,
            "_http",
            side_effect=[
                (200, b"ready\n"),
                (200, b'{"total":{}}'),
                (200, b'{"perAgentTotal":{}}'),
                (200, trend_body),
            ],
        ),
        patch.object(checks, "run_deployed_direct_rpc") as rpc,
        patch.object(checks, "run_access_qualification") as access,
    ):
        checks.verify(target, [])

    rpc.assert_called_once_with(19090)
    access.assert_called_once_with(target)


@pytest.mark.parametrize("failure", [checks.DeploymentError("RPC failed"), KeyboardInterrupt()])
def test_verify_cleans_every_port_forward_when_direct_rpc_stops(failure: BaseException) -> None:
    target = Mock(
        kubectl_binary="kubectl",
        context="kind-local-dev-insurance-hub",
        namespace="local-dev-all",
        business="local-dev-dashboard-api",
        management="local-dev-dashboard-api-management",
    )
    processes = [Mock(), Mock(), Mock()]
    for process in processes:
        process.wait.return_value = 0
    with (
        patch.object(checks, "workload_dependencies"),
        patch.object(checks, "role_snapshot", return_value={"java": "unchanged"}),
        patch.object(checks, "_free_port", side_effect=[18080, 18081, 19090]),
        patch.object(checks.subprocess, "Popen", side_effect=processes) as popen,
        patch.object(checks, "_wait_for_port_forward"),
        patch.object(
            checks,
            "_http",
            side_effect=[
                (200, b"ready\n"),
                (200, b'{"total":{"policiesCount":1}}'),
                (200, b'{"perAgentTotal":{}}'),
                (200, b'{"periodSales":[]}'),
            ],
        ),
        patch.object(checks, "run_deployed_direct_rpc", side_effect=failure),
        patch.object(checks, "run_access_qualification"),
        pytest.raises(type(failure)),
    ):
        checks.verify(target, [])

    assert popen.call_count == 3
    assert popen.call_args_list[2].args[0][-2:] == [
        "service/local-dev-dashboard-api",
        "19090:9090",
    ]
    for process in processes:
        process.terminate.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=5)


def test_access_qualification_cleans_owned_resources_after_probe_failure() -> None:
    target = Mock(
        environment="local-dev",
        context="kind-local-dev-insurance-hub",
        kubectl_binary="kubectl",
    )
    target.namespace_kubectl.side_effect = ["", "created", "sanitized failure", "deleted"]
    target.namespace_json.return_value = {
        "metadata": {"labels": {"insurance-hub.io/qualification": "issue-287"}}
    }
    pod = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": "dashboard-qualification-business",
            "namespace": "local-dev-all",
            "labels": {"insurance-hub.io/qualification": "issue-287"},
        },
        "spec": {},
    }
    namespace = json.dumps(
        {"metadata": {"labels": {"insurance-hub.io/qualification": "issue-287"}}}
    )

    with (
        patch.object(checks, "qualification_pods", return_value=[pod]),
        patch.object(
            checks,
            "wait_for_probe",
            side_effect=checks.DeploymentError("synthetic probe failure"),
        ),
        patch.object(checks, "run", side_effect=["", namespace, "deleted"]) as command,
        pytest.raises(checks.DeploymentError, match="sanitized probe output"),
    ):
        checks.run_access_qualification(target)

    target.namespace_kubectl.assert_any_call(
        "local-dev-all",
        "delete",
        "pod",
        "dashboard-qualification-business",
        "--wait=true",
        "--timeout=30s",
        timeout=40,
    )
    assert command.call_args_list[-1].args[0][-5:] == [
        "delete",
        "namespace",
        "issue-287-dashboard-denied-local-dev",
        "--wait=true",
        "--timeout=60s",
    ]


def test_access_qualification_runs_remaining_probes_after_one_fails() -> None:
    target = Mock(
        environment="local-dev",
        context="kind-local-dev-insurance-hub",
        kubectl_binary="kubectl",
    )
    target.namespace_kubectl.side_effect = lambda _namespace, operation, *_args, **_kwargs: (
        "failed probe output" if operation == "logs" else ""
    )
    target.namespace_json.return_value = {
        "metadata": {"labels": {"insurance-hub.io/qualification": "issue-287"}}
    }
    pods = [
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "name": name,
                "namespace": "local-dev-all",
                "labels": {"insurance-hub.io/qualification": "issue-287"},
            },
            "spec": {},
        }
        for name in ("probe-one", "probe-two")
    ]
    namespace = json.dumps(
        {"metadata": {"labels": {"insurance-hub.io/qualification": "issue-287"}}}
    )
    with (
        patch.object(checks, "qualification_pods", return_value=pods),
        patch.object(
            checks,
            "wait_for_probe",
            side_effect=[checks.DeploymentError("first failed"), None],
        ) as wait,
        patch.object(checks, "run", side_effect=["", namespace, ""]),
        pytest.raises(checks.DeploymentError, match="probe-one.*first failed"),
    ):
        checks.run_access_qualification(target)

    assert [call.args[2] for call in wait.call_args_list] == ["probe-one", "probe-two"]


def test_access_qualification_does_not_read_or_delete_preexisting_probe() -> None:
    target = Mock(
        environment="local-dev",
        context="kind-local-dev-insurance-hub",
        kubectl_binary="kubectl",
    )
    target.namespace_kubectl.return_value = "pod/dashboard-qualification-business"
    pod = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": "dashboard-qualification-business",
            "namespace": "local-dev-all",
            "labels": {"insurance-hub.io/qualification": "issue-287"},
        },
        "spec": {},
    }
    namespace = json.dumps(
        {"metadata": {"labels": {"insurance-hub.io/qualification": "issue-287"}}}
    )

    with (
        patch.object(checks, "qualification_pods", return_value=[pod]),
        patch.object(checks, "run", side_effect=["", namespace, ""]),
        pytest.raises(checks.DeploymentError, match="already exists"),
    ):
        checks.run_access_qualification(target)

    assert target.namespace_kubectl.call_count == 1
    target.namespace_kubectl.assert_called_once_with(
        "local-dev-all",
        "get",
        "pod",
        "dashboard-qualification-business",
        "--ignore-not-found",
        "-o",
        "name",
    )
    target.namespace_json.assert_not_called()


def test_render_rejects_foreign_dependencies_and_incomplete_inventory() -> None:
    with pytest.raises(checks.DeploymentError, match="data dependency"):
        checks.validate_render("local-dev", "kind: Secret\nmetadata:\n  name: kafka\n")
    with pytest.raises(checks.DeploymentError, match="required resource kind"):
        checks.validate_render(
            "local-dev",
            "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: local-dev-dashboard-config\n",
        )


def test_render_requires_the_runtime_config_map_linkage() -> None:
    items = checks.documents(checks.render("local-dev"))
    deployment_item = checks.resource(items, "Deployment", "local-dev-dashboard-api")
    deployment_item["spec"]["template"]["spec"]["containers"][0].pop("envFrom")
    with pytest.raises(checks.DeploymentError, match="container security or resources"):
        checks.validate_render("local-dev", yaml.safe_dump_all(items))


@pytest.mark.parametrize(
    ("environment", "key"),
    [
        ("local-dev", "PASSWORD"),
        ("qa", "TOKEN"),
        ("local-dev", "ADMINISTRATION_MODE"),
        ("qa", "DEPENDENCY_ENDPOINT"),
    ],
)
def test_render_rejects_undeclared_runtime_configuration(environment: str, key: str) -> None:
    items = checks.documents(checks.render(environment))
    config = checks.resource(items, "ConfigMap", f"{environment}-dashboard-config")
    config["data"][key] = "private-or-undeclared-value"

    with pytest.raises(checks.DeploymentError, match="runtime configuration differs"):
        checks.validate_render(environment, yaml.safe_dump_all(items))


def test_render_requires_the_application_exporter_timeout_default() -> None:
    items = checks.documents(checks.render("local-dev"))
    config = checks.resource(items, "ConfigMap", "local-dev-dashboard-config")
    config["data"]["OTEL_EXPORTER_OTLP_TIMEOUT"] = "2000"

    with pytest.raises(checks.DeploymentError, match="application default"):
        checks.validate_render("local-dev", yaml.safe_dump_all(items))


@pytest.mark.parametrize("value", [None, "", "Local", "UTC", " GMT"])
def test_render_requires_the_accepted_sales_trends_time_zone(value: str | None) -> None:
    items = checks.documents(checks.render("local-dev"))
    config = checks.resource(items, "ConfigMap", "local-dev-dashboard-config")
    if value is None:
        config["data"].pop("SALES_TRENDS_TIME_ZONE")
    else:
        config["data"]["SALES_TRENDS_TIME_ZONE"] = value

    with pytest.raises(checks.DeploymentError, match="runtime configuration differs"):
        checks.validate_render("local-dev", yaml.safe_dump_all(items))


@pytest.mark.parametrize(
    ("environment", "mutation", "message"),
    [
        (
            "local-dev",
            lambda items: checks.resource(items, "NetworkPolicy", "local-dev-dashboard-api")[
                "spec"
            ]["ingress"][0]["from"][0]["podSelector"]["matchLabels"].pop(
                "app.kubernetes.io/instance"
            ),
            "admission",
        ),
        (
            "qa",
            lambda items: checks.resource(items, "NetworkPolicy", "qa-dashboard-api")["spec"][
                "egress"
            ][0]["to"][0].pop("podSelector"),
            "egress",
        ),
        (
            "qa",
            lambda items: checks.resource(items, "Deployment", "qa-dashboard-api")["spec"][
                "template"
            ]["spec"]["volumes"][0]["secret"].__setitem__(
                "secretName", "qa-elasticsearch-es-http-certs-public"
            ),
            "data boundary",
        ),
    ],
)
def test_render_rejects_weakened_target_security(environment: str, mutation, message: str) -> None:
    items = checks.documents(checks.render(environment))
    mutation(items)

    with pytest.raises(checks.DeploymentError, match=message):
        checks.validate_render(environment, yaml.safe_dump_all(items))


def test_java_edge_identity_requires_the_dedicated_dashboard_marker() -> None:
    rendered = checks.run(
        [
            os.environ.get("KUBECTL", "kubectl"),
            "kustomize",
            str(checks.ROOT / "k8s/overlays/local-dev/svc/agent-portal-gateway/legacy"),
        ]
    )
    items = checks.documents(rendered)
    deployment_item = checks.resource(items, "Deployment", "local-dev-agent-portal-gateway-legacy")
    deployment_item["spec"]["template"]["metadata"]["labels"].pop(checks.BUSINESS_CLIENT_LABEL)

    with (
        patch.object(checks, "run", return_value=yaml.safe_dump_all(items)),
        pytest.raises(checks.DeploymentError, match="caller identity"),
    ):
        checks.validate_java_edge_identity("local-dev")


def test_live_state_rejects_wrong_resource_identity() -> None:
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    actual[("configmap", "local-dev-dashboard-config")]["metadata"]["name"] = "foreign"

    with pytest.raises(checks.DeploymentError, match="identity differs"):
        target.live(items, require_deployment=True)


def rendered_resources(environment: str = "local-dev") -> list[dict[str, object]]:
    return checks.validate_render(environment, checks.render(environment))


def target_with_live_resources(
    items: list[dict[str, object]],
) -> tuple[checks.Target, dict[tuple[str, str], dict[str, object]]]:
    actual = {
        (str(item["kind"]).lower(), str(item["metadata"]["name"])): deepcopy(item) for item in items
    }
    target = object.__new__(checks.Target)
    target.environment = "local-dev"
    target.namespace = "local-dev-all"
    target.deployment = "local-dev-dashboard-api"
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _get, kind, name, _ignore: actual[(kind, name)])
    return target, actual


def test_live_state_accepts_the_complete_rendered_contract() -> None:
    items = rendered_resources()
    target, _actual = target_with_live_resources(items)
    target.live(items, require_deployment=True)


def test_live_state_accepts_api_server_defaulted_probe_fields() -> None:
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    deployment = actual[("deployment", "local-dev-dashboard-api")]
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    for name in ("startupProbe", "livenessProbe", "readinessProbe"):
        container[name]["httpGet"]["scheme"] = "HTTP"
        container[name]["successThreshold"] = 1

    target.live(items, require_deployment=True)


def test_reader_predecessor_config_accepts_only_the_missing_trend_zone() -> None:
    # given
    expected = checks.resource(rendered_resources(), "ConfigMap", "local-dev-dashboard-config")[
        "data"
    ]
    actual = deepcopy(expected)
    actual.pop("SALES_TRENDS_TIME_ZONE")

    # when
    accepted = checks.matching_reader_predecessor_config(actual, expected)

    # then
    assert accepted
    actual["FOREIGN_SETTING"] = "true"
    assert not checks.matching_reader_predecessor_config(actual, expected)


def test_live_state_accepts_declared_predecessor_only_before_rollout() -> None:
    # given a reviewed replacement and the exact previously accepted local release.
    items = rendered_resources()
    deployment = checks.resource(items, "Deployment", "local-dev-dashboard-api")
    config = checks.resource(items, "ConfigMap", "local-dev-dashboard-config")
    desired_image = "insurance-hub-dashboard-api:1.0.0-local.123456789abc"
    previous_image = "insurance-hub-dashboard-api:1.0.0-local.abcdef123456"
    deployment["metadata"]["labels"]["app.kubernetes.io/version"] = "git-123456789abc"
    deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = (
        "git-123456789abc"
    )
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = desired_image
    config["data"]["SERVICE_VERSION"] = "git-123456789abc"
    target, actual = target_with_live_resources(items)
    live_deployment = actual[("deployment", "local-dev-dashboard-api")]
    live_deployment["metadata"]["labels"]["app.kubernetes.io/version"] = "git-abcdef123456"
    live_deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = (
        "git-abcdef123456"
    )
    live_deployment["spec"]["template"]["spec"]["containers"][0]["image"] = previous_image
    actual[("configmap", "local-dev-dashboard-config")]["data"]["SERVICE_VERSION"] = (
        "git-abcdef123456"
    )
    live_config = actual[("configmap", "local-dev-dashboard-config")]["data"]
    for name in (
        "ELASTICSEARCH_INDEX",
        "ELASTICSEARCH_TIMEOUT",
        "ELASTICSEARCH_URL",
        "SALES_TRENDS_TIME_ZONE",
    ):
        live_config.pop(name)
    live_config["OTEL_EXPORTER_OTLP_TIMEOUT"] = "2s"
    live_container = live_deployment["spec"]["template"]["spec"]["containers"][0]
    live_container["name"] = "dashboard-service"
    live_container["imagePullPolicy"] = "IfNotPresent"
    live_container["env"] = [
        item for item in live_container["env"] if item["name"] == "SERVICE_INSTANCE_ID"
    ]
    live_policy = actual[("networkpolicy", "local-dev-dashboard-api")]
    live_policy["spec"].pop("egress")
    live_policy["spec"]["ingress"] = [
        {
            "from": [{"podSelector": {"matchLabels": {checks.BUSINESS_CLIENT_LABEL: "true"}}}],
            "ports": [
                {"port": 8080, "protocol": "TCP"},
                {"port": 9090, "protocol": "TCP"},
            ],
        },
        {
            "from": [{"podSelector": {"matchLabels": {checks.MANAGEMENT_CLIENT_LABEL: "true"}}}],
            "ports": [{"port": 8081, "protocol": "TCP"}],
        },
    ]

    with patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True):
        # when / then the declared predecessor is accepted only before apply.
        target.live(items, require_deployment=False)
        with pytest.raises(checks.DeploymentError, match="differs from rendered configuration"):
            target.live(items, require_deployment=True)


@pytest.mark.parametrize(
    "previous_image",
    [
        "insurance-hub-dashboard-api:latest",
        "insurance-hub-policy-search-api:1.0.0-local.abcdef123456",
    ],
)
def test_live_state_rejects_invalid_predecessor(previous_image: str) -> None:
    # given
    items = rendered_resources()
    target, _actual = target_with_live_resources(items)

    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        # when / then
        pytest.raises(checks.DeploymentError, match="valid only for a direct local rollout"),
    ):
        target.live(items, require_deployment=False)


@pytest.mark.parametrize(
    ("kind", "name", "mutation", "message"),
    [
        (
            "ConfigMap",
            "local-dev-dashboard-config",
            lambda item: item["data"].__setitem__("FOREIGN_SETTING", "true"),
            "rendered configuration",
        ),
        (
            "Service",
            "local-dev-dashboard-api",
            lambda item: item["spec"].__setitem__("externalIPs", ["203.0.113.10"]),
            "external exposure",
        ),
        (
            "Deployment",
            "local-dev-dashboard-api",
            lambda item: item["metadata"].__setitem__(
                "ownerReferences", [{"kind": "Foreign", "name": "owner"}]
            ),
            "rendered identity",
        ),
        (
            "Deployment",
            "local-dev-dashboard-api",
            lambda item: item["spec"]["template"]["spec"].__setitem__("hostNetwork", True),
            "Pod security settings",
        ),
        (
            "Deployment",
            "local-dev-dashboard-api",
            lambda item: item["spec"]["template"]["spec"]["containers"][0].__setitem__(
                "name", "dashboard-service"
            ),
            "container name differs",
        ),
        (
            "Deployment",
            "local-dev-dashboard-api",
            lambda item: item["spec"]["template"]["spec"]["containers"][0]["resources"][
                "limits"
            ].__setitem__("cpu", "500m"),
            "container resources differs",
        ),
        (
            "NetworkPolicy",
            "local-dev-dashboard-api",
            lambda item: item["spec"].__setitem__("ingress", []),
            "rendered policy",
        ),
    ],
)
def test_live_state_rejects_foreign_or_drifted_resources(
    kind: str, name: str, mutation, message: str
) -> None:
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    mutation(actual[(kind.lower(), name)])

    with pytest.raises(checks.DeploymentError, match=message):
        target.live(items, require_deployment=True)


@pytest.mark.parametrize(
    ("expected", "image_id", "pod_count", "message"),
    [
        (
            "sha256:" + "a" * 64,
            "ghcr.io/igor-baiborodine/insurance-hub/dashboard-api@sha256:" + "a" * 64,
            1,
            "",
        ),
        (
            "sha256:" + "a" * 64,
            "ghcr.io/igor-baiborodine/insurance-hub/dashboard-api@sha256:" + "b" * 64,
            1,
            "image digest differs",
        ),
        ("", "sha256:" + "a" * 64, 1, "DEPLOY_IMAGE_DIGEST is required"),
        ("sha256:short", "sha256:" + "a" * 64, 1, "DEPLOY_IMAGE_DIGEST is required"),
        ("sha256:" + "a" * 64, "sha256:" + "a" * 64, 0, "Pod inventory differs"),
    ],
)
def test_qa_live_state_requires_running_pod_digest(
    expected: str, image_id: str, pod_count: int, message: str
) -> None:
    items = rendered_resources("qa")
    deployment_item = checks.resource(items, "Deployment", "qa-dashboard-api")
    actual = {
        (str(item["kind"]).lower(), str(item["metadata"]["name"])): deepcopy(item) for item in items
    }
    pod = {
        "metadata": {"name": "qa-dashboard-api-abc"},
        "status": {
            "phase": "Running",
            "conditions": [{"type": "Ready", "status": "True"}],
            "containerStatuses": [{"name": "dashboard-api", "imageID": image_id}],
        },
    }

    def live_json(_operation, kind, *arguments):
        if kind == "pods":
            return {"items": [pod] * pod_count}
        return actual[(kind, arguments[0])]

    target = object.__new__(checks.Target)
    target.environment = "qa"
    target.namespace = "qa-svc"
    target.deployment = deployment_item["metadata"]["name"]
    target.configuration = {"mode": "flux"}
    target.json = Mock(side_effect=live_json)
    environment = {"DEPLOY_IMAGE_DIGEST": expected} if expected else {}

    with patch.dict(os.environ, environment, clear=True):
        if message:
            with pytest.raises(checks.DeploymentError, match=message):
                target.live(items, require_deployment=True)
        else:
            target.live(items, require_deployment=True)
            target.json.assert_any_call(
                "get",
                "pods",
                "-l",
                "app.kubernetes.io/component=api,app.kubernetes.io/name=dashboard-api",
            )


def test_live_rejection_prevents_server_side_apply() -> None:
    target = Mock()
    target.configuration = {"mode": "direct"}
    target.rendered.return_value = ("rendered", [], "image")
    target.live.side_effect = checks.DeploymentError("live ConfigMap drift")
    with (
        patch.object(checks, "Target", return_value=target),
        patch.object(checks, "check_render"),
        patch.object(checks, "secret_and_image"),
        patch.object(lifecycle, "apply") as apply,
        pytest.raises(checks.DeploymentError, match="ConfigMap drift"),
    ):
        lifecycle.execute(checks, ["deploy"])
    apply.assert_not_called()


def java_resources(
    deployment_uid: str = "deployment-uid",
    service_uid: str = "service-uid",
    available: int = 1,
) -> dict[str, dict[str, object]]:
    labels = {
        "app.kubernetes.io/name": "dashboard-api-legacy",
        "app.kubernetes.io/instance": "insurance-hub-dashboard-api-legacy",
        "app.kubernetes.io/component": "api",
    }
    return {
        "deployment": {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {
                "name": "local-dev-dashboard-api-legacy",
                "namespace": "local-dev-all",
                "uid": deployment_uid,
                "labels": labels,
            },
            "spec": {"replicas": 1},
            "status": {"availableReplicas": available, "readyReplicas": available},
        },
        "service": {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {
                "name": "local-dev-dashboard-api-legacy",
                "namespace": "local-dev-all",
                "uid": service_uid,
                "labels": labels,
            },
            "spec": {
                "type": "ClusterIP",
                "selector": {
                    "app.kubernetes.io/component": "api",
                    "app.kubernetes.io/managed-by": "kustomize",
                    "app.kubernetes.io/name": "dashboard-api-legacy",
                    "app.kubernetes.io/part-of": "insurance-hub",
                    "environment": "local-dev",
                },
            },
        },
    }


def java_target(resources: dict[str, dict[str, object]]) -> Mock:
    target = Mock()
    target.environment = "local-dev"
    target.namespace = "local-dev-all"
    target.configuration = {"mode": "direct"}
    target.rendered.return_value = ("rendered", [], "image")
    target.json.side_effect = lambda _get, kind, _name, _ignore: resources.get(kind, {})
    return target


def test_java_snapshot_requires_declared_stable_available_resources() -> None:
    target = java_target(java_resources())
    with patch.dict(
        os.environ,
        {
            "DEPLOY_JAVA_DEPLOYMENT_UID": "deployment-uid",
            "DEPLOY_JAVA_SERVICE_UID": "service-uid",
        },
        clear=True,
    ):
        assert checks.role_snapshot(target) == {
            "deployment/local-dev-dashboard-api-legacy": "deployment-uid",
            "service/local-dev-dashboard-api-legacy": "service-uid",
        }


@pytest.mark.parametrize("defect", ["absent", "replaced", "selector", "unavailable"])
def test_invalid_java_owner_prevents_go_apply(defect: str) -> None:
    resources = java_resources()
    expected_deployment_uid = "deployment-uid"
    if defect == "absent":
        resources["deployment"] = {}
    elif defect == "replaced":
        resources["deployment"]["metadata"]["uid"] = "replacement-uid"
    elif defect == "selector":
        resources["service"]["spec"]["selector"].pop("environment")
    else:
        resources["deployment"]["status"] = {
            "availableReplicas": 0,
            "readyReplicas": 0,
            "unavailableReplicas": 1,
        }
    target = java_target(resources)
    with (
        patch.dict(
            os.environ,
            {
                "DEPLOY_JAVA_DEPLOYMENT_UID": expected_deployment_uid,
                "DEPLOY_JAVA_SERVICE_UID": "service-uid",
            },
            clear=True,
        ),
        patch.object(checks, "Target", return_value=target),
        patch.object(checks, "check_render"),
        patch.object(checks, "secret_and_image"),
        patch.object(lifecycle, "apply") as apply,
        pytest.raises(checks.DeploymentError, match="authoritative Java"),
    ):
        lifecycle.execute(checks, ["deploy"])
    apply.assert_not_called()


def test_qa_aggregate_requires_go_beside_java() -> None:
    checks.validate_qa_aggregate(["./dashboard", "./dashboard/legacy", "./product"])
    with pytest.raises(checks.DeploymentError, match="Go Dashboard"):
        checks.validate_qa_aggregate(["./dashboard/legacy", "./product"])
    with pytest.raises(checks.DeploymentError, match="Java Dashboard"):
        checks.validate_qa_aggregate(["./dashboard", "./product"])


def test_deployment_adapter_maps_interrupt_to_shell_status(monkeypatch) -> None:
    def interrupted(_arguments=None):
        os.kill(os.getpid(), signal.SIGINT)
        return 0

    monkeypatch.setattr(deployment, "main", interrupted)
    assert deployment.cli(["deploy-status"]) == 130


def test_deployment_adapter_reports_guard_failure(monkeypatch, capsys) -> None:
    monkeypatch.setattr(
        deployment,
        "main",
        Mock(side_effect=checks.DeploymentError("synthetic guard")),
    )
    assert deployment.cli(["deploy-status"]) == 1
    captured = capsys.readouterr()
    assert captured.out == ""
    assert "dashboard-deployment: synthetic guard" in captured.err
