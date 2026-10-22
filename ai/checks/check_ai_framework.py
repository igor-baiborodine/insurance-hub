#!/usr/bin/env python3
"""Check tracked AI framework structure and optionally one local ticket directory."""

from __future__ import annotations

import argparse
import os
import re
import signal
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

import yaml

LINK = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")
HEADING = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$")
SKILL_REFERENCE = re.compile(r"ai/skills/([a-z0-9-]+)/SKILL\.md")
CATALOG_REFERENCE = re.compile(r"\]\(ai/skills/([a-z0-9-]+)/SKILL\.md\)")
COMPLETED_STEP = re.compile(r"^- \[x\] Step \d+:.*$", re.MULTILINE)
SUMMARY_REFERENCE = re.compile(r"Summary:\s*\[[^\]]+\]\(([^)]+)\)")
READINESS = re.compile(r"^\s*- \*\*Status:\*\*\s*(.+)$", re.MULTILINE)
READY_STATES = ("blocked", "ready", "conditionally ready for decision work")
EVIDENCE_STATUSES = {"passed", "failed", "blocked", "not run", "not applicable"}

TEMPLATE_FIELDS = {
    "requirements-template.md": (
        "### Context and Evidence",
        "### Scope",
        "### Acceptance Criteria Assessment",
        "#### Must-have (80-part)",
        "#### Nice-to-have (20-part)",
        "### Open Questions and Decisions to Be Made",
        "### Readiness and Specification Handoff",
        "**Status:**",
        "[What happens now",
    ),
    "ticket-content-template.md": (
        "### Context",
        "### Description",
        "### Acceptance Criteria",
        "### Dev Notes",
        "**Open questions and prerequisite decisions**",
        "[Explain the current behavior",
    ),
    "delivery-steps-template.md": (
        "## Specification",
        "## Readiness",
        "## Progress",
        "## Steps",
        "Summary:",
        "[Decision IDs",
    ),
}

LIFECYCLE_MARKERS = {
    "ticket-requirements-workflow": (
        "**Blocking:**",
        "**Resolved:**",
        "**Deferred to delivery:**",
        "conditionally ready for decision work",
    ),
    "ticket-specification-workflow": (
        "## Inputs and Readiness",
        "## Draft the Ticket Content",
        "do not create",
    ),
    "ticket-implementation-workflow": (
        "plan-only request ends",
        "resolve prerequisite decision gates",
        "ticket-post-step-workflow/SKILL.md",
    ),
    "ticket-post-step-workflow": (
        "## Required Post-Step Actions",
        "### Validation evidence record",
        "tested state",
        "| Check | Status | Source | Scope | Command or scenario | Tested state |",
    ),
}


class FrameworkTerminated(Exception):
    """The CLI received a handled termination signal."""


def _on_term(_signum: int, _frame: object) -> None:
    raise FrameworkTerminated


def _display(root: Path, path: Path) -> str:
    try:
        return path.relative_to(root).as_posix()
    except ValueError:
        return str(path)


def _read(root: Path, path: Path, issues: list[str]) -> str | None:
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        reason = error.strerror if isinstance(error, OSError) else "invalid UTF-8"
        issues.append(f"{_display(root, path)}: cannot read: {reason}")
        return None


def _without_fences(text: str) -> list[tuple[int, str]]:
    lines: list[tuple[int, str]] = []
    fence: str | None = None
    for number, line in enumerate(text.splitlines(), start=1):
        match = re.match(r"^\s*(`{3,}|~{3,})", line)
        if match:
            marker = match.group(1)
            if fence is None:
                fence = marker[0]
            elif marker[0] == fence:
                fence = None
            continue
        if fence is None:
            lines.append((number, line))
    return lines


def _anchors(text: str) -> set[str]:
    anchors: set[str] = set()
    duplicates: dict[str, int] = {}
    for _, line in _without_fences(text):
        match = HEADING.match(line)
        if match is None:
            for identifier in re.findall(r'<a\s+[^>]*id=["\']([^"\']+)["\']', line):
                anchors.add(identifier)
            continue
        title = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", match.group(1))
        title = re.sub(r"[^\w -]", "", title.lower())
        slug = re.sub(r"\s+", "-", title.strip())
        count = duplicates.get(slug, 0)
        duplicates[slug] = count + 1
        anchors.add(f"{slug}-{count}" if count else slug)
    return anchors


