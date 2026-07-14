package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import lombok.AccessLevel;
import lombok.Getter;
import lombok.NoArgsConstructor;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;

import javax.persistence.Column;
import javax.persistence.Convert;
import javax.persistence.Entity;
import javax.persistence.Id;
import javax.persistence.Table;

@Entity
@Table(name = "product")
@NoArgsConstructor(access = AccessLevel.PROTECTED)
@Getter
public class ProductEntity {

    @Id
    @Column(name = "code", nullable = false, updatable = false)
    private String code;

    @Convert(converter = ProductDefinitionJsonConverter.class)
    @Column(name = "definition", nullable = false, columnDefinition = "jsonb")
    private ProductDefinition definition;

    private ProductEntity(String code, ProductDefinition definition) {
        this.code = code;
        this.definition = definition;
    }

    public static ProductEntity from(Product product) {
        return new ProductEntity(product.getCode(), ProductDefinition.from(product));
    }

    public Product toDomain() {
        return definition.toDomain(code);
    }
}
