"""Disposable failure fixtures for the repository AI framework gate."""

from pathlib import Path

from check_ai_framework import (
    check_adapters,
    check_catalog,
    check_ci_trigger,
    check_links,
    check_skill_references,
    check_ticket_dir,
    framework_markdown,
)


def _write(root: Path, name: str, content: str) -> Path:
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    return path


def test_broken_adapter_target_fails_with_path(tmp_path: Path) -> None:
    # given
    adapter = _write(
        tmp_path,
        ".github/skills/review/SKILL.md",
        "---\nname: review\ndescription: Review changes.\n---\n\n"
        "Read `ai/framework/skills/review/SKILL.md`.\n",
    )

    # when
    issues = check_adapters(tmp_path)

    # then
    assert any(
        str(adapter.relative_to(tmp_path)) in issue and "missing canonical skill target" in issue
        for issue in issues
    )


def test_missing_catalog_entry_fails_with_skill_name(tmp_path: Path) -> None:
    # given
    _write(tmp_path, "AGENTS.md", "# Skills\n\nNo catalog entry here.\n")
    _write(
        tmp_path,
        "ai/framework/skills/ticket-requirements-workflow/SKILL.md",
        "---\nname: ticket-requirements-workflow\ndescription: Assess requirements.\n---\n",
    )

    # when
    issues = check_catalog(tmp_path)

    # then
    assert "AGENTS.md: missing skill catalog entry: ticket-requirements-workflow" in issues


def test_broken_link_and_anchor_fail_at_source_line(tmp_path: Path) -> None:
    # given
    source = _write(
        tmp_path,
        "ai/manifest.md",
        "# Manifest\n\n[absent](missing.md)\n[wrong](target.md#not-a-heading)\n",
    )
    _write(tmp_path, "ai/target.md", "# Existing Heading\n")

    # when
    issues = check_links(tmp_path, [source])

    # then
    assert "ai/manifest.md:3: missing relative link target: missing.md" in issues
    assert "ai/manifest.md:4: missing Markdown anchor: target.md#not-a-heading" in issues


def test_invalid_readiness_state_fails_with_artifact_path(tmp_path: Path) -> None:
    # given
    ticket = tmp_path / "ai/artifacts/adhoc/example"
    _write(
        tmp_path,
        "ai/artifacts/adhoc/example/example-requirements.md",
        "### Readiness and Specification Handoff\n\n- **Status:** Green light\n",
    )

    # when
    issues = check_ticket_dir(tmp_path, ticket)

    # then
    assert any("example-requirements.md: invalid readiness state" in issue for issue in issues)


def test_completed_step_without_evidence_fails(tmp_path: Path) -> None:
    # given
    ticket = tmp_path / "ai/artifacts/adhoc/example"
    _write(
        tmp_path,
        "ai/artifacts/adhoc/example/example-delivery-steps.md",
        "## Progress\n\n- [x] Step 1: Done | Summary: [step](example-step-01.md)\n\n## Steps\n",
    )
    _write(
        tmp_path,
        "ai/artifacts/adhoc/example/example-step-01.md",
        "# Step 1\n\n## Validation Evidence\n\nNo checks recorded.\n",
    )

    # when
    issues = check_ticket_dir(tmp_path, ticket)

    # then
    assert any(
        "example-step-01.md: completed step has no validation evidence rows" in issue
        for issue in issues
    )


def test_completed_step_with_evidence_passes(tmp_path: Path) -> None:
    # given
    ticket = tmp_path / "ai/artifacts/adhoc/example"
    _write(
        tmp_path,
        "ai/artifacts/adhoc/example/example-requirements.md",
        "### Readiness and Specification Handoff\n\n- **Status:** Ready\n",
    )
    _write(
        tmp_path,
        "ai/artifacts/adhoc/example/example-delivery-steps.md",
        "## Progress\n\n- [x] Step 1: Done | Summary: [step](example-step-01.md)\n\n## Steps\n",
    )
    _write(
        tmp_path,
        "ai/artifacts/adhoc/example/example-step-01.md",
        "# Step 1\n\n## Validation Evidence\n\n"
        "| Check | Status |\n| --- | --- |\n| Fixture | passed |\n",
    )

    # when
    issues = check_ticket_dir(tmp_path, ticket)

    # then
    assert issues == []


def test_consumer_discovery_excludes_dependency_and_artifacts(tmp_path: Path) -> None:
    # given
    local = _write(tmp_path, "ai/consumer-profile.md", "# Local owner\n")
    adapter = _write(tmp_path, ".agents/skills/code-review/SKILL.md", "Read shared review.\n")
    _write(tmp_path, "ai/framework/AGENTS.md", "# Contributor policy, not consumer policy\n")
    _write(tmp_path, "ai/artifacts/local/note.md", "# Private evidence\n")

    # when
    selected = framework_markdown(tmp_path)

    # then
    assert set(selected) == {local, adapter}


def test_catalog_reads_shared_skills_without_local_copies(tmp_path: Path) -> None:
    # given
    _write(
        tmp_path,
        "AGENTS.md",
        "[review](ai/framework/skills/code-review/SKILL.md)\n",
    )
    _write(
        tmp_path,
        "ai/framework/skills/code-review/SKILL.md",
        "---\nname: code-review\ndescription: Canonical workflow.\n---\n",
    )

    # when
    issues = check_catalog(tmp_path)

    # then
    assert issues == []


def test_legacy_skill_does_not_mask_missing_shared_source(tmp_path: Path) -> None:
    # given
    source = _write(
        tmp_path,
        "CLAUDE.md",
        "Read `ai/framework/skills/code-review/SKILL.md`.\n",
    )
    _write(tmp_path, "ai/skills/code-review/SKILL.md", "# Obsolete local workflow\n")

    # when
    missing_shared = check_skill_references(tmp_path, [source])
    source.write_text("Read `ai/skills/code-review/SKILL.md`.\n")
    legacy_route = check_skill_references(tmp_path, [source])

    # then
    assert missing_shared == ["CLAUDE.md:1: stale canonical skill reference: code-review"]
    assert legacy_route == [
        "CLAUDE.md:1: use direct canonical skill path: ai/framework/skills/code-review/SKILL.md"
    ]


def test_ci_requires_explicit_pinned_submodule_preparation(tmp_path: Path) -> None:
    # given
    workflow = _write(
        tmp_path,
        ".github/workflows/ai-framework.yml",
        "on:\n  pull_request:\n    paths: [AGENTS.md, 'ai/**', '.github/skills/**', "
        "'.claude/**', '.agents/**', '.continue/**', .gitmodules, 'scripts/ai/**']\n"
        "jobs:\n  ai-check:\n    steps:\n"
        "      - uses: actions/checkout@v6\n"
        "      - run: make ai-check\n",
    )

    # when
    unprepared = check_ci_trigger(tmp_path)
    workflow.write_text(
        workflow.read_text().replace(
            "      - uses: actions/checkout@v6\n",
            "      - uses: actions/checkout@v6\n        with:\n          submodules: recursive\n"
            "          persist-credentials: false\n",
        )
    )
    prepared = check_ci_trigger(tmp_path)

    # then
    assert unprepared == [
        ".github/workflows/ai-framework.yml: checkout must prepare pinned submodules without persistent auth"
    ]
    assert prepared == []
