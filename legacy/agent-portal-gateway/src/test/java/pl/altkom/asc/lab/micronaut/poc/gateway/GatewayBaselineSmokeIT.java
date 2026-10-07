package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.micronaut.context.ApplicationContext;
import io.micronaut.core.type.Argument;
import io.micronaut.http.HttpRequest;
import io.micronaut.http.HttpResponse;
import io.micronaut.http.HttpStatus;
import io.micronaut.http.client.HttpClient;
import io.micronaut.http.client.exceptions.HttpClientResponseException;
import io.micronaut.runtime.server.EmbeddedServer;
import io.micronaut.security.authentication.UserDetails;
import io.micronaut.security.token.jwt.generator.JwtTokenGenerator;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;
import pl.altkom.asc.lab.micronaut.poc.product.service.api.v1.ProductDto;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class GatewayBaselineSmokeIT {

    private static final String PRODUCT_RESPONSE = "[{\"code\":\"SMOKE\",\"name\":\"Smoke product\","
            + "\"image\":\"/smoke.png\",\"description\":\"Disposable baseline product\","
            + "\"maxNumberOfInsured\":1,\"covers\":[],\"questions\":[],\"icon\":\"smoke\"}]";

    private final AtomicInteger productRequests = new AtomicInteger();
    private HttpServer productServer;
    private EmbeddedServer gatewayServer;
    private HttpClient gatewayClient;
    private String bearerToken;

    @BeforeAll
    void setup() throws IOException {
        productServer = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
        productServer.createContext("/products", this::respondWithProductCatalog);
        productServer.start();

        byte[] secretBytes = new byte[64];
        new SecureRandom().nextBytes(secretBytes);
        String testSecret = Base64.getEncoder().encodeToString(secretBytes);

        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("micronaut.server.port", -1);
        properties.put("tracing.zipkin.enabled", false);
        properties.put(
                "micronaut.http.services.product-service.urls",
                Collections.singletonList("http://127.0.0.1:" + productServer.getAddress().getPort()));
        properties.put("micronaut.security.token.jwt.signatures.secret.generator.secret", testSecret);

        gatewayServer = ApplicationContext.run(EmbeddedServer.class, properties);
        gatewayClient = gatewayServer.getApplicationContext()
                .createBean(HttpClient.class, gatewayServer.getURL());
        bearerToken = gatewayServer.getApplicationContext().getBean(JwtTokenGenerator.class)
                .generateToken(new UserDetails("baseline-smoke", Collections.singletonList("CAR")), 60)
                .orElseThrow(() -> new IllegalStateException("Could not generate disposable test token"));
    }

    @AfterAll
    void cleanup() {
        if (gatewayClient != null) {
            gatewayClient.close();
        }
        if (gatewayServer != null) {
            gatewayServer.stop();
        }
        if (productServer != null) {
            productServer.stop(0);
        }

        assertThat(gatewayServer).isNotNull();
        assertThat(gatewayServer.isRunning()).isFalse();
    }

    @Nested
    public class GatewayListener {

        @Test
        public void happyPath() {
            // given
            HttpRequest<?> request = HttpRequest.GET("/api/products").bearerAuth(bearerToken);

            // when
            HttpResponse<List<ProductDto>> result = gatewayClient.toBlocking().exchange(
                    request,
                    Argument.listOf(ProductDto.class));

            // then
            assertThat(result.getStatus().getCode()).isEqualTo(HttpStatus.OK.getCode());
            assertThat(result.body()).singleElement().satisfies(product -> {
                assertThat(product.getCode()).isEqualTo("SMOKE");
                assertThat(product.getName()).isEqualTo("Smoke product");
            });
            assertThat(productRequests).hasValue(1);
        }

        @Test
        public void givenMissingAuthentication_thenRejectBeforeCallingProduct() {
            // given
            int requestsBeforeCall = productRequests.get();

            // when
            Throwable result = org.assertj.core.api.Assertions.catchThrowable(
                    () -> gatewayClient.toBlocking().exchange(HttpRequest.GET("/api/products")));

            // then
            assertThat(result).isInstanceOf(HttpClientResponseException.class);
            assertThat(((HttpClientResponseException) result).getStatus().getCode())
                    .isEqualTo(HttpStatus.UNAUTHORIZED.getCode());
            assertThat(productRequests).hasValue(requestsBeforeCall);
        }
    }

    private void respondWithProductCatalog(HttpExchange exchange) throws IOException {
        byte[] response = PRODUCT_RESPONSE.getBytes(StandardCharsets.UTF_8);
        productRequests.incrementAndGet();
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, response.length);
        try (OutputStream body = exchange.getResponseBody()) {
            body.write(response);
        }
    }
}
