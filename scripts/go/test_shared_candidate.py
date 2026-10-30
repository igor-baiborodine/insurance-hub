"""E01/E14 guards, exact selection, source lifetime and disposable real Go proof."""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path

import pytest

import library_tooling
import shared_candidate as candidate
from shared_support import DIRECTORY, MODULE, Budget, SharedError

ROOT = Path(__file__).resolve().parents[2]


class GoEnvironment:
    def __init__(self, work="off", flags="", replacements=None):
        self.work, self.flags, self.replacements = work, flags, replacements

    def run(self, arguments, *_args):
        if arguments[1] == "env":
            return json.dumps({"GOWORK": self.work, "GOFLAGS": self.flags})
        return json.dumps({"Replace": self.replacements})


def test_python_executable_preserves_virtual_environment_entry_point(
    tmp_path: Path, monkeypatch
) -> None:
    # given
    runtime = tmp_path / "runtime/python"
    runtime.parent.mkdir()
    runtime.touch()
    virtual_python = tmp_path / ".venv/bin/python"
    virtual_python.parent.mkdir(parents=True)
    virtual_python.symlink_to(runtime)
    monkeypatch.setenv("PYTHON", str(virtual_python))
    # when
    selected = candidate._python_executable(tmp_path)
    # then
    assert selected == str(virtual_python)


@pytest.mark.parametrize(
    "settings",
    [
        GoEnvironment(work="/tmp/go.work"),
        GoEnvironment(flags="-modfile=/tmp/alternate.mod"),
        GoEnvironment(flags="-mod=vendor"),
        GoEnvironment(replacements=[{"Old": {"Path": MODULE}}]),
    ],
)
def test_normal_guard_rejects_effective_overrides_before_forcing_flags(
    tmp_path: Path, settings
) -> None:
    with pytest.raises(SharedError):
        library_tooling.normal_guard(tmp_path, "go", settings)


def test_normal_guard_rejects_caller_flag_even_if_persistent_settings_hide_it(
    tmp_path: Path, monkeypatch
) -> None:
    monkeypatch.setenv("GOFLAGS", "-modfile=/tmp/alternate.mod")
    with pytest.raises(SharedError, match="alternate"):
        library_tooling.normal_guard(tmp_path, "go", GoEnvironment())


def test_candidate_context_cannot_be_enabled_by_a_flag(monkeypatch) -> None:
    monkeypatch.delenv("SERVICEKIT_CONTEXT_SOCKET", raising=False)
    monkeypatch.delenv("SERVICEKIT_CONTEXT_TOKEN", raising=False)
    with pytest.raises(SharedError, match="live owner"):
        candidate.phase("unit")


@pytest.mark.parametrize(
    "package",
    [
        {"ImportPath": MODULE + "/domain"},
        {
            "ImportPath": MODULE + "/config",
            "Imports": [
                MODULE.replace("libs/servicekit", "services/product-service/internal/domain")
            ],
        },
    ],
)
def test_runtime_scope_rejects_business_package_and_private_import(package: dict) -> None:
    with pytest.raises(SharedError):
        library_tooling.scope_check([package])


@pytest.mark.parametrize("defect", ["stale", "directory", "alternate"])
def test_actual_graph_proof_rejects_stale_pin_or_wrong_source(tmp_path: Path, defect: str) -> None:
    # given
    library, alternate = tmp_path / "library", tmp_path / "candidate.mod"
    graph = [
        {"Path": "example.com/consumer", "Main": True, "GoMod": str(alternate)},
        {
            "Path": MODULE,
            "Version": "v0.1.0",
            "Replace": {"Dir": str(library), "GoMod": str(library / "go.mod")},
        },
    ]
    if defect == "stale":
        del graph[1]["Replace"]
    elif defect == "directory":
        graph[1]["Replace"]["Dir"] = "/wrong"
    else:
        graph[0]["GoMod"] = str(tmp_path / "go.mod")
    # when / then
    with pytest.raises(SharedError):
        candidate.verify_selection(graph, library, alternate)


