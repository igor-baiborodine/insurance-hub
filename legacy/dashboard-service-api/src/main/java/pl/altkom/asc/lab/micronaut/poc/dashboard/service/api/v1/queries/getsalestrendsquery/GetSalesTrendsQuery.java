package pl.altkom.asc.lab.micronaut.poc.dashboard.service.api.v1.queries.getsalestrendsquery;

import lombok.*;
import pl.altkom.asc.lab.micronaut.poc.command.bus.api.Query;

import java.time.LocalDate;

import io.micronaut.core.annotation.Introspected;

@Introspected
@Getter
@Setter
@ToString
@AllArgsConstructor
@NoArgsConstructor
public class GetSalesTrendsQuery implements Query<GetSalesTrendsQueryResult> {
    private String productCode;
    private LocalDate saleDateFrom;
    private LocalDate saleDateTo;
    private String aggregationUnitCode;
}
