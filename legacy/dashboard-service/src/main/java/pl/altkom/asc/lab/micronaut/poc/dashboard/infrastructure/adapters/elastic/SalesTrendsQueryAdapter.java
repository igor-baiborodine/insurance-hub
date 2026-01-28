package pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic;

import co.elastic.clients.elasticsearch._types.aggregations.CalendarInterval;
import co.elastic.clients.elasticsearch._types.aggregations.DateHistogramBucket;
import co.elastic.clients.elasticsearch.core.SearchRequest;
import co.elastic.clients.elasticsearch.core.SearchResponse;

import co.elastic.clients.json.JsonData;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.SalesResult;
import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.SalesTrendsQuery;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.time.Instant;
import java.time.LocalDate;
import java.time.ZoneId;

public class SalesTrendsQueryAdapter extends QueryAdapter<SalesTrendsQuery,SalesTrendsQuery.Result> {
    public SalesTrendsQueryAdapter(SalesTrendsQuery query) {
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
                        .aggregations("sales", sa -> sa
                                .dateHistogram(dh -> dh
                                        .field("from")
                                        .calendarInterval(mapToCalendarInterval(query.getAggregationUnit()))
                                )
                                .aggregations("total_premium", ssa -> ssa
                                        .sum(sum -> sum.field("totalPremium"))
                                )
                        )
                )
        );
    }

    @Override
    SalesTrendsQuery.Result extractResult(SearchResponse<?> searchResponse) {
        SalesTrendsQuery.Result.ResultBuilder result = SalesTrendsQuery.Result.builder();

        var filterAgg = searchResponse.aggregations().get("agg_filter").filter();
        var salesHistogram = filterAgg.aggregations().get("sales").dateHistogram();

        for (DateHistogramBucket b : salesHistogram.buckets().array()) {
            LocalDate date = Instant.ofEpochMilli(b.key())
                    .atZone(ZoneId.systemDefault())
                    .toLocalDate();

            var sumAgg = b.aggregations().get("total_premium").sum();

            result.periodSale(
                    new SalesTrendsQuery.PeriodSales(
                            date,
                            b.keyAsString(),
                            SalesResult.of(b.docCount(), BigDecimal.valueOf(sumAgg.value()).setScale(2, RoundingMode.HALF_UP))
                    )
            );
        }

        return result.build();
    }

    private CalendarInterval mapToCalendarInterval(pl.altkom.asc.lab.micronaut.poc.dashboard.domain.TimeAggregationUnit unit) {
        return switch (unit) {
            case DAY -> CalendarInterval.Day;
            case WEEK -> CalendarInterval.Week;
            case MONTH -> CalendarInterval.Month;
            case YEAR -> CalendarInterval.Year;
        };
    }
}