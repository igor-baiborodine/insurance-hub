package pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.terminatepolicy;

import lombok.*;
import pl.altkom.asc.lab.micronaut.poc.command.bus.api.Command;

import io.micronaut.core.annotation.Introspected;

@Introspected
@Getter
@Setter
@ToString
@AllArgsConstructor
@NoArgsConstructor
public class TerminatePolicyCommand implements Command<TerminatePolicyResult> {
    private String policyNumber;
}
