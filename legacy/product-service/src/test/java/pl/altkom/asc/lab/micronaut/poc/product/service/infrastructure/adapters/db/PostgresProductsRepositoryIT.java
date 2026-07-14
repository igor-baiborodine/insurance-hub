package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db;

import io.micronaut.runtime.server.EmbeddedServer;
import io.reactivex.Maybe;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;
import pl.altkom.asc.lab.micronaut.poc.product.service.BaseIT;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Choice;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.ChoiceQuestion;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.NumericQuestion;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Products;

import java.math.BigDecimal;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class PostgresProductsRepositoryIT extends BaseIT {

    private Products classUnderTest;
    private PostgresProductsRepository postgresRepository;
    private EmbeddedServer server;

    @BeforeAll
    void setup() {
        Map<String, Object> properties = new HashMap<>();
        properties.put("products.persistence", "postgres");
        server = startServer(properties);
        classUnderTest = server.getApplicationContext().getBean(Products.class);
        postgresRepository = server.getApplicationContext().getBean(PostgresProductsRepository.class);
    }

    @BeforeEach
    void clearDatabase() {
        postgresRepository.deleteAll();
    }

    @AfterAll
    void cleanup() {
        if (server != null) {
            server.stop();
        }
    }

    @Nested
    public class Add {

        @Test
        public void happyPath() {
            // given
            Product product = sampleProduct("TRI", "Safe Traveller");

            // when
            Product result = classUnderTest.add(product).blockingGet();

            // then
            assertThat(result.getCode()).isEqualTo("TRI");
            assertThat(result.getName()).isEqualTo("Safe Traveller");
            assertThat(result.getCovers()).hasSize(3);
            assertThat(result.getQuestions()).hasSize(3);
            assertThat(postgresRepository.findByCode("TRI")).isPresent();
        }
    }

    @Nested
    public class FindAll {

        @Test
        public void happyPath() {
            // given
            classUnderTest.add(sampleProduct("TRI", "Safe Traveller")).blockingGet();
            classUnderTest.add(sampleProduct("CAR", "Happy Driver")).blockingGet();

            // when
            List<Product> result = classUnderTest.findAll().blockingGet();

            // then
            assertThat(result).hasSize(2);
            assertThat(result)
                    .extracting(Product::getCode)
                    .containsExactlyInAnyOrder("TRI", "CAR");
        }
    }

    @Nested
    public class FindOne {

        @Test
        public void happyPath() {
            // given
            classUnderTest.add(sampleProduct("TRI", "Safe Traveller")).blockingGet();

            // when
            Maybe<Product> result = classUnderTest.findOne("TRI");

            // then
            Product product = result.blockingGet();
            assertThat(product).isNotNull();
            assertThat(product.getCode()).isEqualTo("TRI");
            assertThat(product.getName()).isEqualTo("Safe Traveller");
            assertThat(product.getCovers()).hasSize(3);
            assertThat(product.getQuestions()).hasSize(3);
        }
    }

    private Product sampleProduct(String code, String name) {
        Product product = new Product(
                code,
                name,
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
                new NumericQuestion("NUM_OF_CHILDREN", 3, "Number of children")
        ));

        return product;
    }
}