@pytest.mark.integration
def test_real_go_candidate_uses_changed_source_and_preserves_original_manifests(
    tmp_path: Path, monkeypatch
) -> None:
    # given: unpublished candidate differs from required v0.1.0; no proxy needed.
    monkeypatch.setenv("GOPROXY", "off")
    monkeypatch.setenv("GOSUMDB", "off")
    library = tmp_path / DIRECTORY
    library.mkdir(parents=True)
    (library / "go.mod").write_text(f"module {MODULE}\n\ngo 1.27.3\n")
    (library / "go.sum").touch()
    (library / "value.go").write_text('package servicekit\nconst Revision = "candidate"\n')
    consumer = tmp_path / "consumer"
    consumer.mkdir()
    (consumer / "go.mod").write_text(
        f"module example.com/consumer\n\ngo 1.27.3\nrequire {MODULE} v0.1.0\n"
    )
    (consumer / "go.sum").touch()
    (consumer / "value_test.go").write_text(
        f'package consumer\nimport ("testing"; kit "{MODULE}")\n'
        'func TestCandidate(t *testing.T) { if kit.Revision != "candidate" {t.Fatal("stale pin")} }\n'
    )
    before = candidate.locks(consumer)
    # The test target is the explicit owner for this disposable Go fixture.
    (consumer / "Makefile").write_text("test:\n\tgo test -count=1 ./...\n")
    # when
    context = candidate.Candidate(tmp_path, "consumer", "a" * 40, Budget(120))
    Budget(60).run(["make", "test"], consumer, "candidate fixture", context.env)
    context.verify()
    # then
    assert candidate.locks(consumer) == before
    assert context.verify()[1]["Replace"]["Dir"] == str(library)
    (library / "value.go").write_text('package servicekit\nconst Revision = "tampered"\n')
    with pytest.raises(SharedError, match="candidate source changed"):
        context.verify()


def committed_fixture(root: Path, body: str) -> str:
    library = root / DIRECTORY
    library.mkdir(parents=True)
    (library / "go.mod").write_text(f"module {MODULE}\n\ngo 1.27.3\n")
    (library / "go.sum").touch()
    (library / "value.go").write_text('package servicekit\nconst Revision = "candidate"\n')
    consumer = root / "consumer"
    consumer.mkdir()
    (consumer / "go.mod").write_text(
        f"module example.com/consumer\n\ngo 1.27.3\nrequire {MODULE} v0.1.0\n"
    )
    (consumer / "go.sum").touch()
    (consumer / "value_test.go").write_text(
        f'package consumer\nimport ("testing"; kit "{MODULE}")\n'
        'func TestCandidate(t *testing.T) { if kit.Revision != "candidate" {t.Fatal("stale pin")} }\n'
    )
    (consumer / "Makefile").write_text("shared-unit:\n\t" + body + "\n")
    for args in (
        ["init", "-q"],
        ["add", "."],
        [
            "-c",
            "user.name=Fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "-c",
            "commit.gpgsign=false",
            "commit",
            "-qm",
            "fixture",
        ],
    ):
        subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, timeout=10)
    return subprocess.check_output(
        ["git", "rev-parse", "HEAD"], cwd=root, text=True, timeout=10
    ).strip()


@pytest.mark.integration
def test_isolated_checkout_keeps_requested_identity_without_caller_changes(tmp_path: Path) -> None:
    # given: the requested commit differs from both caller HEAD and dirty source.
    root = tmp_path / "original"
    sha = committed_fixture(root, "true")
    created = subprocess.check_output(
        ["git", "show", "-s", "--format=%cI", sha], cwd=root, text=True, timeout=10
    ).strip()
    original_content = (root / "consumer/value_test.go").read_bytes()
    source = root / "consumer/value_test.go"
    source.write_text("committed later\n")
    subprocess.run(["git", "add", "."], cwd=root, check=True, capture_output=True, timeout=10)
    subprocess.run(
        [
            "git",
            "-c",
            "user.name=Fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "-c",
            "commit.gpgsign=false",
            "commit",
            "-qm",
            "later",
        ],
        cwd=root,
        check=True,
        capture_output=True,
        timeout=10,
    )
    source.write_text("caller dirty source\n")
    original = candidate.source_manifest(root)
    index = (root / ".git/index").read_bytes()
    head = (root / ".git/HEAD").read_bytes()
    destination = tmp_path / "checkout"
    # when
    candidate._checkout(root, sha, destination, Budget(60))
    # then: source, full SHA, short version and commit time belong to the requested commit.
    assert (destination / "consumer/value_test.go").read_bytes() == original_content
    identity = subprocess.check_output(
        ["git", "show", "-s", "--format=%H%n%cI", "HEAD"],
        cwd=destination,
        text=True,
        timeout=10,
    ).splitlines()
    assert identity == [sha, created]
    assert (destination / ".git").is_dir()
    assert not (destination / ".git/objects/info/alternates").exists()
    assert candidate.source_manifest(root) == original
    assert (root / ".git/index").read_bytes() == index
    assert (root / ".git/HEAD").read_bytes() == head


