#!/usr/bin/env bash
set -euo pipefail

echo "Running the disposable Product listener/PostgreSQL smoke test..."
mvn -B -ntp -f legacy/product-service/pom.xml \
  -Dbaseline.smoke.exclusive=true \
  -Dskip.surefire.tests=true \
  -Dit.test=ProductBaselineSmokeIT \
  verify

echo "Running the real gateway listener and isolated Product endpoint smoke test..."
mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=GatewayBaselineSmokeIT \
  verify
