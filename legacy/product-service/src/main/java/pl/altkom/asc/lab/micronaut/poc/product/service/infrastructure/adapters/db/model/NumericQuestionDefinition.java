package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import lombok.NoArgsConstructor;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.NumericQuestion;

@NoArgsConstructor
public class NumericQuestionDefinition extends QuestionDefinition {

    public NumericQuestionDefinition(String code, int index, String text) {
        super(code, index, text);
    }

    @Override
    NumericQuestion toDomain() {
        return new NumericQuestion(getCode(), getIndex(), getText());
    }
}
