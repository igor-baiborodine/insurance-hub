package pl.altkom.asc.lab.micronaut.poc.policy.infrastructure.config;

import io.micronaut.configuration.jdbc.hikari.DatasourceConfiguration;
import io.micronaut.context.annotation.Requires;
import io.micronaut.context.env.Environment;
import io.micronaut.context.event.BeanCreatedEvent;
import io.micronaut.context.event.BeanCreatedEventListener;

import javax.inject.Singleton;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;

@Singleton
@Requires(notEnv = Environment.TEST)
public class PostgresDatasourceConfigurationListener
        implements BeanCreatedEventListener<DatasourceConfiguration> {

    private final PostgresDatasourceProperties postgres;

    public PostgresDatasourceConfigurationListener(PostgresDatasourceProperties postgres) {
        this.postgres = postgres;
    }

    @Override
    public DatasourceConfiguration onCreated(BeanCreatedEvent<DatasourceConfiguration> event) {
        DatasourceConfiguration cfg = event.getBean();

        if ("default".equals(cfg.getName())) {
            cfg.setDriverClassName("org.postgresql.Driver");
            cfg.setUsername(postgres.getUsername());
            cfg.setPassword(postgres.getPassword());
            cfg.setUrl(buildJdbcUrl());
        }

        return cfg;
    }

    private String buildJdbcUrl() {
        String sslMode = postgres.isSsl() ? "require" : "disable";

        return "jdbc:postgresql://" + postgres.getHost() + ":" + postgres.getPort()
                + "/" + postgres.getDatabase()
                + "?sslmode=" + sslMode
                + "&user=" + encode(postgres.getUsername())
                + "&password=" + encode(postgres.getPassword());
    }

    private String encode(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8);
    }
}
