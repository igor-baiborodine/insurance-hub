#!/usr/bin/env bash
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly fixture="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/db-permissions/cases.json"

echo "Running candidate Product runtime-role permission cases against disposable PostgreSQL..."
mvn -B -ntp -f legacy/product-service/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=ProductDatabasePermissionsIT \
  -Dbaseline.db.permissions.fixture="${fixture}" \
  verify
