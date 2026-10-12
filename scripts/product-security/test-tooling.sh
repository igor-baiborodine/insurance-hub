#!/usr/bin/env bash
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/product-security-tooling.XXXXXX")
trap 'rm -rf -- "$work_dir"' EXIT

fail() {
  printf 'product-security-test-tooling: %s\n' "$*" >&2
  exit 1
}

expect_failure() {
  local label=$1 expected=$2
  shift 2
  if "$@" >"$work_dir/result.log" 2>&1; then
    fail "$label unexpectedly passed"
  fi
  grep -Fq "$expected" "$work_dir/result.log" || \
    fail "$label failed without its expected sanitized reason"
  printf 'product-security-test-tooling: %s rejected\n' "$label"
}

cases_file="$repo_root/legacy/product-service/src/test/resources/product-read-baseline/access/cases.json"
python3 scripts/product-security/check-cases.py "$cases_file"
python3 - "$cases_file" "$work_dir" <<'PY'
import json
import pathlib
import sys
import xml.etree.ElementTree as ET

source = json.loads(pathlib.Path(sys.argv[1]).read_text())
directory = pathlib.Path(sys.argv[2])
missing = json.loads(json.dumps(source))
missing['cases'] = [case for case in missing['cases']
                    if case['id'] != 'ACCESS-GATEWAY-LIST-EXPIRED-001']
(directory / 'missing.json').write_text(json.dumps(missing))
duplicate = json.loads(json.dumps(source))
duplicate['cases'][1] = duplicate['cases'][0]
(directory / 'duplicate.json').write_text(json.dumps(duplicate))

methods = (
    'happyPath', 'rejectedCredentialsStopAtGateway',
    'givenAcceptedClaimVariants_thenCatalogResultsPreserved',
    'givenTestOwnedSentinel_thenObserverDetectsIdentityAndPreservesTrace',
)
for variant in ('valid', 'missing', 'skipped'):
    names = methods if variant != 'missing' else methods[:-1]
    suite = ET.Element('testsuite', name='example.GatewayProductSecurityIT',
                       tests=str(len(names)), errors='0', failures='0',
                       skipped='1' if variant == 'skipped' else '0')
    for index, name in enumerate(names):
        case = ET.SubElement(suite, 'testcase', name=name)
        if variant == 'skipped' and index == 0:
            ET.SubElement(case, 'skipped')
    ET.ElementTree(suite).write(directory / f'java-{variant}.xml')

direct_ids = [case['id'] for case in source['cases'] if case['target'] == 'product']
prefixes = (
    'TestProductAccess/accepted_HTTP_credential_matrix_remains_anonymous/',
    'TestProductAccess/generated_gRPC_client_ignores_identity_metadata/',
)
for variant in ('valid', 'missing', 'skipped'):
    records = [{'Action': 'run', 'Test': 'TestProductAccess'}]
    for prefix in prefixes:
        for index, identity in enumerate(direct_ids):
            if variant == 'missing' and prefix == prefixes[1] and index == 0:
                continue
            name = prefix + identity
            records.extend(({'Action': 'run', 'Test': name},
                            {'Action': 'skip' if variant == 'skipped' and index == 0
                             and prefix == prefixes[0] else 'pass', 'Test': name}))
    records.append({'Action': 'pass', 'Test': 'TestProductAccess'})
    (directory / f'go-{variant}.json').write_text(
        ''.join(json.dumps(record) + '\n' for record in records))
PY

expect_failure omitted-case SECURITY_CASE_SET_MISMATCH \
  python3 scripts/product-security/check-cases.py "$work_dir/missing.json"
expect_failure duplicate-case SECURITY_CASE_SET_MISMATCH \
  python3 scripts/product-security/check-cases.py "$work_dir/duplicate.json"
python3 scripts/product-security/check-java-results.py "$work_dir/java-valid.xml"
expect_failure omitted-java-method SECURITY_JAVA_RESULT_MISMATCH \
  python3 scripts/product-security/check-java-results.py "$work_dir/java-missing.xml"
expect_failure skipped-java-method SECURITY_JAVA_RESULT_MISMATCH \
  python3 scripts/product-security/check-java-results.py "$work_dir/java-skipped.xml"
python3 services/product-service/scripts/check-access-results.py "$work_dir/go-valid.json"
expect_failure omitted-go-case PRODUCT_ACCESS_RESULT_MISMATCH \
  python3 services/product-service/scripts/check-access-results.py "$work_dir/go-missing.json"
expect_failure skipped-go-case PRODUCT_ACCESS_RESULT_MISMATCH \
  python3 services/product-service/scripts/check-access-results.py "$work_dir/go-skipped.json"

mkdir "$work_dir/bin"
ln -s "$(command -v dirname)" "$work_dir/bin/dirname"
expect_failure missing-tool 'missing prerequisite: java' \
  env PATH="$work_dir/bin" /bin/bash scripts/product-security/test.sh
expect_failure failed-docker 'Docker daemon is unavailable' \
  env DOCKER_HOST="unix://$work_dir/missing-docker.sock" \
  bash scripts/product-security/test.sh
expect_failure external-database 'PRODUCT_DATABASE_URL must be unset' \
  env PRODUCT_DATABASE_URL=controlled bash scripts/product-security/test.sh
for selection in '' unknown; do
  if [[ -z "$selection" ]]; then
    label=empty-selection
    expected='INTEGRATION_SUITE must not be empty'
  else
    label=unknown-selection
    expected='unknown INTEGRATION_SUITE: unknown'
  fi
  expect_failure "$label" "$expected" env GO=go DOCKER=docker \
    POSTGRES_IMAGE=controlled INTEGRATION_SUITE="$selection" FIXTURE_SET=qa \
    bash services/product-service/scripts/test-integration.sh
done
expect_failure omitted-root-case SECURITY_CASE_SET_MISMATCH \
  env PRODUCT_SECURITY_CASES_FILE="$work_dir/missing.json" \
  bash scripts/product-security/test.sh
expect_failure duplicate-root-case SECURITY_CASE_SET_MISMATCH \
  env PRODUCT_SECURITY_CASES_FILE="$work_dir/duplicate.json" \
  bash scripts/product-security/test.sh

expect_failure token-clock-drift 'bounded token clock drift' \
  env PRODUCT_SECURITY_TEST_CLOCK_OFFSET_SECONDS=300 \
  bash scripts/product-security/test.sh
expect_failure java-timeout 'Java security suite timed out' \
  env PRODUCT_SECURITY_TEST_JAVA_TIMEOUT=1s \
  bash scripts/product-security/test.sh
expect_failure backend-loss 'healthy gateway-to-Go smoke test failed' \
  env PRODUCT_SECURITY_TEST_INJECT_FAILURE=backend_down \
  bash scripts/product-security/test.sh

private_temp="$work_dir/private"
mkdir "$private_temp"
expect_failure partial-startup 'injected partial startup failure after Go readiness' \
  env PRODUCT_SECURITY_TEST_INJECT_FAILURE=after_helper_ready TMPDIR="$private_temp" \
  bash scripts/product-security/test.sh
[[ -z $(find "$private_temp" -mindepth 1 -print -quit) ]] || \
  fail 'partial startup left temporary credential files'

printf 'product-security-test-tooling: controlled gate failures and cleanup passed\n'
