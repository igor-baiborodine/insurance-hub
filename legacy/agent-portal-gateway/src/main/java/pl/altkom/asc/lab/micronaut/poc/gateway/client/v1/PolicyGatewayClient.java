package pl.altkom.asc.lab.micronaut.poc.gateway.client.v1;

import io.micronaut.http.annotation.Body;
import io.micronaut.http.annotation.Get;
import io.micronaut.http.annotation.Post;
import io.micronaut.http.client.annotation.Client;
import io.micronaut.http.client.exceptions.HttpClientResponseException;
import io.micronaut.http.client.exceptions.ReadTimeoutException;
import io.micronaut.retry.annotation.Retryable;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.PolicyOperations;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createoffer.CreateOfferCommand;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createoffer.CreateOfferResult;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy.CreatePolicyCommand;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy.CreatePolicyResult;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.terminatepolicy.TerminatePolicyCommand;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.terminatepolicy.TerminatePolicyResult;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.queries.getpolicydetails.GetPolicyDetailsQueryResult;

import javax.validation.constraints.NotNull;

/**
 * Retries only failures that happen before Policy can answer, such as a refused connection. A
 * Policy response or a read timeout may follow a handled command, so neither is replayed.
 */
@Client(id = "policy-service")
@Retryable(attempts = "2", delay = "2s",
        excludes = {HttpClientResponseException.class, ReadTimeoutException.class})
public interface PolicyGatewayClient extends PolicyOperations {

    @Post("/offers")
    CreateOfferResult createOffer(@Body @NotNull CreateOfferCommand cmd);

    @Override
    @Get("/policies/{policyNumber}")
    GetPolicyDetailsQueryResult get(String policyNumber);

    @Override
    @Post("/policies")
    CreatePolicyResult create(CreatePolicyCommand cmd);

    @Override
    @Post("/policies/terminate")
    TerminatePolicyResult terminate(TerminatePolicyCommand cmd);
}