def framework_markdown(root: Path) -> list[Path]:
    files = [root / name for name in ("AGENTS.md", "CLAUDE.md", "CONTINUE.md")]
    files.append(root / ".github/copilot-instructions.md")
    for directory in ("ai", ".github/skills", ".claude/skills", ".continue/rules"):
        files.extend(
            path
            for path in (root / directory).rglob("*.md")
            if not {"artifacts", "_legacy-artifacts"}.intersection(
                path.relative_to(root / directory).parts
            )
        )
    return sorted(path for path in files if path.is_file())


def check_links(root: Path, files: list[Path]) -> list[str]:
    """Validate local Markdown link paths and heading anchors."""
    issues: list[str] = []
    anchor_cache: dict[Path, set[str]] = {}
    for source in files:
        text = _read(root, source, issues)
        if text is None:
            continue
        for number, line in _without_fences(text):
            for match in LINK.finditer(line):
                raw = match.group(1).strip().split(" ", 1)[0].strip("<>")
                parsed = urlsplit(raw)
                if parsed.scheme or parsed.netloc or raw.startswith("/"):
                    continue
                target = (source.parent / unquote(parsed.path)).resolve()
                label = f"{_display(root, source)}:{number}"
                if not target.is_relative_to(root):
                    issues.append(f"{label}: relative link escapes repository: {raw}")
                elif not target.exists():
                    issues.append(f"{label}: missing relative link target: {raw}")
                elif parsed.fragment and target.suffix == ".md":
                    if target not in anchor_cache:
                        target_text = _read(root, target, issues)
                        anchor_cache[target] = _anchors(target_text) if target_text else set()
                    if unquote(parsed.fragment) not in anchor_cache[target]:
                        issues.append(f"{label}: missing Markdown anchor: {raw}")
    return issues


def _metadata(root: Path, path: Path, issues: list[str]) -> dict[str, object] | None:
    text = _read(root, path, issues)
    if text is None:
        return None
    match = re.match(r"\A---\s*\n(.*?)\n---\s*(?:\n|\Z)", text, re.DOTALL)
    if match is None:
        issues.append(f"{_display(root, path)}: missing YAML skill frontmatter")
        return None
    try:
        metadata = yaml.safe_load(match.group(1))
    except yaml.YAMLError as error:
        issues.append(f"{_display(root, path)}: invalid YAML skill frontmatter: {error}")
        return None
    if not isinstance(metadata, dict):
        issues.append(f"{_display(root, path)}: skill frontmatter must be a mapping")
        return None
    if metadata.get("name") != path.parent.name:
        issues.append(f"{_display(root, path)}: skill name must match directory {path.parent.name}")
    if not isinstance(metadata.get("description"), str) or not metadata["description"].strip():
        issues.append(f"{_display(root, path)}: skill description is required")
    return metadata


def check_catalog(root: Path) -> list[str]:
    """Compare the canonical skill directories with the root catalog."""
    issues: list[str] = []
    guide = _read(root, root / "AGENTS.md", issues)
    if guide is None:
        return issues
    catalog = set(CATALOG_REFERENCE.findall(guide))
    skills = {path.parent.name for path in (root / "ai/skills").glob("*/SKILL.md")}
    for missing in sorted(skills - catalog):
        issues.append(f"AGENTS.md: missing skill catalog entry: {missing}")
    for stale in sorted(catalog - skills):
        issues.append(f"AGENTS.md: stale skill catalog entry: {stale}")
    for name in sorted(skills):
        _metadata(root, root / "ai/skills" / name / "SKILL.md", issues)
    return issues


def check_skill_references(root: Path, files: list[Path]) -> list[str]:
    """Find stale canonical skill paths even when written as inline code."""
    issues: list[str] = []
    for source in files:
        text = _read(root, source, issues)
        if text is None:
            continue
        for number, line in _without_fences(text):
            for name in SKILL_REFERENCE.findall(line):
                if not (root / "ai/skills" / name / "SKILL.md").is_file():
                    issues.append(
                        f"{_display(root, source)}:{number}: stale canonical skill reference: {name}"
                    )
    return issues


