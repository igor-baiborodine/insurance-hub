package pl.altkom.asc.lab.micronaut.poc.product.service.domain;

import lombok.Getter;
import lombok.NoArgsConstructor;

@NoArgsConstructor
@Getter
public class DateQuestion extends Question {
    public DateQuestion(
            String code,
            int index,
            String text) {
        super(code, index, text);
    }
}
