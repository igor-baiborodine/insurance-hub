package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import lombok.AllArgsConstructor;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Cover;

import java.math.BigDecimal;

@Getter
@Setter
@NoArgsConstructor
@AllArgsConstructor
public class CoverDefinition {

    private String code;
    private String name;
    private String description;
    private boolean optional;
    private BigDecimal sumInsured;

    static CoverDefinition from(Cover cover) {
        return new CoverDefinition(
                cover.getCode(),
                cover.getName(),
                cover.getDescription(),
                cover.isOptional(),
                cover.getSumInsured());
    }

    Cover toDomain() {
        return new Cover(code, name, description, optional, sumInsured);
    }
}
