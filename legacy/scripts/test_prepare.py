"""Focused host preparation decisions without changing the host."""

from __future__ import annotations

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
    monkeypatch.setattr(prepare, "run_iptables", lambda port, action: calls.append(action) or False)
    monkeypatch.setattr(prepare, "template_exists", lambda: False)
    monkeypatch.setattr(prepare, "create_template", lambda: pytest.fail("dry run created template"))
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
