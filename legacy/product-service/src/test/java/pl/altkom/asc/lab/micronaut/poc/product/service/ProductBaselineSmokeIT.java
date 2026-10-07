package pl.altkom.asc.lab.micronaut.poc.product.service;

import io.micronaut.core.type.Argument;
import io.micronaut.http.HttpRequest;
import io.micronaut.http.client.HttpClient;
import io.micronaut.runtime.server.EmbeddedServer;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;
import pl.altkom.asc.lab.micronaut.poc.product.service.api.v1.ProductDto;

import java.util.Collections;
import java.util.List;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class ProductBaselineSmokeIT extends BaseIT {

    private EmbeddedServer server;
    private HttpClient client;

    @BeforeAll
    void setup() {
        server = startServer();
        client = server.getApplicationContext().createBean(HttpClient.class, server.getURL());
    }

    @AfterAll
    void cleanup() {
        if (client != null) {
            client.close();
        }
        if (server != null) {
            server.stop();
        }

        assertThat(server).isNotNull();
        assertThat(server.isRunning()).isFalse();
        if (Boolean.getBoolean("baseline.smoke.exclusive")) {
            postgresqlContainer.stop();
            assertThat(postgresqlContainer.isRunning()).isFalse();
        }
    }

    @Nested
    public class ListenerAndDatabase {

        @Test
        public void happyPath() {
            // given
            assertThat(server.isRunning()).isTrue();
            assertThat(postgresqlContainer.isRunning()).isTrue();
            assertThat(postgresqlContainer.getContainerId()).isNotBlank();
            assertThat(postgresqlContainer.getDatabaseName()).isEqualTo("product_test");
            assertThat(postgresqlContainer.getJdbcUrl()).contains("/product_test");

            // when
            List<ProductDto> result = client.toBlocking().retrieve(
                    HttpRequest.GET("/products"),
                    Argument.listOf(ProductDto.class));

            // then
            assertThat(result)
                    .extracting(ProductDto::getCode)
                    .containsExactlyInAnyOrder("CAR", "FAI", "HSI", "TRI");
        }
    }

    @Nested
    public class SharedDatabaseProtection {

        @Test
        public void givenDatasourceOverride_thenRejectBeforeStartingAnotherServer() {
            // given
            Map<String, Object> unsafeProperties = Collections.singletonMap(
                    "datasources.default.url",
                    "jdbc:postgresql://127.0.0.1:5492/product");

            // when
            Throwable result = org.assertj.core.api.Assertions.catchThrowable(
                    () -> startServer(unsafeProperties));

            // then
            assertThat(result)
                    .isInstanceOf(IllegalArgumentException.class)
                    .hasMessageContaining("cannot override database isolation properties")
                    .hasMessageContaining("datasources.default.url");
            assertThat(server.isRunning()).isTrue();
        }
    }
}
