package pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.commands.createpolicy.dto;

import io.micronaut.core.annotation.Introspected;
import lombok.*;

@Introspected
@Getter
@Setter
@ToString
@NoArgsConstructor
@AllArgsConstructor
@Builder
public class PersonDto {
    private String firstName;
    private String lastName;
    private String taxId;
}
