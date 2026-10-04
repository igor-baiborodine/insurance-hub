#!/usr/bin/env bash

set -euo pipefail

repository=${1:-.}

if ! repository_root=$(git -C "${repository}" rev-parse --show-toplevel 2>/dev/null); then
  echo "branch-sync: '${repository}' is not inside a Git repository" >&2
  exit 1
fi

if ! branch=$(git -C "${repository_root}" symbolic-ref --quiet --short HEAD); then
  echo "branch-sync: detached HEAD has no branch to compare with a remote upstream" >&2
  exit 1
fi

if ! remote=$(git -C "${repository_root}" config --get "branch.${branch}.remote") ||
  [[ -z ${remote} || ${remote} == "." ]]; then
  echo "branch-sync: branch '${branch}' has no remote upstream" >&2
  exit 1
fi

if ! merge_ref=$(git -C "${repository_root}" config --get "branch.${branch}.merge") ||
  [[ -z ${merge_ref} ]]; then
  echo "branch-sync: branch '${branch}' has no upstream merge ref" >&2
  exit 1
fi

upstream="${remote}/${merge_ref#refs/heads/}"
if ! git -C "${repository_root}" fetch --quiet --no-tags "${remote}" "${merge_ref}"; then
  echo "branch-sync: could not fetch upstream '${upstream}'" >&2
  exit 1
fi

local_commit=$(git -C "${repository_root}" rev-parse HEAD)
remote_commit=$(git -C "${repository_root}" rev-parse FETCH_HEAD)
read -r ahead behind < <(
  git -C "${repository_root}" rev-list --left-right --count \
    "${local_commit}...${remote_commit}"
)

if [[ ${local_commit} != "${remote_commit}" ]]; then
  echo "branch-sync: branch '${branch}' is not synchronized with '${upstream}'" >&2
  echo "branch-sync: local=${local_commit} fetched=${remote_commit} ahead=${ahead} behind=${behind}" >&2
  exit 1
fi

echo "branch-sync: branch='${branch}' upstream='${upstream}' commit='${local_commit}' ahead=0 behind=0"
