#!/usr/bin/env bash
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly fixture="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/access/cases.json"

echo "Running direct Product access cases against disposable PostgreSQL..."
mvn -B -ntp -f legacy/product-service/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=ProductAccessBaselineIT \
  -Dbaseline.access.fixture="${fixture}" \
  verify

echo "Running gateway authentication, authorization, and propagation cases..."
mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=GatewayAccessBaselineIT \
  -Dbaseline.access.fixture="${fixture}" \
  verify
