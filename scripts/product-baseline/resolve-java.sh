#!/usr/bin/env bash
set -euo pipefail

echo "Resolving and compiling the Product API contract..."
mvn -B -ntp -f legacy/product-service-api/pom.xml -DskipTests package

echo "Resolving and compiling Product service test dependencies..."
mvn -B -ntp -f legacy/product-service/pom.xml -DskipTests test-compile

echo "Resolving and compiling gateway test dependencies and published API contracts..."
mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml -DskipTests test-compile
