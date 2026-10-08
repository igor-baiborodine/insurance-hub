#!/usr/bin/env bash
set -euo pipefail

echo "Running Product happy-path listener tests against disposable PostgreSQL..."
mvn -B -ntp -f legacy/product-service/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=ProductsControllerIT \
  verify

echo "Running gateway happy-path listener tests against the isolated Product endpoint..."
mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=GatewayBaselineSmokeIT \
  verify
