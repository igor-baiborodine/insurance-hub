from __future__ import annotations

from pathlib import Path

import pytest

from validate_selection import SelectionError, validate_selection


def create_repository(tmp_path: Path) -> tuple[Path, Path]:
    root = tmp_path / "repository"
    nested = root / "owner" / "tests"
    nested.mkdir(parents=True)
    selected_file = nested / "test_tool.py"
    selected_file.write_text("VALUE = 1\n", encoding="utf-8")
    return root, selected_file


@pytest.mark.parametrize(
    ("selection", "expected_message"),
    [
        ("../outside.py", "prohibited path component"),
        ("owner//tests/test_tool.py", "prohibited path component"),
        ("./owner/tests/test_tool.py", "prohibited path component"),
        ("owner/tests/missing.py", "does not exist"),
        ("owner/tests/test_tool.txt", "must end in .py"),
        ("owner/tests/tool;touch.py", "unsupported characters"),
    ],
)
def test_file_selection_rejects_unsafe_or_invalid_paths(
    tmp_path: Path,
    selection: str,
    expected_message: str,
) -> None:
    # given
    root, _selected_file = create_repository(tmp_path)

    # when / then
    with pytest.raises(SelectionError, match=expected_message):
        validate_selection(root, selection, "files")


def test_file_selection_rejects_absolute_path(tmp_path: Path) -> None:
    # given
    root, selected_file = create_repository(tmp_path)

    # when / then
    with pytest.raises(SelectionError, match="repository-relative"):
        validate_selection(root, str(selected_file), "files")


def test_selection_rejects_symlink_escape(tmp_path: Path) -> None:
    # given
    root, _selected_file = create_repository(tmp_path)
    outside = tmp_path / "outside.py"
    outside.write_text("VALUE = 1\n", encoding="utf-8")
    (root / "escaped.py").symlink_to(outside)

    # when / then
    with pytest.raises(SelectionError, match="outside repository"):
        validate_selection(root, "escaped.py", "files")


def test_file_selection_accepts_multiple_nested_python_files(tmp_path: Path) -> None:
    # given
    root, selected_file = create_repository(tmp_path)
    second_file = root / "owner" / "tool.py"
    second_file.write_text("VALUE = 2\n", encoding="utf-8")
    selection = f"{selected_file.relative_to(root)} {second_file.relative_to(root)}"

    # when
    validated = validate_selection(root, selection, "files")

    # then
    assert validated == ["owner/tests/test_tool.py", "owner/tool.py"]


def test_acceptance_selection_accepts_one_existing_nested_path(tmp_path: Path) -> None:
    # given
    root, _selected_file = create_repository(tmp_path)

    # when
    validated = validate_selection(root, "owner/tests", "acceptance")

    # then
    assert validated == ["owner/tests"]


def test_acceptance_selection_rejects_multiple_paths(tmp_path: Path) -> None:
    # given
    root, _selected_file = create_repository(tmp_path)

    # when / then
    with pytest.raises(SelectionError, match="exactly one path"):
        validate_selection(root, "owner owner/tests", "acceptance")
