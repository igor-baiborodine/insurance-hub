#!/usr/bin/env bash
set -euo pipefail

echo "Running isolated Product data-semantics cases against disposable PostgreSQL..."
mvn -B -ntp -f legacy/product-service/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=ProductDataEdgesIT \
  verify
