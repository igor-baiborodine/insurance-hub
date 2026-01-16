package pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic;

import co.elastic.clients.elasticsearch._types.aggregations.StringTermsBucket;
import co.elastic.clients.elasticsearch._types.aggregations.SumAggregate;
import co.elastic.clients.elasticsearch.core.SearchRequest;
import co.elastic.clients.elasticsearch.core.SearchResponse;
import co.elastic.clients.json.JsonData;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.AgentSalesQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.SalesResult;

import java.math.BigDecimal;

public class AgentSalesQueryAdapter extends QueryAdapter<AgentSalesQuery, AgentSalesQuery.Result> {
    public AgentSalesQueryAdapter(AgentSalesQuery query) {
        super(query);
    }

    @Override
    SearchRequest buildQuery() {
        return SearchRequest.of(s -> s
                .index("policy_stats")
                .size(0)
                .aggregations("agg_filter", a -> a
                        .filter(f -> f
                                .bool(b -> {
                                    if (query.getFilterByAgentLogin() != null) {
                                        b.must(m -> m.term(t -> t.field("agentLogin.keyword").value(query.getFilterByAgentLogin())));
                                    }
                                    if (query.getFilterByProductCode() != null) {
                                        b.must(m -> m.term(t -> t.field("productCode.keyword").value(query.getFilterByProductCode())));
                                    }
                                    if (query.getFilterBySalesDate() != null) {
                                        b.must(m -> m.range(r -> r
                                                .field("from")
                                                .gte(JsonData.of(query.getFilterBySalesDate().getFrom().toString()))
                                                .lt(JsonData.of(query.getFilterBySalesDate().getTo().toString()))
                                        ));
                                    }
                                    return b;
                                })
                        )
                        .aggregations("count_by_agent", sa -> sa
                                .terms(t -> t.field("agentLogin.keyword"))
                                .aggregations("total_premium", ssa -> ssa
                                        .sum(sum -> sum.field("totalPremium"))
                                )
                        )
                )
        );
    }

    @Override
    AgentSalesQuery.Result extractResult(SearchResponse<?> searchResponse) {
        AgentSalesQuery.Result.ResultBuilder result = AgentSalesQuery.Result.builder();

        var filterAgg = searchResponse.aggregations().get("agg_filter").filter();
        var agents = filterAgg.aggregations().get("count_by_agent").sterms();

        for (StringTermsBucket b : agents.buckets().array()) {
            SumAggregate sumAgg = b.aggregations().get("total_premium").sum();
            result.agentTotal(
                    b.key().stringValue(),
                    SalesResult.of(b.docCount(), BigDecimal.valueOf(sumAgg.value()))
            );
        }

        return result.build();
    }
}
