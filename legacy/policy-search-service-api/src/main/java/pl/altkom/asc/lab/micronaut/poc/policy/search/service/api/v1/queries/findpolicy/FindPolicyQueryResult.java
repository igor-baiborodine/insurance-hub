package pl.altkom.asc.lab.micronaut.poc.policy.search.service.api.v1.queries.findpolicy;

import lombok.Setter;
import pl.altkom.asc.lab.micronaut.poc.policy.search.service.api.v1.queries.findpolicy.dto.PolicyListItemDto;

import java.util.Collections;
import java.util.List;

import io.micronaut.core.annotation.Introspected;
import lombok.Getter;
import lombok.NoArgsConstructor;

@Introspected
@Getter
@Setter
@NoArgsConstructor
public class FindPolicyQueryResult {
    private List<PolicyListItemDto> policies;

    public static FindPolicyQueryResult empty() {
        FindPolicyQueryResult result = new FindPolicyQueryResult();
        result.setPolicies(Collections.emptyList());
        return result;
    }
}
