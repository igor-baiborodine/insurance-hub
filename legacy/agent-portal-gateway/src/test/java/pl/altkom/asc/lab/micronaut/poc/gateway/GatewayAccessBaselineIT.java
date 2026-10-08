package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.micronaut.context.ApplicationContext;
import io.micronaut.runtime.server.EmbeddedServer;
import io.micronaut.security.token.jwt.generator.JwtTokenGenerator;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.security.SecureRandom;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.Date;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class GatewayAccessBaselineIT {

    private static final String LIST_RESPONSE = "[{\"code\":\"TRI\",\"name\":\"Travel insurance\"}]";
    private static final String GET_RESPONSE = "{\"code\":\"TRI\",\"name\":\"Travel insurance\"}";

    private final ObjectMapper objectMapper = new ObjectMapper();
    private final AtomicInteger backendRequests = new AtomicInteger();
    private final AtomicReference<Boolean> backendAuthorizationPresent = new AtomicReference<>(false);
    private final AtomicReference<List<String>> backendIdentityHeaders = new AtomicReference<>(Collections.emptyList());
    private HttpServer productServer;
    private EmbeddedServer gatewayServer;
    private JwtTokenGenerator tokenGenerator;
    private JsonNode fixture;

    @BeforeAll
    void setup() throws IOException {
        String fixturePath = System.getProperty("baseline.access.fixture");
        assertThat(fixturePath).as("baseline.access.fixture system property").isNotBlank();
        try (InputStream input = Files.newInputStream(Paths.get(fixturePath))) {
            fixture = objectMapper.readTree(input);
        }

        productServer = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
        productServer.createContext("/products", this::respondAsProduct);
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
        tokenGenerator = gatewayServer.getApplicationContext().getBean(JwtTokenGenerator.class);
    }

    @AfterAll
    void cleanup() {
        if (gatewayServer != null) {
            gatewayServer.stop();
        }
        if (productServer != null) {
            productServer.stop(0);
        }
    }

    @Test
    void capturesGatewayAuthenticationAuthorizationAndPropagation() throws Exception {
        // given
        ArrayNode actual = objectMapper.createArrayNode();

        // when
        for (JsonNode testCase : fixture.path("cases")) {
            if (!"gateway".equals(testCase.path("target").asText())) {
                continue;
            }
            backendRequests.set(0);
            backendAuthorizationPresent.set(false);
            backendIdentityHeaders.set(Collections.emptyList());

            RawResponse response = get(testCase.path("path").asText(), tokenFor(testCase));
            ObjectNode observation = objectMapper.createObjectNode();
            observation.put("id", testCase.path("id").asText());
            observation.put("status", response.status);
            observation.put("contentType", response.contentType);
            observation.put("rawBody", response.body);
            observation.put("downstreamRequests", backendRequests.get());
            observation.put("downstreamAuthorizationPresent", backendAuthorizationPresent.get());
            ArrayNode headers = observation.putArray("downstreamIdentityHeaderNames");
            backendIdentityHeaders.get().forEach(headers::add);
            actual.add(observation);
        }

        // then
        if (Boolean.getBoolean("baseline.access.print")) {
            System.out.println("GATEWAY_ACCESS_OBSERVATIONS=" + actual);
        }
        assertExpected(actual, "gateway");
    }

    private String tokenFor(JsonNode testCase) {
        String credential = testCase.path("credential").asText();
        if ("missing".equals(credential)) {
            return null;
        }
        if ("malformed".equals(credential)) {
            return "not-a-jwt";
        }

        Instant now = Instant.now();
        Map<String, Object> claims = new HashMap<>();
        if (!"missing-subject".equals(credential)) {
            claims.put("sub", "baseline-access");
        }
        claims.put("roles", "role-variation".equals(credential)
                ? Collections.singletonList("UNRELATED") : Collections.singletonList("CAR"));
        claims.put("iss", "issuer-variation".equals(credential) ? "different-issuer" : "auth-service");
        claims.put("iat", Date.from(now));
        claims.put("nbf", Date.from("not-before".equals(credential) ? now.plusSeconds(300) : now.minusSeconds(5)));
        claims.put("exp", Date.from("expired".equals(credential) ? now.minusSeconds(60) : now.plusSeconds(300)));
        if ("audience-variation".equals(credential)) {
            claims.put("aud", Collections.singletonList("different-audience"));
        }

        String token = tokenGenerator.generateToken(claims)
                .orElseThrow(() -> new IllegalStateException("Could not generate disposable access token"));
        return "invalid-signature".equals(credential) ? corruptSignature(token) : token;
    }

    private String corruptSignature(String token) {
        int signatureStart = token.lastIndexOf('.') + 1;
        char first = token.charAt(signatureStart);
        char replacement = first == 'A' ? 'B' : 'A';
        return token.substring(0, signatureStart) + replacement + token.substring(signatureStart + 1);
    }

    private RawResponse get(String path, String token) throws IOException {
        URL url = new URL(gatewayServer.getURL().toString() + path);
        HttpURLConnection connection = (HttpURLConnection) url.openConnection();
        connection.setRequestMethod("GET");
        connection.setRequestProperty("Accept", "application/json");
        if (token != null) {
            connection.setRequestProperty("Authorization", "Bearer " + token);
        }
        connection.setConnectTimeout(2000);
        connection.setReadTimeout(10000);
        int status = connection.getResponseCode();
        InputStream stream = status >= 400 ? connection.getErrorStream() : connection.getInputStream();
        String body = stream == null ? "" : new String(readAll(stream), StandardCharsets.UTF_8);
        String contentType = connection.getContentType();
        connection.disconnect();
        return new RawResponse(status, contentType == null ? "" : contentType.split(";")[0], body);
    }

    private void respondAsProduct(HttpExchange exchange) throws IOException {
        backendRequests.incrementAndGet();
        backendAuthorizationPresent.set(exchange.getRequestHeaders().containsKey("Authorization"));
        List<String> names = new ArrayList<>();
        for (String name : exchange.getRequestHeaders().keySet()) {
            String lower = name.toLowerCase();
            if (lower.equals("authorization") || lower.startsWith("x-user") || lower.startsWith("x-principal")
                    || lower.startsWith("x-role") || lower.startsWith("x-auth")) {
                names.add(lower);
            }
        }
        Collections.sort(names);
        backendIdentityHeaders.set(names);

        String responseBody = exchange.getRequestURI().getPath().endsWith("/TRI") ? GET_RESPONSE : LIST_RESPONSE;
        byte[] response = responseBody.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, response.length);
        exchange.getResponseBody().write(response);
        exchange.close();
    }

    private byte[] readAll(InputStream input) throws IOException {
        try (InputStream stream = input; ByteArrayOutputStream output = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[4096];
            int count;
            while ((count = stream.read(buffer)) >= 0) {
                output.write(buffer, 0, count);
            }
            return output.toByteArray();
        }
    }

    private void assertExpected(ArrayNode actual, String target) {
        int actualIndex = 0;
        for (JsonNode testCase : fixture.path("cases")) {
            if (!target.equals(testCase.path("target").asText())) {
                continue;
            }
            JsonNode expected = testCase.path("expected");
            if (expected.isNull() || expected.isMissingNode()) {
                assertThat(Boolean.getBoolean("baseline.access.print"))
                        .as("expected observation for %s", testCase.path("id").asText()).isTrue();
            } else {
                ObjectNode observation = actual.get(actualIndex).deepCopy();
                observation.remove("id");
                assertThat(observation).isEqualTo(expected);
            }
            actualIndex++;
        }
    }

    private static final class RawResponse {
        private final int status;
        private final String contentType;
        private final String body;

        private RawResponse(int status, String contentType, String body) {
            this.status = status;
            this.contentType = contentType;
            this.body = body;
        }
    }
}
