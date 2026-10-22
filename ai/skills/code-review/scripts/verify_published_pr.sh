#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 7 ]]; then
  printf 'Usage: %s <repository-root> <base-remote> <base-ref> <base-sha> <head-remote> <head-ref> <head-sha>\n' "$0" >&2
  exit 2
fi

repo_root=$1
base_remote=$2
base_ref=$3
expected_base_sha=$4
head_remote=$5
head_ref=$6
expected_head_sha=$7

for expected_sha in "$expected_base_sha" "$expected_head_sha"; do
  if [[ ! $expected_sha =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]]; then
    printf 'Expected PR revisions must be full lowercase commit SHAs\n' >&2
    exit 2
  fi
done
for remote_ref in "$base_ref" "$head_ref"; do
  if [[ $remote_ref != refs/* ]] || ! git -C "$repo_root" check-ref-format "$remote_ref" >/dev/null 2>&1; then
    printf 'Published PR refs must be valid, fully qualified refs/* names\n' >&2
    exit 2
  fi
done

fetch_revision() {
  local label=$1
  local remote=$2
  local remote_ref=$3
  local expected_sha=$4
  local fetched_sha

  if ! GIT_TERMINAL_PROMPT=0 git -C "$repo_root" fetch --quiet --no-tags -- "$remote" "$remote_ref" >/dev/null 2>&1; then
    printf 'Cannot fetch published PR %s ref: %s\n' "$label" "$remote_ref" >&2
    return 1
  fi
  if ! fetched_sha=$(git -C "$repo_root" rev-parse --verify 'FETCH_HEAD^{commit}' 2>/dev/null); then
    printf 'Fetched published PR %s is not a commit: %s\n' "$label" "$remote_ref" >&2
    return 1
  fi
  if [[ $fetched_sha != "$expected_sha" ]]; then
    printf 'Published PR %s revision mismatch: reported=%s fetched=%s\n' \
      "$label" "$expected_sha" "$fetched_sha" >&2
    return 1
  fi
  printf '%s\n' "$fetched_sha"
}

verified_base_sha=$(fetch_revision base "$base_remote" "$base_ref" "$expected_base_sha")
verified_head_sha=$(fetch_revision head "$head_remote" "$head_ref" "$expected_head_sha")
if ! merge_base_sha=$(git -C "$repo_root" merge-base "$verified_base_sha" "$verified_head_sha"); then
  printf 'Published PR base and head have no merge base\n' >&2
  exit 1
fi

printf 'published-pr: base=%s head=%s merge-base=%s\n' \
  "$verified_base_sha" "$verified_head_sha" "$merge_base_sha"
