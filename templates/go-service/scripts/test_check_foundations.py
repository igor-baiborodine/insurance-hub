"""Independent version drift and missing-reference fixtures."""

from pathlib import Path

import pytest

import check_foundations


@pytest.fixture
def modules(tmp_path: Path) -> tuple[Path, Path]:
    paths = (tmp_path / "scaffold", tmp_path / "reference")
    for path in paths:
        path.mkdir()
        (path / "Makefile").write_text(
            "\n".join(f"{pin} := 1.27.3" for pin in check_foundations.SHARED_PINS) + "\n"
        )
        (path / "go.mod").write_text(
            "module example.test/service\n\ngo 1.27.3\n\nrequire (\n\texample.test/lib v1.0.0\n)\n"
        )
    return paths


def test_matching_versions_allow_service_specific_dependencies(modules: tuple[Path, Path]) -> None:
    # given
    scaffold, reference = modules
    with (reference / "go.mod").open("a") as output:
        output.write("\nrequire (\n\texample.test/database v2.0.0\n)\n")

    # when / then
    check_foundations.check(scaffold, reference)


@pytest.mark.parametrize(
    "filename,before,after",
    [
        ("Makefile", "GO_VERSION := 1.27.3", "GO_VERSION := 1.27.2"),
        ("Makefile", "BUF_VERSION := 1.27.3", "BUF_VERSION := 0.1.0"),
        ("Makefile", "BUF_VERSION := 1.27.3", "REMOVED := 1.27.3"),
        ("go.mod", "go 1.27.3", "go 1.27.2"),
        ("go.mod", "example.test/lib v1.0.0", "example.test/lib v1.1.0"),
    ],
)
def test_rejects_controlled_drift(
    modules: tuple[Path, Path], filename: str, before: str, after: str
) -> None:
    # given
    scaffold, reference = modules
    path = scaffold / filename
    path.write_text(path.read_text().replace(before, after))

    # when / then
    with pytest.raises(ValueError):
        check_foundations.check(scaffold, reference)


def test_missing_reference_fails_closed(
    modules: tuple[Path, Path], capsys: pytest.CaptureFixture
) -> None:
    # given
    scaffold, reference = modules

    # when
    result = check_foundations.main(
        ["--scaffold", str(scaffold), "--reference", str(reference / "absent")]
    )

    # then
    assert result == 1
    assert "check-foundations:" in capsys.readouterr().err
