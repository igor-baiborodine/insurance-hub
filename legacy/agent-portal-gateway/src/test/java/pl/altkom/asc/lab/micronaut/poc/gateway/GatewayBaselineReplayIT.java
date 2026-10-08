package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.micronaut.context.ApplicationContext;
import io.micronaut.http.HttpRequest;
import io.micronaut.http.HttpResponse;
import io.micronaut.http.client.HttpClient;
import io.micronaut.runtime.server.EmbeddedServer;
import io.micronaut.security.authentication.UserDetails;
import io.micronaut.security.token.jwt.generator.JwtTokenGenerator;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.SecureRandom;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class GatewayBaselineReplayIT {

    private final ObjectMapper objectMapper = new ObjectMapper();
    private final Map<String, String> directResponses = new HashMap<>();
    private final AtomicInteger downstreamRequests = new AtomicInteger();
    private JsonNode fixture;
    private HttpServer productServer;
    private EmbeddedServer gatewayServer;
    private HttpClient classUnderTest;
    private String bearerToken;

    @BeforeAll
    void setup() throws IOException {
        String fixtureSet = System.getProperty("baseline.fixture.set");
        assertThat(fixtureSet).as("baseline.fixture.set").isIn("local-dev", "qa");
        String fixtureFile = System.getProperty("baseline.fixture.file");
        assertThat(fixtureFile).as("baseline.fixture.file").isNotBlank();
        try (InputStream input = Files.newInputStream(Path.of(fixtureFile))) {
            fixture = objectMapper.readTree(input);
        }
        fixture.path("observations").forEach(observation -> {
            if ("direct".equals(observation.path("boundary").asText())) {
                directResponses.put(
                        observation.path("request").path("path").asText(),
                        observation.path("response").path("rawBody").asText());
            }
        });

        productServer = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
        productServer.createContext("/products", this::respondWithAcceptedProductObservation);
        productServer.start();

        byte[] secretBytes = new byte[64];
        new SecureRandom().nextBytes(secretBytes);
        String testSecret = Base64.getEncoder().encodeToString(secretBytes);
        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("micronaut.server.port", -1);
        properties.put("tracing.zipkin.enabled", false);
        properties.put("micronaut.http.services.product-service.urls",
                Collections.singletonList("http://127.0.0.1:" + productServer.getAddress().getPort()));
        properties.put("micronaut.security.token.jwt.signatures.secret.generator.secret", testSecret);

        gatewayServer = ApplicationContext.run(EmbeddedServer.class, properties);
        classUnderTest = gatewayServer.getApplicationContext().createBean(HttpClient.class, gatewayServer.getURL());
        bearerToken = gatewayServer.getApplicationContext().getBean(JwtTokenGenerator.class)
                .generateToken(new UserDetails("baseline-replay", Collections.singletonList("CAR")), 300)
                .orElseThrow(() -> new IllegalStateException("Could not generate disposable replay token"));
    }

    @AfterAll
    void cleanup() {
        if (classUnderTest != null) {
            classUnderTest.close();
        }
        if (gatewayServer != null) {
            gatewayServer.stop();
        }
        if (productServer != null) {
            productServer.stop(0);
        }
    }

    @Nested
    public class AcceptedGatewayObservations {

        @Test
        public void happyPath() throws IOException {
            // given
            List<JsonNode> accepted = new ArrayList<>();
            fixture.path("observations").forEach(observation -> {
                if ("gateway".equals(observation.path("boundary").asText())) {
                    accepted.add(observation);
                }
            });

            // when
            List<HttpResponse<String>> result = new ArrayList<>();
            for (JsonNode observation : accepted) {
                result.add(classUnderTest.toBlocking().exchange(
                        HttpRequest.GET(observation.path("request").path("path").asText())
                                .bearerAuth(bearerToken),
                        String.class));
            }

            // then
            assertThat(result).hasSameSizeAs(accepted);
            for (int index = 0; index < accepted.size(); index++) {
                JsonNode expected = accepted.get(index);
                HttpResponse<String> actual = result.get(index);
                assertThat(actual.getStatus().getCode())
                        .as(expected.path("scenarioId").asText() + " status")
                        .isEqualTo(expected.path("response").path("status").asInt());
                assertThat(actual.getContentType().map(Object::toString).orElse(""))
                        .as(expected.path("scenarioId").asText() + " content type")
                        .isEqualTo(expected.path("response").path("headers").path("contentType").asText());
                assertThat(normalizeBody(actual.body()))
                        .as(expected.path("scenarioId").asText() + " response body")
                        .isEqualTo(normalizeBody(expected.path("response").path("rawBody").asText()));
            }
            assertThat(downstreamRequests).hasValue(accepted.size());
        }
    }

    private JsonNode normalizeBody(String rawBody) throws IOException {
        JsonNode result = objectMapper.readTree(rawBody);
        if (result.isArray()) {
            List<JsonNode> values = new ArrayList<>();
            result.forEach(values::add);
            values.sort(Comparator.comparing(value -> value.path("code").asText()));
            ArrayNode sorted = objectMapper.createArrayNode();
            values.forEach(sorted::add);
            return sorted;
        }
        return result;
    }

    private void respondWithAcceptedProductObservation(HttpExchange exchange) throws IOException {
        String body = directResponses.get(exchange.getRequestURI().getRawPath());
        if (body == null) {
            exchange.sendResponseHeaders(404, -1);
            exchange.close();
            return;
        }
        byte[] response = body.getBytes(StandardCharsets.UTF_8);
        downstreamRequests.incrementAndGet();
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, response.length);
        try (OutputStream output = exchange.getResponseBody()) {
            output.write(response);
        }
    }
}
