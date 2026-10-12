#!/usr/bin/env bash
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

fail() {
  printf 'product-security-test: %s\n' "$*" >&2
  exit 1
}

for tool in java mvn go docker jq python3 timeout; do
  command -v "$tool" >/dev/null 2>&1 || fail "missing prerequisite: $tool"
done
mvn -version | grep -q 'Java version: 14\.' || fail 'Java 14 is required'
docker info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'
[[ ! ${PRODUCT_DATABASE_URL+x} ]] || fail 'PRODUCT_DATABASE_URL must be unset'

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/product-security.XXXXXX")
chmod 700 "$work_dir"
helper_pid=
cleanup() {
  if [[ -n "$helper_pid" ]] && kill -0 "$helper_pid" 2>/dev/null; then
    touch "$work_dir/stop"
    for _ in {1..100}; do
      kill -0 "$helper_pid" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$helper_pid" 2>/dev/null; then
      kill -TERM "$helper_pid" 2>/dev/null || true
      for _ in {1..50}; do
        kill -0 "$helper_pid" 2>/dev/null || break
        sleep 0.1
      done
    fi
    if kill -0 "$helper_pid" 2>/dev/null; then
      kill -KILL "$helper_pid" 2>/dev/null || true
    fi
    wait "$helper_pid" 2>/dev/null || true
  fi
  rm -rf -- "$work_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

endpoint_file="$work_dir/endpoint.json"
helper_log="$work_dir/helper.log"
java_log="$work_dir/java.log"

timeout 180s make -C services/product-service build-security-harness \
  SECURITY_HARNESS_OUTPUT="$work_dir/security-harness"
"$work_dir/security-harness" -endpoint-file "$endpoint_file" -stop-file "$work_dir/stop" \
  >"$helper_log" 2>&1 &
helper_pid=$!

for _ in {1..1200}; do
  [[ -s "$endpoint_file" ]] && break
  if ! kill -0 "$helper_pid" 2>/dev/null; then
    tail -n 25 "$helper_log" >&2
    fail 'Go harness exited before publishing its endpoint'
  fi
  sleep 0.1
done
[[ -s "$endpoint_file" ]] || fail 'Go harness readiness exceeded 120 seconds'
jq -e --argjson pid "$helper_pid" '
  .pid == $pid and .fixture == "DATA-QA-001" and .rowCount == 4
  and .reader == "go_product_reader" and (.nonce | test("^[0-9a-f]{32}$"))
  and (.containerId | test("^[0-9a-f]{64}$"))
  and ([.http,.grpc,.health] | all(test("^127[.]0[.]0[.]1:[0-9]+$")))
' "$endpoint_file" >/dev/null || fail 'invalid Go endpoint handshake'

qa_access="$repo_root/legacy/product-service/src/test/resources/product-read-baseline/access/qa.json"
timeout 150s mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=GatewayProductSecurityIT#happyPath \
  -Dproduct.security.endpoint.file="$endpoint_file" \
  -Dproduct.security.qa.access.file="$qa_access" \
  verify >"$java_log" 2>&1 || {
    tail -n 65 "$java_log" >&2
    fail 'healthy gateway-to-Go smoke test failed'
  }
grep -q 'Tests run: 1, Failures: 0, Errors: 0, Skipped: 0' "$java_log" || \
  fail 'healthy gateway smoke test did not execute exactly one passing test'

touch "$work_dir/stop"
for _ in {1..350}; do
  kill -0 "$helper_pid" 2>/dev/null || break
  sleep 0.1
done
if kill -0 "$helper_pid" 2>/dev/null; then
  fail 'Go harness did not stop within 35 seconds'
fi
wait "$helper_pid" || fail 'Go harness shutdown failed'
helper_pid=
container_id=$(jq -r '.containerId' "$endpoint_file")
if docker inspect "$container_id" >/dev/null 2>&1; then
  fail 'disposable PostgreSQL container was not removed'
fi
python3 - "$endpoint_file" <<'PY' || fail 'Go listener remained open after shutdown'
import json
import socket
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    record = json.load(source)
for name in ("http", "grpc", "health"):
    host, port = record[name].rsplit(":", 1)
    with socket.socket() as connection:
        connection.settimeout(0.3)
        if connection.connect_ex((host, int(port))) == 0:
            raise SystemExit(f"{name} listener still accepts connections")
PY

if timeout 90s mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=GatewayProductSecurityIT#happyPath \
  -Dproduct.security.endpoint.file="$endpoint_file" \
  -Dproduct.security.qa.access.file="$qa_access" \
  verify >"$java_log" 2>&1; then
  fail 'healthy gateway assertion passed after Go stopped'
fi
grep -q 'PRODUCT_SECURITY_SMOKE_LIST_START' "$java_log" || \
  fail 'Go-absent run did not reach the gateway smoke request'
grep -Eq 'Tests run: 1, Failures: [1-9]|Tests run: 1, Failures: 0, Errors: [1-9]' "$java_log" || \
  fail 'Go-absent run failed outside the expected test assertion'

printf 'product-security-test: real gateway list/get and anonymous gRPC passed; Go-absent rerun failed; listeners and container released\n'
