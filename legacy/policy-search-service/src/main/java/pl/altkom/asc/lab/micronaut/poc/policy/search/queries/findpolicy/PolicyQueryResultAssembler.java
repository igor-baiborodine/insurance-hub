package pl.altkom.asc.lab.micronaut.poc.policy.search.queries.findpolicy;

import lombok.extern.slf4j.Slf4j;
import pl.altkom.asc.lab.micronaut.poc.policy.search.readmodel.PolicyView;
import pl.altkom.asc.lab.micronaut.poc.policy.search.service.api.v1.queries.findpolicy.FindPolicyQueryResult;
import pl.altkom.asc.lab.micronaut.poc.policy.search.service.api.v1.queries.findpolicy.dto.PolicyListItemDto;

import java.util.Comparator;
import java.util.List;
import java.util.stream.Collectors;

@Slf4j
class PolicyQueryResultAssembler {

    static FindPolicyQueryResult constructResult(List<PolicyView> policies) {
        log.info("Assembling FindPolicyQueryResult with {} policies...", policies.size());
        List<PolicyListItemDto> items = policies.stream()
                .map(PolicyListItemDtoAssembler::map)
                .sorted(Comparator.comparing(PolicyListItemDto::getDateFrom, Comparator.nullsLast(Comparator.reverseOrder())))
                .collect(Collectors.toList());

        FindPolicyQueryResult result = new FindPolicyQueryResult();
        result.setPolicies(items);
        log.info("Assembled FindPolicyQueryResult with {} items", items.size());

        return result;
    }
}