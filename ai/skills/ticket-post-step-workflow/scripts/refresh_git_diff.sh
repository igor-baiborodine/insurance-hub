#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  printf 'Usage: %s <repository-root> <intended-base-ref> <head-ref> <output-file>\n' "$0" >&2
  exit 2
fi

repo_root=$1
base_ref=$2
head_ref=$3
output_file=$4

if ! base_sha=$(git -C "$repo_root" rev-parse --verify --end-of-options "${base_ref}^{commit}" 2>/dev/null); then
  printf 'Cannot resolve intended base ref: %s\n' "$base_ref" >&2
  exit 2
fi
if ! head_sha=$(git -C "$repo_root" rev-parse --verify --end-of-options "${head_ref}^{commit}" 2>/dev/null); then
  printf 'Cannot resolve head ref: %s\n' "$head_ref" >&2
  exit 2
fi
if ! merge_base_sha=$(git -C "$repo_root" merge-base "$base_sha" "$head_sha"); then
  printf 'No merge base for %s and %s\n' "$base_ref" "$head_ref" >&2
  exit 2
fi

output_dir=$(dirname "$output_file")
temp_file=$(mktemp "${output_dir}/.git-diff.XXXXXX")
trap 'rm -f -- "$temp_file"' EXIT

{
  printf '# Committed PR patch\n'
  printf '# Intended base ref: %s\n' "$base_ref"
  printf '# Base SHA: %s\n' "$base_sha"
  printf '# Head ref: %s\n' "$head_ref"
  printf '# Head SHA: %s\n' "$head_sha"
  printf '# Merge-base SHA: %s\n' "$merge_base_sha"
  printf '# Comparison: git diff <base>...<head>\n'
  printf '# Staged, unstaged, and untracked work is excluded; inspect it separately.\n\n'
  git -C "$repo_root" diff --no-ext-diff --no-textconv "${base_sha}...${head_sha}"
} > "$temp_file"

mv -- "$temp_file" "$output_file"
printf 'Snapshot: base=%s (%s), head=%s (%s), merge-base=%s\n' \
  "$base_ref" "$base_sha" "$head_ref" "$head_sha" "$merge_base_sha"
