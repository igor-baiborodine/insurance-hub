package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.ChoiceQuestion;

import java.util.ArrayList;
import java.util.List;
import java.util.stream.Collectors;

@Getter
@Setter
@NoArgsConstructor
public class ChoiceQuestionDefinition extends QuestionDefinition {

    private List<ChoiceDefinition> choices = new ArrayList<>();

    public ChoiceQuestionDefinition(String code, int index, String text, List<ChoiceDefinition> choices) {
        super(code, index, text);
        this.choices = choices;
    }

    @Override
    ChoiceQuestion toDomain() {
        return new ChoiceQuestion(
                getCode(),
                getIndex(),
                getText(),
                choices == null
                        ? new ArrayList<>()
                        : choices.stream().map(ChoiceDefinition::toDomain).collect(Collectors.toList()));
    }
}
