package pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.web;


import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.micronaut.http.annotation.Controller;
import io.micronaut.validation.Validated;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import pl.altkom.asc.lab.micronaut.poc.command.bus.CommandBus;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.DashboardOperations;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getagentssalesquery.GetAgentsSalesQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getagentssalesquery.GetAgentsSalesQueryResult;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getsalestrendsquery.GetSalesTrendsQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getsalestrendsquery.GetSalesTrendsQueryResult;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.gettotalsalesquery.GetTotalSalesQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.gettotalsalesquery.GetTotalSalesQueryResult;

@Slf4j
@RequiredArgsConstructor
@Validated
@Controller("/dashboard")
public class DashboardController implements DashboardOperations {

    private final CommandBus bus;
    private final ObjectMapper objectMapper;

    @Override
    public GetTotalSalesQueryResult queryTotalSales(GetTotalSalesQuery query) {
        GetTotalSalesQueryResult getTotalSalesQueryResult = bus.executeQuery(query);
        logResult(getTotalSalesQueryResult);
        return getTotalSalesQueryResult;
    }

    @Override
    public GetSalesTrendsQueryResult querySalesTrends(GetSalesTrendsQuery query) {
        GetSalesTrendsQueryResult getSalesTrendsQueryResult = bus.executeQuery(query);
        logResult(getSalesTrendsQueryResult);
        return getSalesTrendsQueryResult;
    }

    @Override
    public GetAgentsSalesQueryResult queryAgentsSales(GetAgentsSalesQuery query) {
        GetAgentsSalesQueryResult getAgentsSalesQueryResult = bus.executeQuery(query);
        logResult(getAgentsSalesQueryResult);
        return getAgentsSalesQueryResult;
    }

    private void logResult(Object result) {
        try {
            log.debug("Result: {}", objectMapper.writeValueAsString(result));
        } catch (JsonProcessingException e) {
            log.error("Failed to serialize result", e);
        }
    }
}
