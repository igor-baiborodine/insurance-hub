package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.micronaut.context.ApplicationContext;
import io.micronaut.runtime.server.EmbeddedServer;
import io.micronaut.security.authentication.UserDetails;
import io.micronaut.security.token.jwt.generator.JwtTokenGenerator;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

import java.io.IOException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.time.Duration;
import java.time.LocalDate;
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class DashboardSalesTrendsEdgeIT {

    private final AtomicInteger backendRequests = new AtomicInteger();
    private final AtomicReference<String> backendBody = new AtomicReference<>();
    private HttpServer backend;
    private EmbeddedServer gateway;
    private HttpClient client;
    private String bearerToken;

    @BeforeAll
    void setup() throws IOException {
        String externalBackend = System.getProperty("dashboard.edge.backend.url", "");
        String backendUrl = externalBackend;
        if (externalBackend.isEmpty()) {
            backend = HttpServer.create(
                    new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
            backend.createContext("/", this::respond);
            backend.start();
            backendUrl = "http://127.0.0.1:" + backend.getAddress().getPort();
        }

        byte[] secretBytes = new byte[64];
        new SecureRandom().nextBytes(secretBytes);
        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("micronaut.server.port", -1);
        properties.put("micronaut.server.host", "127.0.0.1");
        properties.put("tracing.zipkin.enabled", false);
        properties.put("micronaut.http.services.dashboard-service.urls",
                Collections.singletonList(backendUrl));
        properties.put("micronaut.http.services.dashboard-service.read-timeout", "1s");
        properties.put("micronaut.security.token.jwt.signatures.secret.generator.secret",
                Base64.getEncoder().encodeToString(secretBytes));
        gateway = ApplicationContext.run(EmbeddedServer.class, properties);
        bearerToken = gateway.getApplicationContext().getBean(JwtTokenGenerator.class)
                .generateToken(new UserDetails("issue-289-step-6", Collections.singletonList("CAR")), 300)
                .orElseThrow(() -> new IllegalStateException("Could not create disposable test token"));
        client = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(2)).build();
    }

    @AfterAll
    void cleanup() {
        if (gateway != null) {
            gateway.stop();
        }
        if (backend != null) {
            backend.stop(0);
        }
    }

    @Test
    void authenticatedVueMonthPathPreservesLegacyDateResponse() throws Exception {
        assertAuthenticatedUnit("MONTH", "[2020,1,31]", "2020-02-01T00:00:00.000Z");
    }

    @Test
    void authenticatedVueWeekPathPreservesLegacyDateResponse() throws Exception {
        assertAuthenticatedUnit("WEEK", "[2020,1,27]", "2020-01-27T00:00:00.000Z");
    }

    @Test
    void authenticatedDayPathPreservesLegacyDateResponse() throws Exception {
        assertAuthenticatedUnit("DAY", "[2020,2,1]", "2020-02-01T00:00:00.000Z");
    }

    @Test
    void authenticatedYearPathPreservesLegacyDateResponse() throws Exception {
        assertAuthenticatedUnit("YEAR", "[2020,1,1]", "2020-01-01T00:00:00.000Z");
    }

    @Test
    void givenNoToken_thenDenyBeforeBackend() throws Exception {
        // given
        backendRequests.set(0);

        // when
        HttpResponse<String> result = post(requestBody("MONTH"), null);

        // then
        assertThat(result.statusCode()).isEqualTo(401);
        assertThat(result.body()).isEmpty();
        if (backend != null) {
            assertThat(backendRequests.get()).isZero();
        }
    }

    private void assertAuthenticatedUnit(String unit, String date, String period) throws Exception {
        // given
        backendRequests.set(0);
        String[] dateParts = date.substring(1, date.length() - 1).split(",");
        String expectedDate = LocalDate.of(
                Integer.parseInt(dateParts[0]),
                Integer.parseInt(dateParts[1]),
                Integer.parseInt(dateParts[2]))
                .toString();

        // when
        HttpResponse<String> result = post(requestBody(unit), bearerToken);

        // then
        assertThat(result.statusCode()).isEqualTo(200);
        assertThat(result.body()).isEqualTo("{\"periodSales\":[{\"periodDate\":\""
                + expectedDate + "\",\"period\":\"" + period
                + "\",\"policiesCount\":2,"
                + "\"premiumAmount\":2000.00}]}");
        if (backend != null) {
            assertThat(backendRequests.get()).isEqualTo(1);
            assertThat(backendBody.get()).contains("\"aggregationUnitCode\":\"" + unit + "\"")
                    .doesNotContain("productCode");
        }
    }

    private String requestBody(String unit) {
        return "{\"productCode\":null,\"saleDateFrom\":\"2020-01-01\","
                + "\"saleDateTo\":\"2020-03-01\",\"aggregationUnitCode\":\"" + unit + "\"}";
    }

    private HttpResponse<String> post(String body, String token) throws Exception {
        HttpRequest.Builder request = HttpRequest.newBuilder()
                .uri(URI.create(gateway.getURL() + "/api/dashboard/trends"))
                .timeout(Duration.ofSeconds(15))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body));
        if (token != null) {
            request.header("Authorization", "Bearer " + token);
        }
        return client.send(
                request.build(), HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));
    }

    private void respond(HttpExchange exchange) throws IOException {
        backendRequests.incrementAndGet();
        String request = new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
        backendBody.set(request);
        String date = "[2020,2,1]";
        String period = "2020-02-01T00:00:00.000Z";
        if (request.contains("\"aggregationUnitCode\":\"MONTH\"")) {
            date = "[2020,1,31]";
        } else if (request.contains("\"aggregationUnitCode\":\"WEEK\"")) {
            date = "[2020,1,27]";
            period = "2020-01-27T00:00:00.000Z";
        } else if (request.contains("\"aggregationUnitCode\":\"YEAR\"")) {
            date = "[2020,1,1]";
            period = "2020-01-01T00:00:00.000Z";
        }
        String body = "{\"periodSales\":[{\"periodDate\":" + date
                + ",\"period\":\"" + period + "\",\"policiesCount\":2,"
                + "\"premiumAmount\":2000.00}]}";
        byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, bytes.length);
        exchange.getResponseBody().write(bytes);
        exchange.close();
    }
}
