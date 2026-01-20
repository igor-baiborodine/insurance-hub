package pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic.config;

import io.micronaut.context.annotation.ConfigurationProperties;
import lombok.Getter;
import lombok.Setter;

@ConfigurationProperties("elastic")
@Getter
@Setter
class ElasticSearchSettings {
    private String scheme;
    private String host;
    private int port;
    private String username;
    private String password;
    private int connectionTimeout;
    private int connectionRequestTimeout;
    private int socketTimeout;
    private int maxRetryTimeout;
}
