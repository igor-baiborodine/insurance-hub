package pl.altkom.asc.lab.micronaut.poc.product.service.domain;

import lombok.Getter;
import lombok.NoArgsConstructor;

@Getter
@NoArgsConstructor
public class Choice {
    private String code;
    private String label;

    public Choice(String code, String label) {
        this.code = code;
        this.label = label;
    }
}
