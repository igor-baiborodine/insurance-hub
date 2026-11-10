"""Focused guards for the disposable Go event/search runner."""

from __future__ import annotations

import json

import event_integration


def test_runner_uses_ordinary_service_coordinates() -> None:
    assert event_integration.REGISTERED_TOPIC == "policy-registered"
    assert event_integration.TERMINATED_TOPIC == "policy-terminated"
    assert event_integration.CONSUMER_GROUP == "policy-search-service"
    assert event_integration.OWNERSHIP == "policy-search-disposable"


def test_passed_tests_reads_only_named_test_pass_records() -> None:
    output = "\n".join(
        [
            json.dumps({"Action": "pass", "Test": "TestSelected"}),
            json.dumps({"Action": "fail", "Test": "TestFailed"}),
            json.dumps({"Action": "pass", "Package": "example"}),
            "not-json",
        ]
    )

    assert event_integration.passed_tests(output) == {"TestSelected"}


def test_loopback_port_returns_bindable_port() -> None:
    assert event_integration.loopback_port() > 0
