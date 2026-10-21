#!/usr/bin/env bash
set -euo pipefail

if (( $# < 3 )); then
  echo "usage: stream-test-log.sh <log-path> <command> <args...>" >&2
  exit 2
fi

log_path=$1
shift
"$@" 2>&1 | tee -- "$log_path" >/dev/null
