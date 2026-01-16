package pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic;

import co.elastic.clients.elasticsearch.ElasticsearchClient;
import co.elastic.clients.elasticsearch._types.Refresh;
import co.elastic.clients.elasticsearch.core.SearchResponse;
import co.elastic.clients.elasticsearch.core.search.Hit;

import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.AgentSalesQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.PolicyDocument;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.PolicyRepository;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.SalesTrendsQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.TotalSalesQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic.config.JsonConverter;

import java.io.IOException;

import javax.inject.Singleton;

import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;

@Slf4j
@Singleton
@RequiredArgsConstructor
public class PolicyElasticRepository implements PolicyRepository {

    private final ElasticsearchClient esClient;

    @Override
    public boolean indexExists() {
        try {
            return esClient.indices().exists(e -> e.index("policy_stats")).value();
        } catch (IOException e) {
            log.error("Error while checking if index exists", e);
            return false;
        }
    }

    public void save(PolicyDocument policyDocument) {
        try {
            esClient.index(i -> i
                    .index("policy_stats")
                    .id(policyDocument.getNumber())
                    .refresh(Refresh.True)
                    .document(policyDocument)
            );
        } catch (IOException e) {
            log.error("Error while saving policy", e);
            throw new RuntimeException("Error while executing query", e);
        }
    }

    public PolicyDocument findByNumber(String number) {
        try {
            SearchResponse<PolicyDocument> response = esClient.search(s -> s
                            .index("policy_stats")
                            .query(q -> q
                                    .bool(b -> b
                                            .must(m -> m
                                                    .term(t -> t
                                                            .field("number.keyword")
                                                            .value(number)
                                                    )
                                            )
                                    )
                            )
                            .size(1),
                    PolicyDocument.class
            );

            return response.hits().hits().isEmpty()
                    ? null
                    : response.hits().hits().get(0).source();
        } catch (IOException e) {
            log.error("Error while searching for policy", e);
            throw new RuntimeException("Failed to find policy by number", e);
        }
    }

    public TotalSalesQuery.Result getTotalSales(TotalSalesQuery query) {
        // Note: TotalSalesQueryAdapter must be updated to return co.elastic.clients.elasticsearch.core.SearchRequest
        TotalSalesQueryAdapter queryAdapter = QueryAdapter.of(query);
        try {
            var response = esClient.search(queryAdapter.buildQuery(), Void.class);
            return queryAdapter.extractResult(response);
        } catch (IOException e) {
            throw new RuntimeException("Failed to execute total sales search", e);
        }
    }

    public SalesTrendsQuery.Result getSalesTrends(SalesTrendsQuery query) {
        SalesTrendsQueryAdapter queryAdapter = QueryAdapter.of(query);
        try {
            var response = esClient.search(queryAdapter.buildQuery(), Void.class);
            return queryAdapter.extractResult(response);
        } catch (IOException e) {
            throw new RuntimeException("Failed to execute sales trends search", e);
        }
    }

    public AgentSalesQuery.Result getAgentSales(AgentSalesQuery query) {
        AgentSalesQueryAdapter queryAdapter = QueryAdapter.of(query);
        try {
            var response = esClient.search(queryAdapter.buildQuery(), Void.class);
            return queryAdapter.extractResult(response);
        } catch (IOException e) {
            throw new RuntimeException("Failed to execute agent sales search", e);
        }
    }
}