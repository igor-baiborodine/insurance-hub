package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Choice;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.ChoiceQuestion;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.DateQuestion;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.NumericQuestion;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;

import java.math.BigDecimal;
import java.util.Arrays;

import static org.assertj.core.api.Assertions.assertThat;

public class ProductDefinitionJsonbTypeTest {

    private final ProductDefinitionJsonbType classUnderTest = new ProductDefinitionJsonbType();

    @Nested
    public class ProductEntityRoundTrip {

        @Test
        public void happyPath() {
            // given
            Product product = sampleProduct();

            // when
            ProductEntity result = ProductEntity.from(product);

            // then
            assertThat(result.getCode()).isEqualTo("TRI");
            assertThat(result.toDomain().getCode()).isEqualTo("TRI");
            assertThat(result.toDomain().getName()).isEqualTo("Safe Traveller");
            assertThat(result.toDomain().getImage()).isEqualTo("/static/travel.jpg");
            assertThat(result.toDomain().getDescription()).isEqualTo("Travel insurance");
            assertThat(result.toDomain().getMaxNumberOfInsured()).isEqualTo(10);
            assertThat(result.toDomain().getIcon()).isEqualTo("plane");
            assertThat(result.toDomain().getCovers()).hasSize(3);
            assertThat(result.toDomain().getQuestions()).hasSize(3);
            assertThat(result.toDomain().getQuestions().get(0)).isInstanceOf(ChoiceQuestion.class);
            assertThat(result.toDomain().getQuestions().get(1)).isInstanceOf(NumericQuestion.class);
            assertThat(result.toDomain().getQuestions().get(2)).isInstanceOf(DateQuestion.class);
        }
    }

    @Nested
    public class JsonbTypeRoundTrip {

        @Test
        public void happyPath() {
            // given
            ProductDefinition definition = ProductDefinition.from(sampleProduct());

            // when
            String serialized = (String) classUnderTest.disassemble(definition);
            ProductDefinition restored = (ProductDefinition) classUnderTest.assemble(serialized, null);

            // then
            assertThat(serialized).contains("\"type\":\"choice\"");
            assertThat(serialized).contains("\"type\":\"numeric\"");
            assertThat(serialized).contains("\"type\":\"date\"");
            assertThat(restored.getName()).isEqualTo("Safe Traveller");
            assertThat(restored.getCovers()).hasSize(3);
            assertThat(restored.getQuestions()).hasSize(3);
            assertThat(restored.getQuestions().get(0)).isInstanceOf(ChoiceQuestionDefinition.class);
            assertThat(restored.getQuestions().get(1)).isInstanceOf(NumericQuestionDefinition.class);
            assertThat(restored.getQuestions().get(2)).isInstanceOf(DateQuestionDefinition.class);
        }
    }

    private Product sampleProduct() {
        Product product = new Product(
                "TRI",
                "Safe Traveller",
                "/static/travel.jpg",
                "Travel insurance",
                10,
                "plane");

        product.addCover("C1", "Luggage", "", false, new BigDecimal("5000"));
        product.addCover("C2", "Illness", "", false, new BigDecimal("5000"));
        product.addCover("C3", "Assistance", "", true, null);

        product.addQuestions(Arrays.asList(
                new ChoiceQuestion("DESTINATION", 1, "Destination", Arrays.asList(
                        new Choice("EUR", "Europe"),
                        new Choice("WORLD", "World"),
                        new Choice("PL", "Poland")
                )),
                new NumericQuestion("NUM_OF_ADULTS", 2, "Number of adults"),
                new DateQuestion("DEPARTURE_DATE", 3, "Departure date")
        ));

        return product;
    }
}
