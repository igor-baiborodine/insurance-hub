package pl.altkom.asc.lab.micronaut.poc.policy.infrastructure.config;

import io.micronaut.context.annotation.ConfigurationProperties;
import io.micronaut.core.annotation.Introspected;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Introspected
@ConfigurationProperties("postgres")
public class PostgresDatasourceProperties {

    private String host = "localhost";
    private int port = 5432;
    private String database = "policy";
    private String username = "policy";
    private String password = "policy";
    private boolean ssl = false;
}
