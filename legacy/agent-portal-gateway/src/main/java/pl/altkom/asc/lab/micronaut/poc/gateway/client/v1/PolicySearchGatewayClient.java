package pl.altkom.asc.lab.micronaut.poc.gateway.client.v1;

import io.micronaut.http.client.annotation.Client;
import io.micronaut.http.annotation.Get;
import io.micronaut.http.annotation.QueryValue;
import io.micronaut.retry.annotation.Retryable;
import io.reactivex.Maybe;
import pl.altkom.asc.lab.micronaut.poc.policy.search.service.api.v1.PolicySearchOperations;
import pl.altkom.asc.lab.micronaut.poc.policy.search.service.api.v1.queries.findpolicy.FindPolicyQueryResult;

@Client(id = "policy-search-service")
@Retryable(attempts = "2", delay = "2s")
public interface PolicySearchGatewayClient extends PolicySearchOperations {

    @Override
    @Get("/policies")
    Maybe<FindPolicyQueryResult> policies(@QueryValue("q") String queryText);
}
