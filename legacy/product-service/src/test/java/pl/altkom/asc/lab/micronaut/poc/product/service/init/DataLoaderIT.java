package pl.altkom.asc.lab.micronaut.poc.product.service.init;

import io.micronaut.runtime.server.EmbeddedServer;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;
import pl.altkom.asc.lab.micronaut.poc.product.service.BaseIT;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Products;
import pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.ProductsRepository;

import java.util.List;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class DataLoaderIT extends BaseIT {

    private EmbeddedServer server;
    private DataLoader classUnderTest;
    private Products productsRepository;
    private ProductsRepository postgresRepository;

    @BeforeAll
    void setup() {
        server = startServer();
        classUnderTest = server.getApplicationContext().getBean(DataLoader.class);
        productsRepository = server.getApplicationContext().getBean(Products.class);
        postgresRepository = server.getApplicationContext().getBean(ProductsRepository.class);
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

    @Test
    public void happyPath() {
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
