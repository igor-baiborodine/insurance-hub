"""Common legacy CI selection and structured update contracts."""

from __future__ import annotations

import stat
import subprocess
import time
from pathlib import Path

import pytest

import release


@pytest.mark.parametrize(
    ("paths", "kind", "expected"),
    [
        (["legacy/policy-service/src/A.java"], "service", "policy-service"),
        (["legacy/policy-service-api/pom.xml"], "api", "policy-service-api"),
        (["docs/README.md"], "pr", None),
    ],
)
def test_select_single_module(paths: list[str], kind: str, expected: str | None) -> None:
    # when
    selected = release.select(paths, kind)
    # then
    assert selected == expected


def test_select_rejects_multiple_modules() -> None:
    # when / then
    with pytest.raises(ValueError, match="multiple legacy modules"):
        release.select(["legacy/auth-service/pom.xml", "legacy/policy-service/pom.xml"], "pr")


def test_select_jar_rejects_ambiguous_artifacts(tmp_path: Path) -> None:
    # given
    (tmp_path / "command-bus-1.jar").touch()
    (tmp_path / "command-bus-2.jar").touch()
    (tmp_path / "command-bus-1-sources.jar").touch()
    # when / then
    with pytest.raises(ValueError, match="expected one deployable JAR"):
        release.select_jar("command-bus", tmp_path)


def test_select_jar_ignores_sources(tmp_path: Path) -> None:
    # given
    expected = tmp_path / "policy-service-api-1.0.1.jar"
    expected.touch()
    (tmp_path / "policy-service-api-1.0.1-sources.jar").touch()
    # when
    selected = release.select_jar("policy-service-api", tmp_path)
    # then
    assert selected == expected


def test_manifest_updates_only_expected_container(tmp_path: Path) -> None:
    # given
    path = tmp_path / "deployment.yaml"
    path.write_text(
        "kind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n"
        "        - name: web-vue\n          image: ghcr.io/owner/repo/web-vue-legacy:1.0.1\n"
        "          # keep comment\n",
        encoding="utf-8",
    )
    # when
    changed = release.update_manifest(path, "ghcr.io/owner/repo/web-vue-legacy", "1.0.2")
    # then
    assert changed
    assert "web-vue-legacy:1.0.2" in path.read_text(encoding="utf-8")
    assert "# keep comment" in path.read_text(encoding="utf-8")


def test_pom_updates_only_matching_dependency() -> None:
    # given
    source = (
        '<project xmlns="http://maven.apache.org/POM/4.0.0"><dependencies>'
        "<dependency><artifactId>policy-service-api</artifactId><version>1.0.1</version></dependency>"
        "<dependency><artifactId>other</artifactId><version>2.0.0</version></dependency>"
        "</dependencies></project>"
    )
    # when
    revised = release.update_pom(source, "policy-service-api", "1.0.2")
    # then
    assert revised is not None
    assert "<version>1.0.2</version>" in revised
    assert "<artifactId>other</artifactId><version>2.0.0</version>" in revised
    assert release.update_pom(revised, "policy-service-api", "1.0.2") is None


@pytest.mark.parametrize(
    "module,image",
    [
        ("documents-service", "document-api-legacy"),
        ("agent-portal-gateway", "agent-portal-gateway-legacy"),
        ("policy-service", "policy-api-legacy"),
    ],
)
def test_image_mapping(module: str, image: str) -> None:
    assert release.image_name(module) == image


def test_run_git_cleans_group_after_success(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    class ExitedGit:
        pid = 999_999
        returncode = 0

        def communicate(self, *, timeout: float) -> tuple[str, str]:
            assert timeout > 0
            return "result\n", ""

    cleaned: list[float] = []
    monkeypatch.setattr(release.subprocess, "Popen", lambda *_args, **_kwargs: ExitedGit())
    monkeypatch.setattr(release, "process_group_exists", lambda _process: True)
    monkeypatch.setattr(
        release,
        "terminate_process_group",
        lambda _process, *, teardown_seconds: cleaned.append(teardown_seconds) or True,
    )

    # when
    output = release.run_git("status", teardown_seconds=1)

    # then
    assert output == "result\n"
    assert cleaned == [1]


def test_update_dependents_preserves_existing_git_identity(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    (tmp_path / "legacy").mkdir()
    subprocess.run(["git", "init", "-q", str(tmp_path)], check=True, timeout=5)
    for key, value in (("user.name", "Fixture User"), ("user.email", "fixture@example.invalid")):
        subprocess.run(["git", "-C", str(tmp_path), "config", key, value], check=True, timeout=5)
    monkeypatch.setattr(release, "ROOT", tmp_path)

    # when
    release.update_dependents("policy-service-api", "1.0.2", "synthetic-token")

    # then
    for key, expected in (("user.name", "Fixture User"), ("user.email", "fixture@example.invalid")):
        result = subprocess.run(
            ["git", "-C", str(tmp_path), "config", "--get", key],
            check=True,
            timeout=5,
            text=True,
            capture_output=True,
        )
        assert result.stdout.strip() == expected


@pytest.mark.parametrize("fail_push", [False, True])
def test_update_dependents_scopes_identity_and_cleans_askpass(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    fail_push: bool,
) -> None:
    # given
    sentinel = "synthetic-review-sentinel"
    pom = tmp_path / "legacy/dashboard-service/pom.xml"
    pom.parent.mkdir(parents=True)
    pom.write_text(
        '<project xmlns="http://maven.apache.org/POM/4.0.0"><dependencies>'
        "<dependency><artifactId>policy-service-api</artifactId><version>1.0.1</version>"
        "</dependency></dependencies></project>",
        encoding="utf-8",
    )
    calls: list[tuple[str, ...]] = []
    askpass_paths: list[Path] = []

    def fake_run_git(
        *args: str,
        env: dict[str, str] | None = None,
        deadline: float | None = None,
        teardown_seconds: float = 5,
    ) -> str:
        assert env is not None
        assert deadline is not None and deadline > time.monotonic()
        assert teardown_seconds == 5
        calls.append(args)
        askpass = Path(env["GIT_ASKPASS"])
        askpass_paths.append(askpass)
        assert askpass.is_file()
        assert stat.S_IMODE(askpass.stat().st_mode) == 0o700
        assert sentinel not in askpass.read_text(encoding="utf-8")
        assert sentinel not in " ".join(args)
        assert env["GIT_AUTHOR_NAME"] == "github-actions[bot]"
        assert env["GIT_COMMITTER_EMAIL"] == "github-actions[bot]@users.noreply.github.com"
        if fail_push and args[0] == "push":
            raise RuntimeError("controlled push failure")
        return ""

    monkeypatch.setattr(release, "ROOT", tmp_path)
    monkeypatch.setattr(release, "run_git", fake_run_git)

    # when
    if fail_push:
        with pytest.raises(RuntimeError, match="controlled push failure"):
            release.update_dependents("policy-service-api", "1.0.2", sentinel)
    else:
        release.update_dependents("policy-service-api", "1.0.2", sentinel)

    # then
    assert [args[0] for args in calls] == ["add", "commit", "pull", "push"]
    assert all(args[0] != "config" for args in calls)
    assert all(not path.exists() for path in askpass_paths)
    assert sentinel not in pom.read_text(encoding="utf-8")
    captured = capsys.readouterr()
    assert sentinel not in captured.out
    assert sentinel not in captured.err
