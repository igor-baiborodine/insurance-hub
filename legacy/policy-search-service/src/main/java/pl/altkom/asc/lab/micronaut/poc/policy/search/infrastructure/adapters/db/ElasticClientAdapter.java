package pl.altkom.asc.lab.micronaut.poc.policy.search.infrastructure.adapters.db;

import io.reactivex.Maybe;
import lombok.extern.slf4j.Slf4j;
import org.apache.http.HttpHost;
import org.apache.http.auth.AuthScope;
import org.apache.http.auth.UsernamePasswordCredentials;
import org.apache.http.client.CredentialsProvider;
import org.apache.http.conn.ssl.NoopHostnameVerifier;
import org.apache.http.impl.client.BasicCredentialsProvider;
import org.apache.http.ssl.SSLContextBuilder;
import org.apache.http.ssl.TrustStrategy;
import org.elasticsearch.action.ActionListener;
import org.elasticsearch.action.index.IndexRequest;
import org.elasticsearch.action.admin.indices.create.CreateIndexRequest;
import org.elasticsearch.action.admin.indices.create.CreateIndexResponse;
import org.elasticsearch.action.admin.indices.get.GetIndexRequest;
import org.elasticsearch.action.index.IndexResponse;
import org.elasticsearch.action.search.SearchRequest;
import org.elasticsearch.action.search.SearchResponse;
import org.elasticsearch.client.RestClient;
import org.elasticsearch.client.RestClientBuilder;
import org.elasticsearch.client.RestHighLevelClient;
import io.reactivex.Single;
import io.reactivex.Completable;

import javax.inject.Singleton;
import javax.net.ssl.SSLContext;
import java.security.cert.X509Certificate;

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

    public Single<Boolean> indexExists(String indexName) {
        return Single.create(sink -> {
            GetIndexRequest request = new GetIndexRequest().indices(indexName);
            restHighLevelClient.indices().existsAsync(request, new ActionListener<Boolean>() {
                @Override
                public void onResponse(Boolean exists) {
                    sink.onSuccess(exists);
                }

                @Override
                public void onFailure(Exception e) {
                    sink.onError(e);
                }
            });
        });
    }

    public Completable createIndex(String indexName) {
        return Completable.create(sink -> {
            CreateIndexRequest request = new CreateIndexRequest(indexName);
            restHighLevelClient.indices().createAsync(request, new ActionListener<CreateIndexResponse>() {
                @Override
                public void onResponse(CreateIndexResponse createIndexResponse) {
                    sink.onComplete();
                }

                @Override
                public void onFailure(Exception e) {
                    sink.onError(e);
                }
            });
        });
    }

    private RestHighLevelClient buildClient() {
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
            builder.setMaxRetryTimeoutMillis(elasticSearchSettings.getMaxRetryTimeout());

            RestHighLevelClient client = new RestHighLevelClient(builder);
            log.info("Elasticsearch client initialized");
            testConnection(client);

            return client;
        } catch (Exception e) {
            log.error("Failed to initialize Elasticsearch client: {}", e.getMessage());
            throw new RuntimeException(e);
        }
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
            throw new RuntimeException(e);
        }
    }
}