@pytest.mark.integration
@pytest.mark.parametrize("mode", ["normal", "candidate"])
def test_controller_phases_receive_exact_git_image_identity(
    tmp_path: Path, monkeypatch, mode: str
) -> None:
    # given: an image owner asks Git for the same identity that Product's Makefile uses.
    monkeypatch.setenv("GOPROXY", "off")
    monkeypatch.setenv("GOSUMDB", "off")
    root = tmp_path / "original"
    marker = tmp_path / "identity"
    command = f"git show -s --format='%H%n%cI' HEAD > '{marker}'"
    if mode == "candidate":
        command += f'\n\tSERVICEKIT_PHASE=unit "$(PYTHON)" "{ROOT}/scripts/go/shared_cli.py" phase'
    sha = committed_fixture(root, command)
    if mode == "normal":
        # A standalone offline consumer needs no published module to exercise this boundary.
        (root / "consumer/go.mod").write_text("module example.com/consumer\n\ngo 1.27.3\n")
        (root / "consumer/value_test.go").write_text("package consumer\n")
        subprocess.run(["git", "add", "."], cwd=root, check=True, capture_output=True, timeout=10)
        subprocess.run(
            [
                "git",
                "-c",
                "user.name=Fixture",
                "-c",
                "user.email=fixture@example.invalid",
                "-c",
                "commit.gpgsign=false",
                "commit",
                "-qm",
                "standalone",
            ],
            cwd=root,
            check=True,
            capture_output=True,
            timeout=10,
        )
        sha = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=root, text=True, timeout=10
        ).strip()
    expected_identity = subprocess.check_output(
        ["git", "show", "-s", "--format=%H%n%cI", sha], cwd=root, text=True, timeout=10
    )
    expected = {
        "base": sha,
        "head": sha,
        "obligations": [
            {"module": "consumer", "phase": "unit", "mode": mode, "target": "shared-unit"}
        ],
    }
    original = candidate.source_manifest(root)
    # when
    reports = candidate.validate(root, expected, "consumer", tmp_path / "tools")
    # then
    assert marker.read_text() == expected_identity
    assert reports[0]["head"] == sha
    assert reports[0]["status"] == "passed"
    assert candidate.source_manifest(root) == original


@pytest.mark.integration
@pytest.mark.parametrize("failure", [False, True])
def test_controller_issues_real_phase_receipt_and_cleans_on_failure(
    tmp_path: Path, monkeypatch, failure: bool
) -> None:
    # given
    monkeypatch.setenv("GOPROXY", "off")
    monkeypatch.setenv("GOSUMDB", "off")
    root = tmp_path / "original"
    command = (
        "false"
        if failure
        else f'SERVICEKIT_PHASE=unit "$(PYTHON)" "{ROOT}/scripts/go/shared_cli.py" phase'
    )
    sha = committed_fixture(root, command)
    expected = {
        "base": sha,
        "head": sha,
        "obligations": [
            {"module": "consumer", "phase": "unit", "mode": "candidate", "target": "shared-unit"}
        ],
    }
    original = candidate.source_manifest(root)
    # when / then
    if failure:
        with pytest.raises(RuntimeError):
            candidate.validate(root, expected, "consumer", tmp_path / "tools")
    else:
        reports = candidate.validate(root, expected, "consumer", tmp_path / "tools")
        assert reports[0]["candidate_sha"] == sha
        assert reports[0]["phase"] == "unit"
        assert not Path(reports[0]["candidate_directory"]).exists()
    assert candidate.source_manifest(root) == original


