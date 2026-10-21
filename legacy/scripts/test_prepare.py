"""Focused host preparation decisions without changing the host."""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

import prepare


def test_hosts_update_is_idempotent() -> None:
    # given
    old = "127.0.0.1 localhost\n192.0.2.2 kafkaserver\n"
    # when
    first = prepare.updated_hosts(old)
    second = prepare.updated_hosts(first)
    # then
    assert first == "127.0.0.1 localhost kafkaserver\n192.0.2.2\n"
    assert second == first


def test_dry_run_does_not_mutate(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    hosts = tmp_path / "hosts"
    hosts.write_text("127.0.0.1 localhost\n", encoding="utf-8")
    calls = []
    monkeypatch.setattr(
        prepare, "run_iptables", lambda port, action, **_kwargs: calls.append(action) or False
    )
    monkeypatch.setattr(prepare, "template_exists", lambda **_kwargs: False)
    monkeypatch.setattr(
        prepare, "create_template", lambda **_kwargs: pytest.fail("dry run created template")
    )
    # when
    prepare.prepare(dry_run=True, hosts=hosts)
    # then
    assert hosts.read_text(encoding="utf-8") == "127.0.0.1 localhost\n"
    assert calls == ["-C"] * 3


def test_invalid_target_is_rejected_before_operations() -> None:
    # when / then
    with pytest.raises(SystemExit) as error:
        prepare.main(["--target", "qa"])
    assert error.value.code == 2


def test_firewall_inspection_error_stops_before_mutation(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    hosts = tmp_path / "hosts"
    original = "127.0.0.1 localhost\n"
    hosts.write_text(original, encoding="utf-8")

    def fail_inspection(_port: int, _action: str, **_kwargs: object) -> bool:
        raise RuntimeError("firewall inspection failed")

    monkeypatch.setattr(prepare, "run_iptables", fail_inspection)
    monkeypatch.setattr(
        prepare,
        "template_exists",
        lambda **_kwargs: pytest.fail("continued after firewall inspection failure"),
    )

    # when / then
    with pytest.raises(RuntimeError, match="firewall inspection failed"):
        prepare.prepare(dry_run=False, hosts=hosts)
    assert hosts.read_text(encoding="utf-8") == original


def test_iptables_check_distinguishes_inspection_error(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    class FailedInspection:
        pid = 999_999
        returncode = 2

        def communicate(self, *, timeout: float) -> tuple[str, str]:
            assert timeout > 0
            return "", "permission denied"

    monkeypatch.setattr(prepare.subprocess, "Popen", lambda *_args, **_kwargs: FailedInspection())
    monkeypatch.setattr(prepare, "process_group_exists", lambda _process: False)

    # when / then
    with pytest.raises(RuntimeError, match="inspection failed.*permission denied"):
        prepare.run_iptables(80, "-C")


def test_iptables_timeout_owns_cleanup(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    class TimedOutInspection:
        pid = 999_999
        returncode = None

        def communicate(self, *, timeout: float) -> tuple[str, str]:
            raise subprocess.TimeoutExpired(["iptables", "-C"], timeout)

    cleaned: list[float] = []
    monkeypatch.setattr(prepare.subprocess, "Popen", lambda *_args, **_kwargs: TimedOutInspection())
    monkeypatch.setattr(
        prepare,
        "terminate_process_group",
        lambda _process, *, teardown_seconds: cleaned.append(teardown_seconds) or True,
    )

    # when / then
    with pytest.raises(RuntimeError, match="timed out; firewall state is unknown"):
        prepare.run_iptables(80, "-C")
    assert cleaned == [prepare.TEARDOWN_TIMEOUT_SECONDS]


@pytest.mark.parametrize("error", [KeyboardInterrupt(), prepare.PrepareTerminated()])
def test_iptables_interruption_owns_cleanup(
    error: BaseException, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    class InterruptedInspection:
        pid = 999_999
        returncode = None

        def communicate(self, *, timeout: float) -> tuple[str, str]:
            assert timeout > 0
            raise error

    cleaned: list[float] = []
    monkeypatch.setattr(
        prepare.subprocess, "Popen", lambda *_args, **_kwargs: InterruptedInspection()
    )
    monkeypatch.setattr(
        prepare,
        "terminate_process_group",
        lambda _process, *, teardown_seconds: cleaned.append(teardown_seconds) or True,
    )

    # when / then
    with pytest.raises(type(error)):
        prepare.run_iptables(80, "-C")
    assert cleaned == [prepare.TEARDOWN_TIMEOUT_SECONDS]


@pytest.mark.parametrize(
    ("error", "expected_status"),
    [
        (KeyboardInterrupt(), 130),
        (prepare.PrepareTerminated(), 143),
        (subprocess.TimeoutExpired(["iptables"], 10), 1),
    ],
)
def test_main_maps_interruption_and_timeout(
    error: BaseException, expected_status: int, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    def fail(**_kwargs: object) -> None:
        raise error

    monkeypatch.setattr(prepare, "prepare", fail)

    # when
    status = prepare.main(["--target", "localhost"])

    # then
    assert status == expected_status
