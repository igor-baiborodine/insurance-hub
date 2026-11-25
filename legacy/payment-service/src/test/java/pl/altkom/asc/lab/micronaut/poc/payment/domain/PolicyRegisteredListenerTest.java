package pl.altkom.asc.lab.micronaut.poc.payment.domain;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.ArgumentCaptor;
import org.mockito.Captor;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.events.PolicyRegisteredEvent;

import java.io.IOException;
import java.io.InputStream;
import java.math.BigDecimal;
import java.time.LocalDate;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

@ExtendWith(MockitoExtension.class)
class PolicyRegisteredListenerTest {

    @Mock
    private PolicyAccountRepository policyAccountRepository;
    @Mock
    private PolicyAccountNumberGenerator policyAccountNumberGenerator;
    @InjectMocks
    private PolicyRegisteredListener classUnderTest;

    @Captor
    private ArgumentCaptor<PolicyAccount> accountCaptor;

    @Test
    void happyPath() throws IOException {
        // given
        ObjectMapper mapper = new ObjectMapper().findAndRegisterModules();
        JsonNode record = readRecord(mapper);
        PolicyRegisteredEvent event = mapper.treeToValue(record.get("value"), PolicyRegisteredEvent.class);
        when(policyAccountRepository.findByPolicyNumber("SYN-442-001")).thenReturn(Optional.empty());
        when(policyAccountNumberGenerator.generate()).thenReturn("SYNTHETIC-ACCOUNT");

        // when
        classUnderTest.onPolicyRegistered(event);

        // then
        assertEquals(record.get("key").textValue(), event.getPolicy().getNumber());
        assertNull(event.getPolicy().getAgentLogin());
        verify(policyAccountRepository).save(accountCaptor.capture());
        PolicyAccount result = accountCaptor.getValue();
        assertEquals("SYN-442-001", result.getPolicyNumber());
        assertEquals(1, result.getEntries().size());
        AccountingEntry expected = result.getEntries().get(0);
        assertTrue(expected instanceof ExpectedPayment);
        assertEquals(0, new BigDecimal("120.00").compareTo(expected.getAmount()));
        assertEquals(LocalDate.of(2026, 1, 1), expected.getEffectiveDate());
    }

    private static JsonNode readRecord(ObjectMapper mapper) throws IOException {
        try (InputStream input = PolicyRegisteredListenerTest.class.getResourceAsStream(
                "/events/policy-registered.json")) {
            return mapper.readTree(input);
        }
    }
}
