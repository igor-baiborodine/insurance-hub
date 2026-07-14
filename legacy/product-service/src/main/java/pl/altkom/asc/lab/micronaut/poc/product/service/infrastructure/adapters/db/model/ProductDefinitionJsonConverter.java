package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;

import javax.persistence.AttributeConverter;
import javax.persistence.Converter;

@Converter()
public class ProductDefinitionJsonConverter implements AttributeConverter<ProductDefinition, String> {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    @Override
    public String convertToDatabaseColumn(ProductDefinition attribute) {
        if (attribute == null) {
            return null;
        }

        try {
            return OBJECT_MAPPER.writeValueAsString(attribute);
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("Cannot serialize product definition to JSON", e);
        }
    }

    @Override
    public ProductDefinition convertToEntityAttribute(String dbData) {
        if (dbData == null) {
            return null;
        }

        try {
            return OBJECT_MAPPER.readValue(dbData, ProductDefinition.class);
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("Cannot deserialize product definition from JSON", e);
        }
    }
}
