package pl.altkom.asc.lab.micronaut.poc.product.service.init;

import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;
import pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.InMemoryProductsRepository;

import java.util.List;

import static org.assertj.core.api.Assertions.assertThat;

public class DataLoaderTest {

    private final InMemoryProductsRepository productsRepository = new InMemoryProductsRepository();
    private final DataLoader classUnderTest = new DataLoader(productsRepository);

    @Nested
    public class OnApplicationEvent {

        @Test
        public void happyPath() {
            // given

            // when
            classUnderTest.onApplicationEvent(null);
            List<Product> result = productsRepository.findAll().blockingGet();

            // then
            assertThat(result).hasSize(4);
            assertThat(result)
                    .extracting(Product::getCode)
                    .containsExactlyInAnyOrder("CAR", "FAI", "HSI", "TRI");
        }

        @Test
        public void givenAlreadySeededCatalog_thenReplayingStartupDoesNotDuplicateProducts() {
            // given
            classUnderTest.onApplicationEvent(null);

            // when
            classUnderTest.onApplicationEvent(null);
            List<Product> result = productsRepository.findAll().blockingGet();

            // then
            assertThat(result).hasSize(4);
            assertThat(result)
                    .extracting(Product::getCode)
                    .containsExactlyInAnyOrder("CAR", "FAI", "HSI", "TRI");
        }
    }
}
