package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.web;

import io.micronaut.context.ApplicationContext;
import io.micronaut.runtime.server.EmbeddedServer;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;
import pl.altkom.asc.lab.micronaut.poc.product.service.api.v1.ProductDto;

import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Function;
import java.util.stream.Collectors;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class ProductsControllerIT {

    private ProductsTestClient classUnderTest;
    private EmbeddedServer server;

    @BeforeAll
    void setup() {
        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        server = ApplicationContext.run(EmbeddedServer.class, properties);
        classUnderTest = server.getApplicationContext().createBean(ProductsTestClient.class, server.getURL());
    }

    @AfterAll
    void cleanup() {
        if (server != null) {
            server.stop();
        }
    }

    @Nested
    public class GetAll {

        @Test
        public void happyPath() {
            // given

            // when
            List<ProductDto> result = classUnderTest.getAll();

            // then
            assertThat(result).hasSize(4);
            assertThat(result)
                    .extracting(ProductDto::getCode)
                    .containsExactlyInAnyOrder("CAR", "FAI", "HSI", "TRI");

            Map<String, ProductDto> productsByCode = result.stream()
                    .collect(Collectors.toMap(ProductDto::getCode, Function.identity()));

            assertThat(productsByCode.get("CAR"))
                    .satisfies(product -> {
                        assertThat(product.getName()).isEqualTo("Happy Driver");
                        assertThat(product.getCovers()).hasSize(1);
                        assertThat(product.getQuestions()).hasSize(1);
                        assertThat(product.getMaxNumberOfInsured()).isEqualTo(1);
                        assertThat(product.getIcon()).isEqualTo("car");
                    });

            assertThat(productsByCode.get("FAI"))
                    .satisfies(product -> {
                        assertThat(product.getName()).isEqualTo("Happy farm");
                        assertThat(product.getCovers()).hasSize(4);
                        assertThat(product.getQuestions()).hasSize(4);
                        assertThat(product.getMaxNumberOfInsured()).isEqualTo(1);
                        assertThat(product.getIcon()).isEqualTo("apple");
                    });

            assertThat(productsByCode.get("HSI"))
                    .satisfies(product -> {
                        assertThat(product.getName()).isEqualTo("Happy House");
                        assertThat(product.getCovers()).hasSize(4);
                        assertThat(product.getQuestions()).hasSize(4);
                        assertThat(product.getMaxNumberOfInsured()).isEqualTo(5);
                        assertThat(product.getIcon()).isEqualTo("building");
                    });

            assertThat(productsByCode.get("TRI"))
                    .satisfies(product -> {
                        assertThat(product.getName()).isEqualTo("Safe Traveller");
                        assertThat(product.getCovers()).hasSize(3);
                        assertThat(product.getQuestions()).hasSize(3);
                        assertThat(product.getMaxNumberOfInsured()).isEqualTo(10);
                        assertThat(product.getIcon()).isEqualTo("plane");
                    });
        }
    }

    @Nested
    public class GetByCode {

        @Test
        public void happyPath() {
            // given
            String productCode = "TRI";

            // when
            ProductDto result = classUnderTest.get(productCode);

            // then
            assertThat(result).isNotNull();
            assertThat(result.getCode()).isEqualTo("TRI");
            assertThat(result.getName()).isEqualTo("Safe Traveller");
            assertThat(result.getCovers()).hasSize(3);
            assertThat(result.getQuestions()).hasSize(3);
            assertThat(result.getMaxNumberOfInsured()).isEqualTo(10);
            assertThat(result.getIcon()).isEqualTo("plane");
        }
    }
}
