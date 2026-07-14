package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import lombok.NoArgsConstructor;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.DateQuestion;

@NoArgsConstructor
public class DateQuestionDefinition extends QuestionDefinition {

    public DateQuestionDefinition(String code, int index, String text) {
        super(code, index, text);
    }

    @Override
    DateQuestion toDomain() {
        return new DateQuestion(getCode(), getIndex(), getText());
    }
}
