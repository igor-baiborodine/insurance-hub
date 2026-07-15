package pl.altkom.asc.lab.micronaut.poc.product.service.domain;

import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;


@Getter
@NoArgsConstructor
public class Cover {
    private String code;
    private String name;
    private String description;
    private boolean optional;
    private BigDecimal sumInsured;

    public Cover(
            String code,
            String name,
            String description,
            boolean optional,
            BigDecimal sumInsured) {
        this.code = code;
        this.name = name;
        this.description = description;
        this.optional = optional;
        this.sumInsured = sumInsured;
    }
}
