package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import lombok.AllArgsConstructor;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Choice;

@Getter
@Setter
@NoArgsConstructor
@AllArgsConstructor
public class ChoiceDefinition {

    private String code;
    private String label;

    static ChoiceDefinition from(Choice choice) {
        return new ChoiceDefinition(choice.getCode(), choice.getLabel());
    }

    Choice toDomain() {
        return new Choice(code, label);
    }
}
