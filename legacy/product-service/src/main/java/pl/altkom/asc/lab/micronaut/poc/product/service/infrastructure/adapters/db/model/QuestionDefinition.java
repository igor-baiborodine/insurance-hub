package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import com.fasterxml.jackson.annotation.JsonSubTypes;
import com.fasterxml.jackson.annotation.JsonTypeInfo;
import lombok.AllArgsConstructor;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.DateQuestion;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.NumericQuestion;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Question;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.ChoiceQuestion;

import java.util.ArrayList;
import java.util.stream.Collectors;

@Getter
@Setter
@NoArgsConstructor
@AllArgsConstructor
@JsonTypeInfo(
        use = JsonTypeInfo.Id.NAME,
        include = JsonTypeInfo.As.PROPERTY,
        property = "type")
@JsonSubTypes({
        @JsonSubTypes.Type(value = ChoiceQuestionDefinition.class, name = "choice"),
        @JsonSubTypes.Type(value = DateQuestionDefinition.class, name = "date"),
        @JsonSubTypes.Type(value = NumericQuestionDefinition.class, name = "numeric")
})
public abstract class QuestionDefinition {

    private String code;
    private int index;
    private String text;

    static QuestionDefinition from(Question question) {
        if (question instanceof ChoiceQuestion) {
            return new ChoiceQuestionDefinition(
                    question.getCode(),
                    question.getIndex(),
                    question.getText(),
                    ((ChoiceQuestion) question).getChoices() == null
                            ? new ArrayList<>()
                            : ((ChoiceQuestion) question).getChoices().stream()
                                    .map(ChoiceDefinition::from)
                                    .collect(Collectors.toList()));
        }

        if (question instanceof DateQuestion) {
            return new DateQuestionDefinition(question.getCode(), question.getIndex(), question.getText());
        }

        if (question instanceof NumericQuestion) {
            return new NumericQuestionDefinition(question.getCode(), question.getIndex(), question.getText());
        }

        throw new IllegalArgumentException("Unsupported question type: " + question.getClass().getName());
    }

    abstract Question toDomain();
}
