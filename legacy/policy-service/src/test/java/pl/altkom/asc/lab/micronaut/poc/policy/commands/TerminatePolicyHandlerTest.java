package pl.altkom.asc.lab.micronaut.poc.policy.commands;

import org.junit.jupiter.api.Test;
import pl.altkom.asc.lab.micronaut.poc.policy.domain.AgentRef;
import pl.altkom.asc.lab.micronaut.poc.policy.domain.Offer;
import pl.altkom.asc.lab.micronaut.poc.policy.domain.OfferStatus;
import pl.altkom.asc.lab.micronaut.poc.policy.domain.Person;
import pl.altkom.asc.lab.micronaut.poc.policy.domain.Policy;
import pl.altkom.asc.lab.micronaut.poc.policy.domain.PolicyFactory;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.events.PolicyTerminatedEvent;

import java.math.BigDecimal;
import java.time.LocalDate;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;

class TerminatePolicyHandlerTest {

    @Test
    void terminatedEventCarriesEffectiveCoverageDates() {
        LocalDate coverageStart = LocalDate.of(2026, 1, 1);
        LocalDate originalExpiry = LocalDate.of(2026, 12, 31);
        LocalDate terminationDate = LocalDate.of(2026, 6, 30);
        Offer offer = new Offer(
                null,
                "O-1",
                "P-1",
                coverageStart,
                originalExpiry,
                Map.of(),
                new BigDecimal("120"),
                Map.of("C-1", new BigDecimal("120")),
                OfferStatus.NEW,
                coverageStart
        );
        Policy policy = new PolicyFactory().fromOffer(
                offer, new Person("Synthetic", "Holder", "000000000"), AgentRef.of("agent"));
        policy.terminate(terminationDate);

        PolicyTerminatedEvent event = TerminatePolicyHandler.createEvent(policy);

        assertEquals(policy.getNumber(), event.getPolicy().getNumber());
        assertEquals(coverageStart, event.getPolicy().getFrom());
        assertEquals(terminationDate, event.getPolicy().getTo());
        assertEquals("Synthetic Holder", event.getPolicy().getPolicyHolder());
    }
}
