package pl.altkom.asc.lab.micronaut.poc.policy.search.infrastructure.adapters.db;

import io.reactivex.Maybe;
import java.util.Arrays;
import java.util.List;
import java.util.stream.Collectors;
import javax.inject.Singleton;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.elasticsearch.action.index.IndexRequest;
import org.elasticsearch.action.search.SearchRequest;
import org.elasticsearch.action.search.SearchResponse;
import org.elasticsearch.xcontent.XContentType;
import org.elasticsearch.index.query.QueryBuilders;
import org.elasticsearch.index.query.QueryStringQueryBuilder;
import org.elasticsearch.search.builder.SearchSourceBuilder;
import pl.altkom.asc.lab.micronaut.poc.policy.search.readmodel.PolicyView;
import pl.altkom.asc.lab.micronaut.poc.policy.search.readmodel.PolicyViewRepository;
import pl.altkom.asc.lab.micronaut.poc.policy.search.service.api.v1.queries.findpolicy.FindPolicyQuery;

@Slf4j
@Singleton
@RequiredArgsConstructor
public class ElasticPolicyViewRepository implements PolicyViewRepository {

    private static final String INDEX_NAME = "policy-views";

    private final ElasticClientAdapter elasticClientAdapter;
    private final JsonConverter jsonConverter;
    
    @Override
    public void save(PolicyView policy) {
        log.info("Saving policy {}", policy);
        IndexRequest indexRequest = new IndexRequest(INDEX_NAME).id(policy.getNumber());
        indexRequest.source(jsonConverter.stringifyObject(policy), XContentType.JSON);
        elasticClientAdapter.index(indexRequest).blockingGet();
    }
    
    @Override
    public Maybe<List<PolicyView>> findAll(FindPolicyQuery query) {
        log.info("Searching policies for query: {}", query.getQueryText());

        QueryStringQueryBuilder queryStringQueryBuilder = QueryBuilders.queryStringQuery(query.getQueryText())
                .field("number")
                .field("policyHolder");
        SearchSourceBuilder searchSourceBuilder = new SearchSourceBuilder();
        searchSourceBuilder.query(queryStringQueryBuilder).size(100);

        SearchRequest searchRequest = new SearchRequest(INDEX_NAME);
        searchRequest.source(searchSourceBuilder);

        return elasticClientAdapter
                .search(searchRequest)
                .map(response -> {
                    List<PolicyView> results = mapSearchResponse(response);
                    log.info("Found {} policies for query: {}", results.size(), query.getQueryText());
                    return results;
                });
    }

    private List<PolicyView> mapSearchResponse(SearchResponse searchResponse) {
        return Arrays
                .stream(searchResponse.getHits().getHits())
                .map(hit -> jsonConverter.objectFromString(hit.getSourceAsString(), PolicyView.class))
                .collect(Collectors.toList());
    }
    
    
}
