package pl.altkom.asc.lab.micronaut.poc.product.service;

import io.micronaut.context.ApplicationContext;
import io.micronaut.runtime.server.EmbeddedServer;
import org.testcontainers.containers.PostgreSQLContainer;

import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

public abstract class BaseIT {

    private static final String POSTGRES_DOCKER_IMAGE_NAME = "postgres:16.4-alpine";
    private static final String POSTGRES_DATABASE_NAME = "product_test";
    private static final List<String> DISPOSABLE_DATABASE_PROPERTY_PREFIXES = Arrays.asList(
            "datasources.default.",
            "postgres.",
            "jpa.default.properties.hibernate.hbm2ddl.auto",
            "micronaut.environments"
    );

    static final PostgreSQLContainer<?> postgresqlContainer;

    static {
        postgresqlContainer = new PostgreSQLContainer<>(POSTGRES_DOCKER_IMAGE_NAME)
                .withDatabaseName(POSTGRES_DATABASE_NAME);
        postgresqlContainer.start();
    }

    protected EmbeddedServer startServer() {
        return startServer(new HashMap<>());
    }

    protected EmbeddedServer startServerWithDisposableConnectionTimeout(long timeoutMillis) {
        return startServer(new HashMap<>(), timeoutMillis);
    }

    protected EmbeddedServer startServer(Map<String, Object> extraProperties) {
        return startServer(extraProperties, null);
    }

    private EmbeddedServer startServer(Map<String, Object> extraProperties, Long connectionTimeoutMillis) {
        List<String> unsafeProperties = extraProperties.keySet().stream()
                .filter(BaseIT::isDisposableDatabaseProperty)
                .sorted()
                .collect(Collectors.toList());
        if (!unsafeProperties.isEmpty()) {
            throw new IllegalArgumentException(
                    "Disposable Product tests cannot override database isolation properties: " + unsafeProperties);
        }

        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("datasources.default.url", postgresqlContainer.getJdbcUrl());
        properties.put("datasources.default.driverClassName", "org.postgresql.Driver");
        properties.put("datasources.default.username", postgresqlContainer.getUsername());
        properties.put("datasources.default.password", postgresqlContainer.getPassword());
        if (connectionTimeoutMillis != null) {
            properties.put("datasources.default.connectionTimeout", connectionTimeoutMillis);
        }
        properties.putAll(extraProperties);
        return ApplicationContext.run(EmbeddedServer.class, properties);
    }

    private static boolean isDisposableDatabaseProperty(String property) {
        return DISPOSABLE_DATABASE_PROPERTY_PREFIXES.stream().anyMatch(property::startsWith);
    }
}