def check_adapters(root: Path) -> list[str]:
    """Require each native skill adapter to point to its canonical peer."""
    issues: list[str] = []
    ticket_skills = {path.parent.name for path in (root / "ai/skills").glob("ticket-*/SKILL.md")}
    for vendor in (".github", ".claude"):
        required = set(ticket_skills)
        if vendor == ".github":
            required.update(("code-review", "pr-description"))
        present = {path.parent.name for path in (root / vendor / "skills").glob("*/SKILL.md")}
        for missing in sorted(required - present):
            issues.append(f"{vendor}/skills/{missing}/SKILL.md: missing required adapter")
        for path in sorted((root / vendor / "skills").glob("*/SKILL.md")):
            _metadata(root, path, issues)
            text = _read(root, path, issues)
            if text is None:
                continue
            references = set(SKILL_REFERENCE.findall(text))
            expected = path.parent.name
            if references != {expected}:
                issues.append(
                    f"{_display(root, path)}: adapter must point only to "
                    f"ai/skills/{expected}/SKILL.md"
                )
            if not (root / "ai/skills" / expected / "SKILL.md").is_file():
                issues.append(f"{_display(root, path)}: missing canonical skill target: {expected}")
    for name, markers in {
        "CLAUDE.md": ("@AGENTS.md",),
        ".continue/rules/CONTINUE.md": ("AGENTS.md", "ai/manifest.md", "ai/skills/"),
        ".github/copilot-instructions.md": ("AGENTS.md", "ai/manifest.md"),
    }.items():
        path = root / name
        text = _read(root, path, issues)
        if text is not None:
            for marker in markers:
                if marker not in text:
                    issues.append(f"{name}: missing adapter entry-point reference: {marker}")
            if name == ".continue/rules/CONTINUE.md":
                frontmatter = re.match(r"\A---\s*\n(.*?)\n---", text, re.DOTALL)
                if frontmatter is None:
                    issues.append(f"{name}: missing Continue rule frontmatter")
                else:
                    try:
                        rule = yaml.safe_load(frontmatter.group(1))
                    except yaml.YAMLError:
                        rule = None
                    if not isinstance(rule, dict) or not isinstance(rule.get("name"), str):
                        issues.append(f"{name}: Continue rule needs a name in YAML frontmatter")
    return issues


def check_templates_and_lifecycle(root: Path) -> list[str]:
    issues: list[str] = []
    for name, markers in TEMPLATE_FIELDS.items():
        path = root / "ai/templates" / name
        text = _read(root, path, issues)
        if text is not None:
            for marker in markers:
                if marker not in text:
                    issues.append(
                        f"{_display(root, path)}: missing required template field: {marker}"
                    )
    for name, markers in LIFECYCLE_MARKERS.items():
        path = root / "ai/skills" / name / "SKILL.md"
        text = _read(root, path, issues)
        if text is not None:
            for marker in markers:
                if marker.lower() not in text.lower():
                    issues.append(f"{_display(root, path)}: missing lifecycle invariant: {marker}")
    return issues


def check_ci_trigger(root: Path) -> list[str]:
    """Keep the hosted gate wired to ordinary tracked framework changes."""
    issues: list[str] = []
    path = root / ".github/workflows/ai-framework.yml"
    text = _read(root, path, issues)
    if text is None:
        return issues
    try:
        workflow = yaml.load(text, Loader=yaml.BaseLoader)
    except yaml.YAMLError as error:
        return [f"{_display(root, path)}: invalid workflow YAML: {error}"]
    if not isinstance(workflow, dict):
        return [f"{_display(root, path)}: workflow must be a YAML mapping"]
    events = workflow.get("on", {})
    pull_request = events.get("pull_request", {}) if isinstance(events, dict) else {}
    paths = pull_request.get("paths", []) if isinstance(pull_request, dict) else []
    if not isinstance(paths, list):
        paths = []
    for required in ("AGENTS.md", "ai/**", ".github/skills/**", ".claude/**", ".continue/**"):
        if required not in paths:
            issues.append(f"{_display(root, path)}: missing PR path trigger: {required}")
    jobs = workflow.get("jobs", {})
    check = jobs.get("ai-check", {}) if isinstance(jobs, dict) else {}
    steps = check.get("steps", []) if isinstance(check, dict) else []
    if not isinstance(steps, list) or not any(
        isinstance(step, dict) and step.get("run") == "make ai-check" for step in steps
    ):
        issues.append(f"{_display(root, path)}: hosted job must run make ai-check")
    return issues


