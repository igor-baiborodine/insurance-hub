#!/usr/bin/env bash

set -euo pipefail

readonly repository_root="$(cd "${BASH_SOURCE[0]%/*}/.." && pwd)"
readonly work_directory="$(mktemp -d "${TMPDIR:-/tmp}/markdown-pre-commit-test.XXXXXX")"
readonly fixture_repository="$work_directory/repository"
readonly restricted_bin="$work_directory/restricted-bin"
trap 'rm -rf -- "$work_directory"' EXIT

fail() {
	printf 'markdown-pre-commit-test: %s\n' "$*" >&2
	exit 1
}

assert_no_hook_temporary_files() {
	if find "$fixture_repository" -maxdepth 1 \
		\( -name '.markdown-toc-index.*' -o -name '.markdown-toc-staged-paths.*' \) \
		-print -quit | grep -q .; then
		fail 'the hook left temporary snapshot resources behind'
	fi
}

expect_hook_success() {
	local label=$1
	shift
	if ! "$@" >"$work_directory/result.log" 2>&1; then
		cat "$work_directory/result.log" >&2
		fail "$label unexpectedly failed"
	fi
	assert_no_hook_temporary_files
	printf 'markdown-pre-commit-test: %s passed\n' "$label"
}

expect_hook_failure() {
	local label=$1 expected=$2
	shift 2
	if "$@" >"$work_directory/result.log" 2>&1; then
		fail "$label unexpectedly passed"
	fi
	grep -Fq "$expected" "$work_directory/result.log" || {
		cat "$work_directory/result.log" >&2
		fail "$label failed without the expected reason: $expected"
	}
	assert_no_hook_temporary_files
	printf 'markdown-pre-commit-test: %s rejected\n' "$label"
}

reset_fixture() {
	git -C "$fixture_repository" reset --hard --quiet HEAD
	git -C "$fixture_repository" clean -fdq
}

blob_id() {
	git -C "$fixture_repository" rev-parse ":$1"
}

worktree_id() {
	git -C "$fixture_repository" hash-object -- "$1"
}

mkdir -p "$fixture_repository/.githooks" "$fixture_repository/scripts" "$restricted_bin"
git -C "$fixture_repository" init --quiet --initial-branch=feat/issue-156-hook-test
git -C "$fixture_repository" config user.name 'Markdown Hook Test'
git -C "$fixture_repository" config user.email 'markdown-hook@example.invalid'
git -C "$fixture_repository" config core.hooksPath .githooks
cp "$repository_root/.githooks/pre-commit" "$fixture_repository/.githooks/pre-commit"
cp "$repository_root/.githooks/pre-push" "$fixture_repository/.githooks/pre-push"
cp "$repository_root/scripts/markdown-toc.sh" "$fixture_repository/scripts/markdown-toc.sh"
[[ -x "$fixture_repository/.githooks/pre-commit" ]] || fail 'the pre-commit hook is not executable'
[[ -x "$fixture_repository/.githooks/pre-push" ]] || fail 'the pre-push hook is not executable'
[[ -x "$fixture_repository/scripts/markdown-toc.sh" ]] || fail 'the TOC checker is not executable'

printf '# Valid document\n\n## Existing section\n' >"$fixture_repository/valid.md"
(
	cd "$fixture_repository"
	./scripts/markdown-toc.sh format valid.md >/dev/null
)
cp "$fixture_repository/valid.md" "$fixture_repository/partial.md"
printf '# Removed document\n\n## Missing TOC\n' >"$fixture_repository/delete-me.md"
printf 'initial text\n' >"$fixture_repository/notes.txt"
git -C "$fixture_repository" add .
git -C "$fixture_repository" -c core.hooksPath=/dev/null commit --quiet -m 'test: initial fixture'

for command_name in env bash git mktemp rm; do
	command_path=$(command -v "$command_name") || fail "missing test prerequisite: $command_name"
	ln -s "$command_path" "$restricted_bin/$command_name"
done

printf 'changed text\n' >"$fixture_repository/notes.txt"
git -C "$fixture_repository" add notes.txt
expect_hook_success 'non-Markdown selection without npx' \
	env PATH="$restricted_bin" "$fixture_repository/.githooks/pre-commit"
reset_fixture

rm "$fixture_repository/delete-me.md"
git -C "$fixture_repository" add delete-me.md
expect_hook_success 'Markdown deletion without npx' \
	env PATH="$restricted_bin" "$fixture_repository/.githooks/pre-commit"
reset_fixture

git -C "$fixture_repository" mv valid.md 'renamed document.md'
mkdir -p "$fixture_repository/docs/nested path"
cp "$fixture_repository/partial.md" "$fixture_repository/docs/nested path/copied document.md"
cp "$fixture_repository/partial.md" "$fixture_repository/new-document.md"
git -C "$fixture_repository" add .
expect_hook_success 'valid new, copied, renamed, nested, and spaced Markdown paths' \
	"$fixture_repository/.githooks/pre-commit"
reset_fixture

printf '# Missing TOC\n\n## Section\n' >"$fixture_repository/missing.md"
cp "$fixture_repository/valid.md" "$fixture_repository/stale.md"
printf '\n## Unlisted section\n' >>"$fixture_repository/stale.md"
git -C "$fixture_repository" add missing.md stale.md
missing_index_before=$(blob_id missing.md)
stale_index_before=$(blob_id stale.md)
missing_worktree_before=$(worktree_id missing.md)
stale_worktree_before=$(worktree_id stale.md)
expect_hook_failure 'missing TOC' 'stale or missing table of contents' \
	"$fixture_repository/.githooks/pre-commit"
grep -Fq 'missing.md' "$work_directory/result.log" || fail 'missing TOC path was not reported'
grep -Fq 'stale.md' "$work_directory/result.log" || fail 'stale TOC path was not reported'
grep -Fq './scripts/markdown-toc.sh format <file>...' "$work_directory/result.log" ||
	fail 'failure did not explain how to format the affected files'
grep -Fq 'then stage the formatted content' "$work_directory/result.log" ||
	fail 'failure did not explain that formatted content must be staged'
[[ $(blob_id missing.md) == "$missing_index_before" ]] || fail 'failure changed the missing TOC index blob'
[[ $(blob_id stale.md) == "$stale_index_before" ]] || fail 'failure changed the stale TOC index blob'
[[ $(worktree_id missing.md) == "$missing_worktree_before" ]] || fail 'failure changed the missing TOC worktree file'
[[ $(worktree_id stale.md) == "$stale_worktree_before" ]] || fail 'failure changed the stale TOC worktree file'
reset_fixture

printf '\n## Staged section\n' >>"$fixture_repository/partial.md"
(
	cd "$fixture_repository"
	./scripts/markdown-toc.sh format partial.md >/dev/null
)
git -C "$fixture_repository" add partial.md
printf '\n## Worktree-only section\n' >>"$fixture_repository/partial.md"
index_before=$(blob_id partial.md)
worktree_before=$(worktree_id partial.md)
expect_hook_success 'valid staged content with stale working-tree content' \
	"$fixture_repository/.githooks/pre-commit"
[[ $(blob_id partial.md) == "$index_before" ]] || fail 'success changed the partial-stage index blob'
[[ $(worktree_id partial.md) == "$worktree_before" ]] || fail 'success changed the partial-stage worktree file'
reset_fixture

printf '\n## Staged-only section\n' >>"$fixture_repository/partial.md"
git -C "$fixture_repository" add partial.md
(
	cd "$fixture_repository"
	./scripts/markdown-toc.sh format partial.md >/dev/null
)
index_before=$(blob_id partial.md)
worktree_before=$(worktree_id partial.md)
expect_hook_failure 'stale staged content with valid working-tree content' \
	'stale or missing table of contents' "$fixture_repository/.githooks/pre-commit"
[[ $(blob_id partial.md) == "$index_before" ]] || fail 'failure changed the staged partial blob'
[[ $(worktree_id partial.md) == "$worktree_before" ]] || fail 'failure changed the valid worktree file'
reset_fixture

printf '# Requires checker\n\n## Section\n' >"$fixture_repository/requires-checker.md"
git -C "$fixture_repository" add requires-checker.md
expect_hook_failure 'missing npx prerequisite' 'npx is required' \
	env PATH="$restricted_bin" "$fixture_repository/.githooks/pre-commit"
reset_fixture

expect_hook_success 'pre-push hook coexistence' "$fixture_repository/.githooks/pre-push"
[[ $(git -C "$fixture_repository" config --get core.hooksPath) == '.githooks' ]] ||
	fail 'the disposable repository did not retain the documented hooks path'

printf 'markdown-pre-commit-test: all staged-snapshot hook checks passed\n'
