package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import ch.qos.logback.classic.Logger;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.core.read.ListAppender;
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
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;
import static org.slf4j.LoggerFactory.getLogger;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class DashboardAgentSalesEdgeIT {

    private final AtomicInteger backendRequests = new AtomicInteger();
    private final AtomicReference<String> backendMode = new AtomicReference<>("success");
    private final AtomicReference<String> backendBody = new AtomicReference<>();
    private HttpServer backend;
    private EmbeddedServer gateway;
    private HttpClient client;
    private String bearerToken;
    private Logger rootLogger;
    private ListAppender<ILoggingEvent> logEvents;

    @BeforeAll
    void setup() throws IOException {
        String externalBackend = System.getProperty("dashboard.edge.backend.url", "");
        String backendUrl = externalBackend;
        if (externalBackend.isEmpty()) {
            backend = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
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
                .generateToken(new UserDetails("issue-288-step-6", Collections.singletonList("CAR")), 300)
                .orElseThrow(() -> new IllegalStateException("Could not create disposable test token"));
        client = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(2)).build();
        logEvents = new ListAppender<>();
        logEvents.start();
        rootLogger = (Logger) getLogger(Logger.ROOT_LOGGER_NAME);
        rootLogger.addAppender(logEvents);
    }

    @AfterAll
    void cleanup() {
        if (rootLogger != null && logEvents != null) {
            rootLogger.detachAppender(logEvents);
            logEvents.stop();
        }
        if (gateway != null) {
            gateway.stop();
        }
        if (backend != null) {
            backend.stop(0);
        }
    }

    @Test
    void authenticatedPathPreservesFiltersAndNumericResponse() throws Exception {
        // given
        String requestBody = "{\"agentLogin\":\"m.smith\",\"productCode\":null,"
                + "\"saleDateFrom\":\"2019-01-01\",\"saleDateTo\":\"2020-01-01\","
                + "\"aggregationUnitCode\":\"MONTH\"}";

        // when
        backendMode.set("success");
        backendRequests.set(0);
        HttpResponse<String> result = post(requestBody, bearerToken);

        // then
        assertThat(result.statusCode()).isEqualTo(200);
        assertThat(result.body()).isEqualTo("{\"perAgentTotal\":{\"m.smith\":{"
                + "\"policiesCount\":3,\"premiumAmount\":3000.0}}}");
        if (backend != null) {
            assertThat(backendRequests.get()).isEqualTo(1);
            assertThat(backendBody.get()).contains("\"agentLogin\":\"m.smith\"")
                    .doesNotContain("productCode")
                    .doesNotContain("aggregationUnitCode");
        }
    }

    @Test
    void givenNoToken_thenDenyBeforeBackend() throws Exception {
        // given
        String requestBody = "{\"saleDateFrom\":\"2019-01-01\","
                + "\"saleDateTo\":\"2020-01-01\"}";
        backendRequests.set(0);

        // when
        HttpResponse<String> result = post(requestBody, null);

        // then
        assertThat(result.statusCode()).isEqualTo(401);
        assertThat(result.body()).isEmpty();
        if (backend != null) {
            assertThat(backendRequests.get()).isZero();
        }
    }

    @Test
    void givenInvalidBody_thenReturnSafeErrorBeforeBackend() throws Exception {
        // given
        backendRequests.set(0);

        // when
        HttpResponse<String> result = post(
                "{\"agentLogin\":[],\"saleDateFrom\":\"2019-01-01\","
                        + "\"saleDateTo\":\"2020-01-01\"}",
                bearerToken);

        // then
        assertThat(result.statusCode()).isEqualTo(400);
        assertThat(result.body()).contains("\"message\":\"invalid request\"")
                .doesNotContain("ArrayList");
        if (backend != null) {
            assertThat(backendRequests.get()).isZero();
        }
    }

    @Test
    void givenSyntheticBackendFailure_thenRetryAndSanitize() throws Exception {
        if (backend == null) {
            return;
        }
        // given
        backendMode.set("failure");
        backendRequests.set(0);

        // when
        HttpResponse<String> result = post(
                "{\"saleDateFrom\":\"2019-01-01\",\"saleDateTo\":\"2020-01-01\"}",
                bearerToken);

        // then
        assertThat(result.statusCode()).isEqualTo(500);
        assertThat(result.body()).contains("\"message\":\"internal error\"")
                .doesNotContain("private-agent-upstream-marker");
        assertThat(backendRequests.get()).isEqualTo(3);
        assertThat(logEvents.list).allSatisfy(event -> {
            assertThat(event.getFormattedMessage()).doesNotContain("private-agent-upstream-marker");
            if (event.getThrowableProxy() != null) {
                assertThat(event.getThrowableProxy().getMessage())
                        .doesNotContain("private-agent-upstream-marker");
            }
        });
    }

    private HttpResponse<String> post(String body, String token) throws Exception {
        HttpRequest.Builder request = HttpRequest.newBuilder()
                .uri(URI.create(gateway.getURL() + "/api/dashboard/agentssales"))
                .timeout(Duration.ofSeconds(15))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body));
        if (token != null) {
            request.header("Authorization", "Bearer " + token);
        }
        return client.send(request.build(), HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));
    }

    private void respond(HttpExchange exchange) throws IOException {
        backendRequests.incrementAndGet();
        backendBody.set(new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
        int status = "failure".equals(backendMode.get()) ? 500 : 200;
        String body = status == 200
                ? "{\"perAgentTotal\":{\"m.smith\":{\"policiesCount\":3,"
                + "\"premiumAmount\":3000.0}}}"
                : "{\"message\":\"private-agent-upstream-marker\"}";
        byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, bytes.length);
        exchange.getResponseBody().write(bytes);
        exchange.close();
    }
}
