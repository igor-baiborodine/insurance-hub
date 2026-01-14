package pl.altkom.asc.lab.micronaut.poc.policy.search.infrastructure.adapters.db;

import io.reactivex.Maybe;
import lombok.extern.slf4j.Slf4j;
import org.apache.http.HttpHost;
import org.apache.http.auth.AuthScope;
import org.apache.http.auth.UsernamePasswordCredentials;
import org.apache.http.client.CredentialsProvider;
import org.apache.http.impl.client.BasicCredentialsProvider;
import org.elasticsearch.action.ActionListener;
import org.elasticsearch.action.index.IndexRequest;
import org.elasticsearch.action.index.IndexResponse;
import org.elasticsearch.action.search.SearchRequest;
import org.elasticsearch.action.search.SearchResponse;
import org.elasticsearch.client.RestClient;
import org.elasticsearch.client.RestHighLevelClient;

import javax.inject.Singleton;

@Singleton
@Slf4j
public class ElasticClientAdapter {

    private final RestHighLevelClient restHighLevelClient;
    private final ElasticSearchSettings elasticSearchSettings;

    public ElasticClientAdapter(ElasticSearchSettings elasticSearchSettings) {
        this.elasticSearchSettings = elasticSearchSettings;
        this.restHighLevelClient = buildClient();
    }

    Maybe<IndexResponse> index(IndexRequest indexRequest) {
        return Maybe.create(sink -> {
            restHighLevelClient.indexAsync(indexRequest, new ActionListener<IndexResponse>() {
                @Override
                public void onResponse(IndexResponse indexResponse) {
                    sink.onSuccess(indexResponse);
                }

                @Override
                public void onFailure(Exception e) {
                    sink.onError(e);
                }
            });
        });
    }

    public Maybe<SearchResponse> search(SearchRequest searchRequest) {
        return Maybe.create(sink ->
                restHighLevelClient.searchAsync(searchRequest, new ActionListener<SearchResponse>() {
                    @Override
                    public void onResponse(SearchResponse searchResponse) {
                        sink.onSuccess(searchResponse);
                    }

                    @Override
                    public void onFailure(Exception e) {
                        sink.onError(e);
                    }
                }));
    }

        private RestHighLevelClient buildClient() {
            CredentialsProvider credentialsProvider = new BasicCredentialsProvider();
            credentialsProvider.setCredentials(AuthScope.ANY,
                    new UsernamePasswordCredentials(
                            elasticSearchSettings.getUsername(), elasticSearchSettings.getPassword()));

            RestHighLevelClient client = new RestHighLevelClient(
                    RestClient.builder(new HttpHost(elasticSearchSettings.getHost(), elasticSearchSettings.getPort()))
                            .setHttpClientConfigCallback(httpClientBuilder -> httpClientBuilder
                                    .setDefaultCredentialsProvider(credentialsProvider))
                            .setRequestConfigCallback(config -> config
                                    .setConnectTimeout(elasticSearchSettings.getConnectionTimeout())
                                    .setConnectionRequestTimeout(elasticSearchSettings.getConnectionRequestTimeout())
                                    .setSocketTimeout(elasticSearchSettings.getSocketTimeout())
                            )
                            .setMaxRetryTimeoutMillis(elasticSearchSettings.getMaxRetryTimeout()));

            testConnection(client);

            return client;
        }

        private void testConnection(RestHighLevelClient client) {
            try {
                boolean success = client.ping();
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
            }
        }
}
