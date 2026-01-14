package pl.altkom.asc.lab.micronaut.poc.gateway.client.v1.fallback;

import io.micronaut.retry.annotation.Fallback;
import lombok.extern.slf4j.Slf4j;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.PolicyOperations;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy.CreatePolicyCommand;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy.CreatePolicyResult;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.terminatepolicy.TerminatePolicyCommand;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.terminatepolicy.TerminatePolicyResult;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.queries.getpolicydetails.GetPolicyDetailsQueryResult;

import javax.inject.Singleton;
import javax.validation.constraints.NotNull;

@Slf4j
@Singleton
@Fallback
public class PolicyGatewayClientFallback implements PolicyOperations {

    @Override
    public GetPolicyDetailsQueryResult get(@NotNull String policyNumber) {
        log.warn("Fallback called for get() with policyNumber: {}, empty result returned",
                policyNumber);
        return GetPolicyDetailsQueryResult.empty();
    }

    @Override
    public CreatePolicyResult create(@NotNull CreatePolicyCommand cmd) {
        log.warn("Fallback called for create() with offerNumber: {}, empty result returned",
                cmd.getOfferNumber());
        return new CreatePolicyResult(null);
    }

    @Override
    public TerminatePolicyResult terminate(@NotNull TerminatePolicyCommand cmd) {
        log.warn("Fallback called for terminate(), empty result returned");
        return TerminatePolicyResult.empty();
    }
}
