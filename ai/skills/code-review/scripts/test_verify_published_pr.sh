#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
snapshot_script="$script_dir/../../ticket-post-step-workflow/scripts/refresh_git_diff.sh"
fixture_dir=$(mktemp -d)
trap 'rm -rf -- "$fixture_dir"' EXIT
remote="$fixture_dir/remote.git"
repo="$fixture_dir/repo"
snapshot="$fixture_dir/snapshot.txt"

git init -q --bare "$remote"
git init -q -b main "$repo"
git -C "$repo" config user.name 'Review Fixture'
git -C "$repo" config user.email 'review-fixture@example.invalid'
git -C "$repo" remote add origin "$remote"
printf 'base\n' > "$repo/base.txt"
git -C "$repo" add base.txt
git -C "$repo" commit -qm base
base_sha=$(git -C "$repo" rev-parse HEAD)
git -C "$repo" push -q origin main

git -C "$repo" switch -qc published-feature
printf 'published\n' > "$repo/published.txt"
git -C "$repo" add published.txt
git -C "$repo" commit -qm published
head_sha=$(git -C "$repo" rev-parse HEAD)
git -C "$repo" push -q origin published-feature
remote_head_before=$(git --git-dir="$remote" rev-parse refs/heads/published-feature)

git -C "$repo" switch -qc local-only main
printf 'local commit\n' > "$repo/local.txt"
git -C "$repo" add local.txt
git -C "$repo" commit -qm local
[[ -z $(git -C "$repo" config --get branch.local-only.remote || true) ]]
"$snapshot_script" "$repo" refs/heads/main refs/heads/local-only "$snapshot" >/dev/null
local_patch=$(sed -n '/^diff --git /,$p' "$snapshot")
[[ $local_patch == *'+local commit'* ]]
[[ $local_patch != *'+published'* ]]

printf 'staged\n' > "$repo/staged.txt"
git -C "$repo" add staged.txt
printf 'unstaged\n' >> "$repo/local.txt"
printf 'untracked\n' > "$repo/untracked.txt"
result=$("$script_dir/verify_published_pr.sh" "$repo" origin refs/heads/main "$base_sha" origin refs/heads/published-feature "$head_sha")
[[ $result == "published-pr: base=$base_sha head=$head_sha merge-base=$base_sha" ]]
"$snapshot_script" "$repo" "$base_sha" "$head_sha" "$snapshot" >/dev/null
published_patch=$(sed -n '/^diff --git /,$p' "$snapshot")
[[ $published_patch == *'+published'* ]]
[[ $published_patch != *'+local commit'* ]]
[[ $published_patch != *'+staged'* ]]
[[ $published_patch != *'+unstaged'* ]]
[[ $published_patch != *'+untracked'* ]]
[[ $(git --git-dir="$remote" rev-parse refs/heads/published-feature) == "$remote_head_before" ]]

if "$script_dir/verify_published_pr.sh" "$repo" origin refs/heads/main "$base_sha" origin refs/heads/published-feature "$base_sha" >"$fixture_dir/out" 2>"$fixture_dir/error"; then
  printf 'Stale published PR identity unexpectedly succeeded\n' >&2
  exit 1
fi
[[ $(cat "$fixture_dir/error") == *'Published PR head revision mismatch'* ]]

if "$script_dir/verify_published_pr.sh" "$repo" origin refs/heads/main "$base_sha" origin refs/heads/missing "$head_sha" >"$fixture_dir/out" 2>"$fixture_dir/error"; then
  printf 'Missing published PR ref unexpectedly succeeded\n' >&2
  exit 1
fi
[[ $(cat "$fixture_dir/error") == *'Cannot fetch published PR head ref'* ]]

if "$script_dir/verify_published_pr.sh" "$repo" origin refs/heads/main "$base_sha" origin refs/heads/published-feature >"$fixture_dir/out" 2>"$fixture_dir/error"; then
  printf 'Missing reported PR SHA unexpectedly succeeded\n' >&2
  exit 1
fi
[[ $(cat "$fixture_dir/error") == *'Usage:'* ]]

if "$script_dir/verify_published_pr.sh" "$repo" origin 'refs/heads/main:refs/heads/local-only' "$base_sha" origin refs/heads/published-feature "$head_sha" >"$fixture_dir/out" 2>"$fixture_dir/error"; then
  printf 'Mutating fetch refspec unexpectedly succeeded\n' >&2
  exit 1
fi
[[ $(cat "$fixture_dir/error") == *'valid, fully qualified refs/* names'* ]]

printf 'review mode fixtures passed: unpushed local branch, exact published refs, stale/missing identity, dirty worktree\n'