@pytest.mark.integration
def test_controller_sigterm_cleans_override_and_owned_child(tmp_path: Path) -> None:
    # given
    marker = tmp_path / "started"
    sha = committed_fixture(tmp_path / "original", f"touch '{marker}'; sleep 90")
    expected = {
        "base": sha,
        "head": sha,
        "obligations": [
            {"module": "consumer", "phase": "unit", "mode": "candidate", "target": "shared-unit"}
        ],
    }
    scratch = tmp_path / "scratch"
    scratch.mkdir()
    runner = tmp_path / "runner.py"
    runner.write_text(
        "from pathlib import Path\nfrom shared_support import cli_entry\n"
        "from shared_candidate import validate\n"
        f"cli_entry(lambda: validate(Path({str(tmp_path / 'original')!r}), {expected!r}, 'consumer', Path('/unused')))\n"
    )
    env = {
        **os.environ,
        "PYTHONPATH": str(ROOT / "scripts/go"),
        "PYTHON": sys.executable,
        "TMPDIR": str(scratch),
        "GOPROXY": "off",
        "GOSUMDB": "off",
    }
    process = subprocess.Popen(
        [sys.executable, str(runner)],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        deadline = time.monotonic() + 20
        while not marker.exists() and process.poll() is None and time.monotonic() < deadline:
            time.sleep(0.05)
        assert marker.exists(), process.communicate(timeout=2)
        # when
        process.send_signal(signal.SIGTERM)
        process.communicate(timeout=20)
        # then
        assert process.returncode == 143
        assert not list(scratch.iterdir())
    finally:
        if process.poll() is None:
            process.kill()
            process.communicate(timeout=5)


@pytest.mark.integration
def test_composite_owner_reports_every_basic_candidate_phase(tmp_path: Path, monkeypatch) -> None:
    # given
    monkeypatch.setenv("GOPROXY", "off")
    monkeypatch.setenv("GOSUMDB", "off")
    root = tmp_path / "original"
    tools = ROOT / "libs/servicekit/.tools/bin"
    phases = sorted(candidate.SIMPLE_PHASES)
    commands = [
        f'SERVICEKIT_PHASE={phase} "$(PYTHON)" "{ROOT}/scripts/go/shared_cli.py" phase'
        for phase in phases
    ]
    committed_fixture(root, "\n\t".join(commands))
    module = root / "consumer"
    config = (
        (ROOT / "libs/servicekit/.golangci.yml").read_text().replace(MODULE, "example.com/consumer")
    )
    (module / ".golangci.yml").write_text(config)
    makefile = module / "Makefile"
    makefile.write_text(makefile.read_text() + f'prepare:\n\t"{tools}/golangci-lint" fmt\n')
    subprocess.run(["make", "prepare"], cwd=module, check=True, capture_output=True, timeout=30)
    subprocess.run(["git", "add", "."], cwd=root, check=True, capture_output=True, timeout=10)
    subprocess.run(
        [
            "git",
            "-c",
            "user.name=Fixture",
            "-c",
            "user.email=fixture@example.invalid",
            "-c",
            "commit.gpgsign=false",
            "commit",
            "-qm",
            "phase fixtures",
        ],
        cwd=root,
        check=True,
        capture_output=True,
        timeout=10,
    )
    sha = subprocess.check_output(
        ["git", "rev-parse", "HEAD"], cwd=root, text=True, timeout=10
    ).strip()
    expected = {
        "base": sha,
        "head": sha,
        "obligations": [
            {"module": "consumer", "phase": phase, "mode": "candidate", "target": "shared-unit"}
            for phase in phases
        ],
    }
    # when
    reports = candidate.validate(root, expected, "consumer", tools)
    # then
    assert {report["phase"] for report in reports} == candidate.SIMPLE_PHASES
    assert all(report["candidate_sha"] == sha for report in reports)
    assert all(not Path(report["candidate_directory"]).exists() for report in reports)
