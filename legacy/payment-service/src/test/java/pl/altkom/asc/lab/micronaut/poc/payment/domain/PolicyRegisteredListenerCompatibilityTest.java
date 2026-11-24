package pl.altkom.asc.lab.micronaut.poc.payment.domain;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.events.PolicyRegisteredEvent;

import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.LocalDate;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

class PolicyRegisteredListenerCompatibilityTest {

    @Test
    void consumesPolicyProducedRegistrationWithExpectedPaymentContract() throws Exception {
        ObjectMapper mapper = new ObjectMapper().findAndRegisterModules();
        JsonNode fixture = mapper.readTree(Files.readString(fixturePath()));
        JsonNode registration = fixture.at("/events/registration");
        PolicyRegisteredEvent event = mapper.treeToValue(
                registration.get("value"), PolicyRegisteredEvent.class);
        PolicyAccountRepository repository = mock(PolicyAccountRepository.class);
        PolicyAccountNumberGenerator accountNumbers = mock(PolicyAccountNumberGenerator.class);
        when(repository.findByPolicyNumber("SYN-442-001")).thenReturn(Optional.empty());
        when(accountNumbers.generate()).thenReturn("SYNTHETIC-ACCOUNT");

        new PolicyRegisteredListener(repository, accountNumbers).onPolicyRegistered(event);

        assertEquals(registration.get("key").textValue(), event.getPolicy().getNumber());
        assertNull(event.getPolicy().getAgentLogin());
        ArgumentCaptor<PolicyAccount> captured = ArgumentCaptor.forClass(PolicyAccount.class);
        verify(repository).save(captured.capture());
        PolicyAccount account = captured.getValue();
        assertEquals("SYN-442-001", account.getPolicyNumber());
        assertEquals(1, account.getEntries().size());
        AccountingEntry expected = account.getEntries().get(0);
        assertTrue(expected instanceof ExpectedPayment);
        assertEquals(0, new BigDecimal("120.00").compareTo(expected.getAmount()));
        assertEquals(LocalDate.of(2026, 1, 1), expected.getEffectiveDate());
    }

    private static Path fixturePath() {
        Path root = Path.of("").toAbsolutePath();
        while (root != null) {
            Path candidate = root.resolve(
                    "services/policy-service/testdata/events/consumer-compatibility.json");
            if (Files.isRegularFile(candidate)) {
                return candidate;
            }
            root = root.getParent();
        }
        throw new IllegalStateException("Policy consumer compatibility fixture is unavailable");
    }
}
