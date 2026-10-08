#!/usr/bin/env bash
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly fixture="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/failures/cases.json"

echo "Running Product failure and boundary cases against disposable PostgreSQL..."
mvn -B -ntp -f legacy/product-service/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=ProductFailureBaselineIT \
  verify

echo "Running gateway retry and fallback cases against an isolated Product endpoint..."
mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=GatewayFailureBaselineIT \
  -Dbaseline.failure.fixture="${fixture}" \
  verify
