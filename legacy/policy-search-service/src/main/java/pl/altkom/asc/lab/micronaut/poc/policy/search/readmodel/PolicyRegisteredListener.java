package pl.altkom.asc.lab.micronaut.poc.policy.search.readmodel;

import io.micronaut.configuration.kafka.annotation.KafkaListener;
import io.micronaut.configuration.kafka.annotation.OffsetReset;
import io.micronaut.configuration.kafka.annotation.Topic;
import io.micronaut.http.annotation.Header;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.events.PolicyRegisteredEvent;

@KafkaListener(clientId = "policy-registered-listener", offsetReset = OffsetReset.EARLIEST)
@Header(name = "Content-Type", value = "application/json")
public class PolicyRegisteredListener extends AbstractPolicyListener {

    @Topic("policy-registered")
    void onPolicyRegistered(PolicyRegisteredEvent event) {
        saveMappedPolicy(event.getPolicy());
    }
}
