package pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic;

import co.elastic.clients.elasticsearch._types.aggregations.Aggregation;
import co.elastic.clients.elasticsearch._types.aggregations.StringTermsBucket;
import co.elastic.clients.elasticsearch._types.aggregations.SumAggregate;
import co.elastic.clients.elasticsearch.core.SearchRequest;
import co.elastic.clients.elasticsearch.core.SearchResponse;

import co.elastic.clients.json.JsonData;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.SalesResult;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.TotalSalesQuery;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.util.Map;

class TotalSalesQueryAdapter extends QueryAdapter<TotalSalesQuery, TotalSalesQuery.Result> {

    public TotalSalesQueryAdapter(TotalSalesQuery query) {
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
                        .aggregations("count_by_product", sa -> sa
                                .terms(t -> t.field("productCode.keyword"))
                                .aggregations("total_premium", ssa -> ssa
                                        .sum(sum -> sum.field("totalPremium"))
                                )
                        )
                )
        );
    }

    @Override
    TotalSalesQuery.Result extractResult(SearchResponse<?> searchResponse) {
        TotalSalesQuery.Result.ResultBuilder result = TotalSalesQuery.Result.builder();
        long totalCount = 0;
        BigDecimal totalAmount = BigDecimal.ZERO;

        var filterAgg = searchResponse.aggregations().get("agg_filter").filter();
        var products = filterAgg.aggregations().get("count_by_product").sterms();

        for (StringTermsBucket b : products.buckets().array()) {
            long bucketCount = b.docCount();
            totalCount += bucketCount;

            SumAggregate sumAgg = b.aggregations().get("total_premium").sum();
            BigDecimal bucketAmount = BigDecimal.valueOf(sumAgg.value()).setScale(2, RoundingMode.HALF_UP);
            totalAmount = totalAmount.add(bucketAmount);

            result.productTotal(b.key().stringValue(), SalesResult.of(bucketCount, bucketAmount));
        }

        result.total(SalesResult.of(totalCount, totalAmount));
        return result.build();
    }
}
