"""Pytest coverage for observability signal, deadline, and process-group ownership."""

from __future__ import annotations

import os
import signal
import stat
import subprocess
import sys
import time
from pathlib import Path
from unittest import mock

import probe_chat
import probe_dashboard
import probe_document
import probe_pricing
import probe_product
import pytest

import alloy_runtime
import backend_ingestion
import monitoring_runtime

RUNTIMES = (alloy_runtime, monitoring_runtime)


def test_protected_environment_file_is_private_and_keeps_secret_out_of_caller_argv(
    tmp_path: Path,
) -> None:
    # given
    destination = tmp_path / "postgres.env"
    sentinel = "SENTINEL-protected-database-value"

    # when
    alloy_runtime.write_protected_env_file(destination, {"POSTGRES_PASSWORD": sentinel})
    docker_arguments = ["--env-file", str(destination)]

    # then
    assert stat.S_IMODE(destination.stat().st_mode) == 0o600
    assert destination.read_text(encoding="utf-8") == f"POSTGRES_PASSWORD={sentinel}\n"
    assert sentinel not in docker_arguments


@pytest.mark.parametrize("value", ("line one\nline two", "line one\rline two"))
def test_protected_environment_file_rejects_multiline_values(tmp_path: Path, value: str) -> None:
    # when / then
    with pytest.raises(ValueError, match="line break"):
        alloy_runtime.write_protected_env_file(tmp_path / "unsafe.env", {"VALUE": value})
    assert not (tmp_path / "unsafe.env").exists()


