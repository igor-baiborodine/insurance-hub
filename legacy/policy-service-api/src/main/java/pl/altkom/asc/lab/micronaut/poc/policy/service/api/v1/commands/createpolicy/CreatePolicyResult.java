package pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy;

import io.micronaut.core.annotation.Introspected;
import lombok.*;

@Introspected
@Getter
@Setter
@Builder
@ToString
@AllArgsConstructor
@NoArgsConstructor
public class CreatePolicyResult {
    private String policyNumber;
}
