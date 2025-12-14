package pl.altkom.asc.lab.micronaut.poc.payment.infrastructure.adapters.config;

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
    private String database = "payment";
    private String username = "payment";
    private String password = "payment";
    private boolean ssl = false;
}
