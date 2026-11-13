"""Focused coverage smoke for the unit and migrator profile boundary."""

import pytest
from merge_coverage import merge_profiles


def test_merge_preserves_unit_and_migrator_hits_without_duplicate_blocks(tmp_path):
    # given
    unit = tmp_path / "unit.txt"
    unit.write_text(
        "mode: atomic\nservice/a.go:1.1,2.2 2 0\nservice/b.go:1.1,2.2 1 3\n"
        "service/empty.go:1.1,1.1 0 0\n"
    )
    integration = tmp_path / "integration.txt"
    integration.write_text("mode: atomic\nservice/a.go:1.1,2.2 2 4\nservice/c.go:1.1,2.2 1 0\n")
    # when
    actual = merge_profiles([unit, integration])
    # then
    assert actual == (
        "mode: atomic\nservice/a.go:1.1,2.2 2 4\n"
        "service/b.go:1.1,2.2 1 3\nservice/c.go:1.1,2.2 1 0\n"
        "service/empty.go:1.1,1.1 0 0\n"
    )
    assert "service/a.go:1.1,2.2 2 0" in unit.read_text()


@pytest.mark.parametrize("invalid", ["mode: count\na.go:1.1,2.2 1 1\n", "mode: atomic\n"])
def test_merge_rejects_incompatible_or_empty_profiles(tmp_path, invalid):
    # given
    profile = tmp_path / "invalid.txt"
    profile.write_text(invalid)
    # when / then
    with pytest.raises(ValueError):
        merge_profiles([profile])
