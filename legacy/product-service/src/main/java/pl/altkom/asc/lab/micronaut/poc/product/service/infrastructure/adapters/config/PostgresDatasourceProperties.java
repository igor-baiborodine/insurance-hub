package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.config;

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
    private String database = "product";
    private String username = "product";
    private String password = "product";
    private boolean ssl = false;
}
