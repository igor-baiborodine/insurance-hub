"""Disposable failure fixtures for the repository AI framework gate."""

from pathlib import Path

from check_ai_framework import check_adapters, check_catalog, check_links, check_ticket_dir


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
        "Read `ai/skills/review/SKILL.md`.\n",
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
        "ai/skills/ticket-requirements-workflow/SKILL.md",
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
