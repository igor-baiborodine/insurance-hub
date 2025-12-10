package pl.altkom.asc.lab.micronaut.poc.documents.domain

import io.micronaut.configuration.kafka.annotation.KafkaListener
import io.micronaut.configuration.kafka.annotation.OffsetReset
import io.micronaut.configuration.kafka.annotation.Topic
import io.micronaut.http.annotation.Header
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.events.PolicyRegisteredEvent

@KafkaListener(clientId = "policy-registered-listener", offsetReset = OffsetReset.EARLIEST)
@Header(name = "Content-Type", value = "application/json")
class PolicyRegisteredListener(
    private val policyDocumentService: PolicyDocumentService
) {

    @Topic("policy-registered")
    fun onPolicyRegistered(event: PolicyRegisteredEvent) {
        policyDocumentService.add(event)
    }
}
