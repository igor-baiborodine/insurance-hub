package pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getagentssalesquery;

import pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getagentssalesquery.dto.SalesDto;

import java.util.Map;

import io.micronaut.core.annotation.Introspected;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;
import lombok.Singular;

@Introspected
@Getter
@Setter
@NoArgsConstructor
public class GetAgentsSalesQueryResult {
    @Singular("agentTotal")
    private Map<String, SalesDto> perAgentTotal;
}