@pytest.mark.parametrize("runtime", RUNTIMES)
@pytest.mark.parametrize(("signum", "status"), ((signal.SIGINT, 130), (signal.SIGTERM, 143)))
def test_interruption_status_matches_supported_posix_contract(
    runtime, signum: int, status: int
) -> None:
    # when
    interrupted = runtime.Interrupted(signum)

    # then
    assert interrupted.status == status
    assert signal.Signals(signum).name in str(interrupted)


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_bounded_activity_interrupts_expired_work(runtime) -> None:
    # when / then
    with pytest.raises(runtime.DeadlineExceeded, match="work budget"):
        with runtime.bounded_activity("controlled deadline test", 0.02):
            time.sleep(0.2)


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_work_deadline_resumes_after_intermediate_cleanup(runtime) -> None:
    # when / then
    with pytest.raises(runtime.DeadlineExceeded, match="work budget"):
        with runtime.bounded_activity("intermediate cleanup", 0.15):
            with runtime.teardown_activity(0.3, resume_work=True):
                time.sleep(0.04)
            time.sleep(0.25)


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_process_group_cleanup_escalates_and_reaps_descendants(runtime) -> None:
    # given
    program = """
import signal
import subprocess
import sys
import time

subprocess.Popen([
    sys.executable,
    "-c",
    "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(60)",
])
signal.signal(signal.SIGTERM, lambda *_args: raise_exit())

def raise_exit():
    raise SystemExit(0)

print("ready", flush=True)
time.sleep(60)
"""
    process = subprocess.Popen(
        [sys.executable, "-c", program],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    assert process.stdout is not None
    assert process.stdout.readline().strip() == "ready"

    # when
    runtime.terminate_process_group(process, graceful_seconds=0.1, teardown_seconds=2)

    # then
    assert process.poll() is not None
    with pytest.raises(ProcessLookupError):
        os.killpg(process.pid, 0)


def test_parent_probe_timeout_allows_child_finally(tmp_path: Path) -> None:
    # given
    probe = tmp_path / "fake_probe.py"
    marker = tmp_path / "cleanup.marker"
    probe.write_text(
        "import signal,sys,time\n"
        "from pathlib import Path\n"
        "signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))\n"
        "try: time.sleep(60)\n"
        "finally: Path(sys.argv[1]).write_text('cleaned')\n",
        encoding="utf-8",
    )
    harness = backend_ingestion.DockerHarness("fixture", tmp_path)

    # when / then
    with (
        mock.patch.object(harness, "container_exists", return_value=False),
        pytest.raises(subprocess.TimeoutExpired),
    ):
        harness.probe([sys.executable, str(probe), str(marker)], tmp_path / "owner", timeout=0.3)
    assert marker.read_text(encoding="utf-8") == "cleaned"


@pytest.mark.parametrize(("signum", "status"), ((signal.SIGINT, 130), (signal.SIGTERM, 143)))
def test_parent_only_signal_allows_probe_cleanup(tmp_path: Path, signum: int, status: int) -> None:
    # given
    started = tmp_path / "started"
    cleaned = tmp_path / "cleaned"
    probe = tmp_path / "probe.py"
    probe.write_text(
        "import signal,sys,time\n"
        "from pathlib import Path\n"
        "signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))\n"
        "Path(sys.argv[1]).write_text('started')\n"
        "try: time.sleep(60)\n"
        "finally: Path(sys.argv[2]).write_text('cleaned')\n",
        encoding="utf-8",
    )
    parent = tmp_path / "parent.py"
    parent.write_text(
        "import sys\n"
        "from pathlib import Path\n"
        "import alloy_runtime,backend_ingestion\n"
        "root=Path(sys.argv[1])\n"
        "harness=backend_ingestion.DockerHarness('fixture',root)\n"
        "harness.container_exists=lambda _name:False\n"
        "try:\n"
        "  with alloy_runtime.controlled_signals():\n"
        "    harness.probe([sys.executable,str(root/'probe.py'),str(root/'started'),str(root/'cleaned')],root/'owned')\n"
        "except alloy_runtime.Interrupted as error: sys.exit(error.status)\n",
        encoding="utf-8",
    )
    process = subprocess.Popen(
        [sys.executable, str(parent), str(tmp_path)],
        env={**os.environ, "PYTHONPATH": str(Path(backend_ingestion.__file__).parent)},
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    try:
        deadline = time.monotonic() + 5
        while not started.exists() and time.monotonic() < deadline:
            time.sleep(0.02)
        assert started.exists(), process.communicate(timeout=2)

        # when
        os.kill(process.pid, signum)
        _stdout, stderr = process.communicate(timeout=10)

        # then
        assert process.returncode == status, stderr
        assert cleaned.read_text(encoding="utf-8") == "cleaned"
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
            process.communicate(timeout=2)


def test_product_probe_docker_removal_has_a_finite_timeout(tmp_path: Path) -> None:
    # given
    args = type("Args", (), {"ownership_file": None})()
    observed: list[float] = []

    def remove(_arguments, **kwargs):
        observed.append(kwargs["timeout"])
        return subprocess.CompletedProcess([], 0)

    # when / then
    with (
        mock.patch.object(
            probe_product, "run", side_effect=probe_product.ProbeError("setup failed")
        ),
        mock.patch.object(probe_product.subprocess, "run", side_effect=remove),
        pytest.raises(probe_product.ProbeError, match="setup failed"),
    ):
        probe_product.execute_in_root(args, tmp_path)
    assert len(observed) == 1
    assert 0 < observed[0] <= 15


def test_dashboard_probe_uses_default_exporter_timeout() -> None:
    # given
    args = type(
        "Args",
        (),
        {
            "version": "git-135beef00000",
            "instance_id": "instance",
            "otlp_endpoint": "http://127.0.0.1:4317",
        },
    )()

    # when
    with mock.patch.dict(os.environ, {"OTEL_EXPORTER_OTLP_TIMEOUT": "sentinel"}):
        values = probe_dashboard.dashboard_environment(
            args, "http://127.0.0.1:9200", 18080, 19090, 18081
        )

    # then
    assert "OTEL_EXPORTER_OTLP_TIMEOUT" not in values
    assert values["SALES_TRENDS_TIME_ZONE"] == "GMT"
    assert values["OTEL_EXPORTER_OTLP_ENDPOINT"] == "http://127.0.0.1:4317"


def test_dashboard_probe_docker_removal_has_a_finite_timeout(tmp_path: Path) -> None:
    # given
    args = type("Args", (), {"ownership_file": None})()
    observed: list[float] = []

    def remove(_arguments, **kwargs):
        observed.append(kwargs["timeout"])
        return subprocess.CompletedProcess([], 0)

    # when / then
    with (
        mock.patch.object(
            probe_dashboard, "run", side_effect=probe_dashboard.ProbeError("setup failed")
        ),
        mock.patch.object(probe_dashboard.subprocess, "run", side_effect=remove),
        pytest.raises(probe_dashboard.ProbeError, match="setup failed"),
    ):
        probe_dashboard.execute_in_root(args, tmp_path)
    assert len(observed) == 1
    assert 0 < observed[0] <= 15


def test_document_probe_disables_workers_and_drops_inherited_credentials() -> None:
    # given
    args = type(
        "Args",
        (),
        {
            "version": "git-364000000000",
            "instance_id": "document-probe",
            "otlp_endpoint": "http://127.0.0.1:4317",
        },
    )()
    sentinel = "SENTINEL-document-observability-secret"

    # when
    with mock.patch.dict(
        os.environ,
        {
            "DOCUMENT_DATABASE_URL": sentinel,
            "MINIO_SECRET_KEY": sentinel,
            "EVENT_WORKERS_ENABLED": "true",
            "SERVICE_NAME": "foreign-service",
        },
    ):
        values = probe_document.environment(args)

    # then
    assert values["SERVICE_NAME"] == "document-service"
    assert values["EVENT_WORKERS_ENABLED"] == "false"
    assert values["OTEL_EXPORTER_OTLP_ENDPOINT"] == "http://127.0.0.1:4317"
    assert sentinel not in values.values()


def test_document_probe_timeout_reclaims_owned_process(tmp_path: Path) -> None:
    # given
    ownership = tmp_path / "owned"
    args = type(
        "Args",
        (),
        {
            "service_root": tmp_path,
            "log_file": tmp_path / "service.log",
            "ownership_file": ownership,
            "trace_id": "a" * 32,
            "version": "git-364000000000",
            "instance_id": "document-probe",
            "otlp_endpoint": "http://127.0.0.1:4317",
        },
    )()
    process = mock.Mock(pid=4364)
    process.wait.side_effect = subprocess.TimeoutExpired("probe", 60)

    # when / then
    with (
        mock.patch.object(probe_document, "run"),
        mock.patch.object(probe_document.subprocess, "Popen", return_value=process),
        mock.patch.object(probe_document.alloy_runtime, "terminate_process_group") as terminate,
        pytest.raises(probe_document.ProbeError, match="timed out"),
    ):
        probe_document.execute_in_root(args, tmp_path)
    assert terminate.call_count == 2
    assert not ownership.exists()


def test_chat_probe_uses_fixed_identity_and_keeps_sentinel_out_of_identity() -> None:
    # given
    args = type(
        "Args",
        (),
        {
            "version": "git-390000000000",
            "instance_id": "chat-probe",
            "otlp_endpoint": "http://127.0.0.1:4317",
        },
    )()

    # when
    with mock.patch.dict(os.environ, {"SERVICE_NAME": probe_chat.SENSITIVE_SENTINEL}):
        values = probe_chat.environment(args)

    # then
    assert values["SERVICE_NAME"] == "chat-service"
    assert values["SERVICE_VERSION"] == "git-390000000000"
    assert values["OTEL_EXPORTER_OTLP_ENDPOINT"] == "http://127.0.0.1:4317"
    assert probe_chat.SENSITIVE_SENTINEL not in {
        values["SERVICE_NAME"],
        values["SERVICE_VERSION"],
        values["SERVICE_INSTANCE_ID"],
    }


def test_chat_probe_timeout_reclaims_owned_process(tmp_path: Path) -> None:
    # given
    ownership = tmp_path / "owned"
    args = type(
        "Args",
        (),
        {
            "service_root": tmp_path,
            "log_file": tmp_path / "service.log",
            "ownership_file": ownership,
            "trace_id": "a" * 32,
            "version": "git-390000000000",
            "instance_id": "chat-probe",
            "otlp_endpoint": "http://127.0.0.1:4317",
        },
    )()
    process = mock.Mock(pid=4390)
    process.wait.side_effect = subprocess.TimeoutExpired("probe", 60)

    # when / then
    with (
        mock.patch.object(probe_chat, "run"),
        mock.patch.object(probe_chat.subprocess, "Popen", return_value=process),
        mock.patch.object(probe_chat.alloy_runtime, "terminate_process_group") as terminate,
        pytest.raises(probe_chat.ProbeError, match="timed out"),
    ):
        probe_chat.execute_in_root(args, tmp_path)
    assert terminate.call_count == 2
    assert not ownership.exists()


def test_forced_probe_recovery_reclaims_product_group_and_postgres(tmp_path: Path) -> None:
    # given
    ownership = tmp_path / "owned"
    ownership.write_text("5252\n", encoding="ascii")
    child = mock.Mock(pid=4242)
    child.communicate.side_effect = [
        subprocess.TimeoutExpired("probe", 1),
        subprocess.TimeoutExpired("probe", 1),
        ("", ""),
    ]
    harness = backend_ingestion.DockerHarness("fixture", tmp_path)

    # when / then
    with (
        mock.patch.object(backend_ingestion.subprocess, "Popen", return_value=child),
        mock.patch.object(backend_ingestion.os, "killpg") as kill_group,
        mock.patch.object(harness, "container_exists", return_value=True),
        mock.patch.object(harness, "command") as docker,
        pytest.raises(subprocess.TimeoutExpired),
    ):
        harness.probe([sys.executable, "probe.py"], ownership, timeout=1)
    assert kill_group.call_args_list == [
        mock.call(4242, signal.SIGTERM),
        mock.call(4242, signal.SIGKILL),
        mock.call(5252, signal.SIGKILL),
    ]
    docker.assert_called_once_with(["docker", "rm", "-f", "ih-product-probe-4242"], timeout=15)


def test_pricing_probe_keeps_harness_values_out_of_the_service_environment() -> None:
    # given
    inherited = {
        probe_pricing.SENTINEL_ENVIRONMENT: backend_ingestion.PRICING_SENSITIVE_SENTINEL,
        probe_pricing.FAILURE_TRACE_ENVIRONMENT: backend_ingestion.PRICING_FAILURE_TRACE_ID,
    }

    # when
    with mock.patch.dict(os.environ, inherited):
        values = probe_pricing.pricing_environment(
            "postgresql://reader", "http://127.0.0.1:4317", "git-135beef00000", "pod", 1, 2, 3
        )

    # then
    assert backend_ingestion.PRICING_SENSITIVE_SENTINEL not in values.values()
    assert backend_ingestion.PRICING_FAILURE_TRACE_ID not in values.values()
    assert values["DB_MAX_CONNS"] == "4"
    assert values["DEPLOYMENT_ENVIRONMENT"] == "qa"


def test_pricing_probe_restarts_the_stopped_database_after_a_failed_request() -> None:
    # given
    docker: list[list[str]] = []

    def run(arguments, **_kwargs):
        docker.append(list(arguments))
        return ""

    # when / then
    with (
        mock.patch.object(probe_pricing, "run", side_effect=run),
        mock.patch.object(probe_pricing, "wait_postgres") as wait_postgres,
        mock.patch.object(probe_pricing, "calculate", side_effect=OSError("connection reset")),
        pytest.raises(OSError, match="connection reset"),
    ):
        probe_pricing.exercise_outage("pg", "http://calculate", "http://ready", "0" * 32, "x")
    assert docker == [["docker", "stop", "--time", "5", "pg"], ["docker", "start", "pg"]]
    wait_postgres.assert_called_once_with("pg")


def test_pricing_probe_rejects_a_quote_from_the_stopped_database() -> None:
    # given
    with (
        mock.patch.object(probe_pricing, "run", return_value=""),
        mock.patch.object(probe_pricing, "wait_postgres"),
        mock.patch.object(probe_pricing, "calculate", return_value=(200, probe_pricing.TRI_QUOTE)),
        # when / then
        pytest.raises(probe_pricing.ProbeError, match="stopped database returned HTTP 200"),
    ):
        probe_pricing.exercise_outage("pg", "http://calculate", "http://ready", "0" * 32, "x")


def test_pricing_probe_docker_removal_has_a_finite_timeout(tmp_path: Path) -> None:
    # given
    args = type("Args", (), {"ownership_file": None})()
    observed: list[float] = []

    def remove(_arguments, **kwargs):
        observed.append(kwargs["timeout"])
        return subprocess.CompletedProcess([], 0)

    # when / then
    with (
        mock.patch.dict(
            os.environ,
            {
                probe_pricing.FAILURE_TRACE_ENVIRONMENT: "0" * 32,
                probe_pricing.SENTINEL_ENVIRONMENT: "sentinel",
            },
        ),
        mock.patch.object(
            probe_pricing, "run", side_effect=probe_pricing.ProbeError("setup failed")
        ),
        mock.patch.object(probe_pricing.subprocess, "run", side_effect=remove),
        pytest.raises(probe_pricing.ProbeError, match="setup failed"),
    ):
        probe_pricing.execute_in_root(args, tmp_path)
    assert len(observed) == 1
    assert 0 < observed[0] <= 15


@pytest.mark.parametrize(
    ("operations", "expected"),
    [
        ({("acquire", "success"): 2, ("acquire", "storage_unavailable"): 1}, "acquire"),
        ({("acquire", "success"): 3, ("query", "storage_unavailable"): 1}, "query"),
        ({("decode", "invalid_data"): 1}, None),
        ({("acquire", "storage_unavailable"): 1, ("query", "storage_unavailable"): 1}, None),
        ({("acquire", "success"): 3}, None),
    ],
)
def test_pricing_outage_stage_requires_one_unavailable_reader_stage(operations, expected) -> None:
    # when / then
    if expected is None:
        with pytest.raises(backend_ingestion.HarnessError):
            backend_ingestion.pricing_failed_stage(operations)
    else:
        assert backend_ingestion.pricing_failed_stage(operations) == expected
