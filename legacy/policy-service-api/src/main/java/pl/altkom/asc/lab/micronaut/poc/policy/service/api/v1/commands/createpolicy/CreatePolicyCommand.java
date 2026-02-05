package pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy;

import lombok.*;
import pl.altkom.asc.lab.micronaut.poc.command.bus.api.Command;
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy.dto.PersonDto;

import io.micronaut.core.annotation.Introspected;

@Introspected
@Getter
@Setter
@ToString
@AllArgsConstructor
@NoArgsConstructor
public class CreatePolicyCommand implements Command<CreatePolicyResult> {
    private String offerNumber;
    private PersonDto policyHolder;
    private String agentLogin;
}
