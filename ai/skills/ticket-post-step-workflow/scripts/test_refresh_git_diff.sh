#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fixture_dir=$(mktemp -d)
trap 'rm -rf -- "$fixture_dir"' EXIT
repo="$fixture_dir/repo"
snapshot="$fixture_dir/snapshot.txt"

git init -q -b main "$repo"
git -C "$repo" config user.name 'Fixture Author'
git -C "$repo" config user.email 'fixture@example.invalid'
printf 'root\n' > "$repo/root.txt"
git -C "$repo" add root.txt
git -C "$repo" commit -qm root
root_sha=$(git -C "$repo" rev-parse HEAD)

git -C "$repo" switch -qc feature
printf 'feature\n' > "$repo/feature.txt"
git -C "$repo" add feature.txt
git -C "$repo" commit -qm feature
git -C "$repo" switch -q main
printf 'base-only\n' > "$repo/base.txt"
git -C "$repo" add base.txt
git -C "$repo" commit -qm 'advance main'
main_sha=$(git -C "$repo" rev-parse HEAD)

"$script_dir/refresh_git_diff.sh" "$repo" refs/heads/main refs/heads/feature "$snapshot" >/dev/null
contents=$(cat "$snapshot")
[[ "$contents" == *"# Base SHA: $main_sha"* ]]
[[ "$contents" == *"# Merge-base SHA: $root_sha"* ]]
[[ "$contents" == *'+feature'* ]]
[[ "$contents" != *'-base-only'* ]]

git -C "$repo" switch -qc release
printf 'release\n' > "$repo/release.txt"
git -C "$repo" add release.txt
git -C "$repo" commit -qm release
git -C "$repo" switch -qc release-feature
printf 'release feature\n' > "$repo/release-feature.txt"
git -C "$repo" add release-feature.txt
git -C "$repo" commit -qm 'release feature'
"$script_dir/refresh_git_diff.sh" "$repo" refs/heads/release refs/heads/release-feature "$snapshot" >/dev/null
contents=$(cat "$snapshot")
[[ "$contents" == *'# Intended base ref: refs/heads/release'* ]]
[[ "$contents" == *'+release feature'* ]]
[[ "$contents" != *'+++ b/release.txt'* ]]

git -C "$repo" switch -qc stack-one
printf 'earlier stack\n' > "$repo/earlier.txt"
git -C "$repo" add earlier.txt
git -C "$repo" commit -qm 'earlier stack'
git -C "$repo" switch -qc stack-two
printf 'current stack\n' > "$repo/current.txt"
git -C "$repo" add current.txt
git -C "$repo" commit -qm 'current stack'
"$script_dir/refresh_git_diff.sh" "$repo" refs/heads/stack-one refs/heads/stack-two "$snapshot" >/dev/null
contents=$(cat "$snapshot")
[[ "$contents" == *'+current stack'* ]]
[[ "$contents" != *'+++ b/earlier.txt'* ]]

printf 'keep previous snapshot\n' > "$snapshot"
if "$script_dir/refresh_git_diff.sh" "$repo" refs/heads/missing refs/heads/stack-two "$snapshot" >"$fixture_dir/out" 2>"$fixture_dir/error"; then
  printf 'Missing base unexpectedly succeeded\n' >&2
  exit 1
fi
[[ $(cat "$snapshot") == 'keep previous snapshot' ]]
[[ $(cat "$fixture_dir/error") == *'Cannot resolve intended base ref: refs/heads/missing'* ]]

printf 'staged work\n' > "$repo/staged.txt"
git -C "$repo" add staged.txt
printf 'unstaged work\n' >> "$repo/current.txt"
printf 'untracked work\n' > "$repo/untracked.txt"
"$script_dir/refresh_git_diff.sh" "$repo" refs/heads/stack-one refs/heads/stack-two "$snapshot" >/dev/null
patch=$(sed -n '/^diff --git /,$p' "$snapshot")
[[ "$patch" == *'+current stack'* ]]
[[ "$patch" != *'staged work'* ]]
[[ "$patch" != *'unstaged work'* ]]
[[ "$patch" != *'untracked work'* ]]
[[ $(git -C "$repo" status --short | wc -l) -eq 3 ]]

printf 'refresh_git_diff fixtures passed: advanced base, non-main base, stacked PR, missing base, dirty tree\n'
