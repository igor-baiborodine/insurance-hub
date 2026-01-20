package pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic.config;

import co.elastic.clients.elasticsearch.ElasticsearchClient;
import co.elastic.clients.json.jackson.JacksonJsonpMapper;
import co.elastic.clients.transport.ElasticsearchTransport;
import co.elastic.clients.transport.rest_client.RestClientTransport;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.apache.http.HttpHost;
import org.apache.http.auth.AuthScope;
import org.apache.http.auth.UsernamePasswordCredentials;
import org.apache.http.client.CredentialsProvider;
import org.apache.http.conn.ssl.NoopHostnameVerifier;
import org.apache.http.impl.client.BasicCredentialsProvider;
import org.apache.http.ssl.SSLContextBuilder;
import org.apache.http.ssl.TrustStrategy;
import org.elasticsearch.client.RestClient;
import org.elasticsearch.client.RestClientBuilder;

import javax.inject.Singleton;
import javax.net.ssl.SSLContext;

import io.micronaut.context.annotation.Factory;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;

import java.security.cert.X509Certificate;

@Slf4j
@Factory
@RequiredArgsConstructor
public class ElasticConfig {

    private final ElasticSearchSettings elasticSearchSettings;
    private final ObjectMapper objectMapper;

    @Singleton
    public ElasticsearchClient elasticsearchClient() {
        try {
            CredentialsProvider credentialsProvider = new BasicCredentialsProvider();
            credentialsProvider.setCredentials(AuthScope.ANY,
                    new UsernamePasswordCredentials(elasticSearchSettings.getUsername(), elasticSearchSettings.getPassword()));

            String scheme = elasticSearchSettings.getScheme();
            if (scheme == null) scheme = "http";

            RestClientBuilder builder = RestClient.builder(
                    new HttpHost(elasticSearchSettings.getHost(), elasticSearchSettings.getPort(), scheme));

            if ("https".equalsIgnoreCase(scheme)) {
                TrustStrategy trustStrategy = (X509Certificate[] chain, String authType) -> true;
                SSLContext sslContext = new SSLContextBuilder()
                        .loadTrustMaterial(null, trustStrategy)
                        .build();

                builder.setHttpClientConfigCallback(httpClientBuilder -> httpClientBuilder
                        .setDefaultCredentialsProvider(credentialsProvider)
                        .setSSLContext(sslContext)
                        .setSSLHostnameVerifier(NoopHostnameVerifier.INSTANCE));
            } else {
                builder.setHttpClientConfigCallback(httpClientBuilder -> httpClientBuilder
                        .setDefaultCredentialsProvider(credentialsProvider));
            }

            builder.setRequestConfigCallback(config -> config
                    .setConnectTimeout(elasticSearchSettings.getConnectionTimeout())
                    .setConnectionRequestTimeout(elasticSearchSettings.getConnectionRequestTimeout())
                    .setSocketTimeout(elasticSearchSettings.getSocketTimeout())
            );

            RestClient restClient = builder.build();
            ElasticsearchTransport transport = new RestClientTransport(
                    restClient, new JacksonJsonpMapper(objectMapper));

            ElasticsearchClient client = new ElasticsearchClient(transport);
            log.info("Elasticsearch Java API Client initialized");
            testConnection(client);

            return client;
        } catch (Exception e) {
            log.error("Failed to initialize Elasticsearch client: {}", e.getMessage());
            throw new RuntimeException(e);
        }
    }

    private void testConnection(ElasticsearchClient client) {
        try {
            boolean success = client.ping().value();
            if (success) {
                log.info("Successfully connected to Elasticsearch at {}:{}",
                        elasticSearchSettings.getHost(), elasticSearchSettings.getPort());
            } else {
                log.error("Elasticsearch ping failed for {}:{}",
                        elasticSearchSettings.getHost(), elasticSearchSettings.getPort());
            }
        } catch (Exception e) {
            log.error("Failed to connect to Elasticsearch at {}:{}. Error: {}",
                    elasticSearchSettings.getHost(), elasticSearchSettings.getPort(), e.getMessage());
            throw new RuntimeException(e);
        }
    }
}
