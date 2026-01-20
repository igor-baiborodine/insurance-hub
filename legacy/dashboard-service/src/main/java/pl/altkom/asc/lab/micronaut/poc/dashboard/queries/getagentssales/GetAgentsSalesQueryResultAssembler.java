package pl.altkom.asc.lab.micronaut.poc.dashboard.queries.getagentssales;

import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.AgentSalesQuery;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getagentssalesquery.GetAgentsSalesQueryResult;
import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getagentssalesquery.dto.SalesDto;

import java.util.HashMap;

public class GetAgentsSalesQueryResultAssembler {

    public static GetAgentsSalesQueryResult assemble(AgentSalesQuery.Result agentsSales) {
        GetAgentsSalesQueryResult result = new GetAgentsSalesQueryResult();
        result.setPerAgentTotal(new HashMap<>());
        agentsSales.getPerAgentTotal().forEach((agent, sales) -> {
                    SalesDto salesDto = new SalesDto();
                    salesDto.setPoliciesCount(sales.getPoliciesCount());
                    salesDto.setPremiumAmount(sales.getPremiumAmount());
                    result.getPerAgentTotal().put(agent, salesDto);
                }
        );
        return result;
    }
}
