package pl.altkom.asc.lab.micronaut.poc.product.service;

import io.micronaut.context.ApplicationContext;
import io.micronaut.runtime.server.EmbeddedServer;
import org.junit.jupiter.api.AfterAll;
import org.testcontainers.containers.PostgreSQLContainer;

import java.util.HashMap;
import java.util.Map;

public abstract class BaseIT {

    private static final String POSTGRES_DOCKER_IMAGE_NAME = "postgres:16.4-alpine";
    private static final String POSTGRES_DATABASE_NAME = "product_test";

    static final PostgreSQLContainer<?> postgresqlContainer;

    static {
        postgresqlContainer = new PostgreSQLContainer<>(POSTGRES_DOCKER_IMAGE_NAME)
                .withDatabaseName(POSTGRES_DATABASE_NAME);
        postgresqlContainer.start();
    }

    protected EmbeddedServer startServer() {
        return startServer(new HashMap<>());
    }

    protected EmbeddedServer startServer(Map<String, Object> extraProperties) {
        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("datasources.default.url", postgresqlContainer.getJdbcUrl());
        properties.put("datasources.default.driverClassName", "org.postgresql.Driver");
        properties.put("datasources.default.username", postgresqlContainer.getUsername());
        properties.put("datasources.default.password", postgresqlContainer.getPassword());
        properties.putAll(extraProperties);
        return ApplicationContext.run(EmbeddedServer.class, properties);
    }

    @AfterAll
    static void stopPostgres() {
        if (postgresqlContainer != null) {
            postgresqlContainer.stop();
        }
    }
}
