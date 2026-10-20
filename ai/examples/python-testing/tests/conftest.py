from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path

import pytest

from example_cli import CommandResult


@dataclass
class FakeRunner:
    result: CommandResult = CommandResult(0, '{"records": 2, "status": "ok"}', "")
    calls: list[tuple[list[str], str, float]] = field(default_factory=list)

    def __call__(self, arguments: list[str], input_text: str, timeout: float) -> CommandResult:
        self.calls.append((list(arguments), input_text, timeout))
        return self.result


@pytest.fixture
def cli_paths(tmp_path: Path) -> tuple[Path, Path, Path]:
    source = tmp_path / "source.json"
    source.write_text(json.dumps({"records": [{"id": 1}, {"id": 2}]}), encoding="utf-8")
    output = tmp_path / "report.json"
    inspector = tmp_path / "inspector.py"
    inspector.write_text("# fake boundary supplied by the test\n", encoding="utf-8")
    return source, output, inspector


@pytest.fixture
def fake_runner() -> FakeRunner:
    return FakeRunner()