def check_ticket_dir(root: Path, directory: Path) -> list[str]:
    """Validate available local readiness and completed-step evidence."""
    issues: list[str] = []
    resolved = directory.resolve()
    if not resolved.is_dir() or not resolved.is_relative_to(root / "ai/artifacts"):
        return [f"{directory}: ticket directory must exist under ai/artifacts/"]
    requirements = sorted(resolved.glob("*-requirements.md"))
    plans = sorted(resolved.glob("*-delivery-steps.md"))
    if not requirements and not plans:
        issues.append(f"{_display(root, resolved)}: no requirements or delivery plan to validate")
    for path in requirements:
        text = _read(root, path, issues)
        if text is None:
            continue
        match = READINESS.search(text)
        state = match.group(1).strip().lower() if match else ""
        valid = any(
            state == allowed
            or state.startswith(
                tuple(allowed + separator for separator in (" ", ":", ";", "—", "–"))
            )
            for allowed in READY_STATES
        )
        if not valid:
            issues.append(
                f"{_display(root, path)}: invalid readiness state; expected one of {READY_STATES}"
            )
    for plan in plans:
        text = _read(root, plan, issues)
        if text is None:
            continue
        for heading in ("## Progress", "## Steps"):
            if heading not in text:
                issues.append(f"{_display(root, plan)}: missing required plan field: {heading}")
        progress = text.split("## Progress", 1)[-1].split("## Steps", 1)[0]
        for row in COMPLETED_STEP.finditer(progress):
            reference = SUMMARY_REFERENCE.search(row.group())
            if reference is None:
                issues.append(
                    f"{_display(root, plan)}: completed step has no summary link: {row.group()}"
                )
                continue
            summary = (plan.parent / reference.group(1).split("#", 1)[0]).resolve()
            if not summary.is_relative_to(resolved) or not summary.is_file():
                issues.append(
                    f"{_display(root, plan)}: completed step summary is missing: {reference.group(1)}"
                )
                continue
            evidence = _read(root, summary, issues)
            if evidence is None:
                continue
            section = re.search(
                r"^#{1,6} Validation Evidence\s*$([\s\S]*?)(?=^#{1,6} |\Z)",
                evidence,
                re.MULTILINE,
            )
            rows = re.findall(r"^\|.*\|$", section.group(1), re.MULTILINE) if section else []
            has_evidence = len(rows) >= 3 and any(
                len(cells := [cell.strip().lower() for cell in row.strip("|").split("|")]) > 1
                and cells[1] in EVIDENCE_STATUSES
                for row in rows[2:]
            )
            if not has_evidence:
                issues.append(
                    f"{_display(root, summary)}: completed step has no validation evidence rows"
                )
    return issues


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(prog="check_ai_framework.py")
    result.add_argument(
        "--ticket-dir",
        help="validate one ignored local ticket directory in addition to tracked framework files",
    )
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    root = Path(__file__).resolve().parents[2]
    files = framework_markdown(root)
    issues = check_links(root, files)
    issues.extend(check_catalog(root))
    issues.extend(check_skill_references(root, files))
    issues.extend(check_adapters(root))
    issues.extend(check_templates_and_lifecycle(root))
    issues.extend(check_ci_trigger(root))
    ticket_dir = arguments.ticket_dir or os.environ.get("AI_CHECK_TICKET_DIR")
    if ticket_dir:
        requested = Path(ticket_dir)
        issues.extend(
            check_ticket_dir(root, requested if requested.is_absolute() else root / requested)
        )
    for issue in issues:
        print(f"ai-check: {issue}", file=sys.stderr)
    if issues:
        print(f"ai-check: {len(issues)} issue(s)", file=sys.stderr)
        return 1
    print("ai-check: framework integrity passed")
    return 0


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, _on_term)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130) from None
    except FrameworkTerminated:
        raise SystemExit(143) from None
