mvn clean install -f legacy/command-bus-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/command-bus
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/policy-service-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/documents-service-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/payment-service-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/policy-search-service-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/pricing-service-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/product-service-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/dashboard-service-api
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/auth-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/policy-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/payment-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/policy-search-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/pricing-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/product-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/documents-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/dashboard-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/chat-service -DskipTests
[ $? -eq 0 ] || exit 1

mvn clean install -f legacy/agent-portal-gateway -DskipTests
[ $? -eq 0 ] || exit 1
