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
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class DocumentLookupEdgeIT {

    private final AtomicInteger backendRequests = new AtomicInteger();
    private final AtomicReference<String> backendMode = new AtomicReference<>("success");
    private HttpServer backend;
    private EmbeddedServer gateway;
    private HttpClient client;
    private String bearerToken;

    @BeforeAll
    void setup() throws IOException {
        String externalBackend = System.getProperty("document.edge.backend.url", "");
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
        properties.put("micronaut.http.services.document-service.urls",
                Collections.singletonList(backendUrl));
        properties.put("micronaut.http.services.document-service.read-timeout", "1s");
        properties.put("micronaut.security.token.jwt.signatures.secret.generator.secret",
                Base64.getEncoder().encodeToString(secretBytes));
        gateway = ApplicationContext.run(EmbeddedServer.class, properties);
        bearerToken = gateway.getApplicationContext().getBean(JwtTokenGenerator.class)
                .generateToken(new UserDetails("issue-362-step-4", Collections.singletonList("CAR")), 300)
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
    void authenticatedLookupPreservesBytesAndExistingFallback() throws Exception {
        // given
        backendMode.set("success");
        backendRequests.set(0);

        // when
        HttpResponse<String> result = get(bearerToken);

        // then
        assertThat(result.statusCode()).isEqualTo(200);
        assertThat(result.body()).isEqualTo("{\"documents\":[{\"policyNumber\":\"SYN-362\","
                + "\"content\":\"JVBERi1PQkpFQ1QtMzYy\"}]}");
        if (backend != null) {
            assertThat(backendRequests.get()).isEqualTo(1);
        }

        // given
        backendRequests.set(0);

        // when
        HttpResponse<String> unauthenticated = get(null);

        // then
        assertThat(unauthenticated.statusCode()).isEqualTo(401);
        assertThat(unauthenticated.body()).isEmpty();
        if (backend != null) {
            assertThat(backendRequests.get()).isZero();
        }

        if (backend != null) {
            // given
            backendMode.set("failure");
            backendRequests.set(0);

            // when
            HttpResponse<String> fallback = get(bearerToken);

            // then
            assertThat(fallback.statusCode()).isEqualTo(200);
            assertThat(fallback.body()).isEqualTo("{}");
            assertThat(backendRequests.get()).isEqualTo(3);
        }
    }

    private HttpResponse<String> get(String token) throws Exception {
        HttpRequest.Builder request = HttpRequest.newBuilder()
                .uri(URI.create(gateway.getURL() + "/api/documents/SYN-362"))
                .timeout(Duration.ofSeconds(15))
                .GET();
        if (token != null) {
            request.header("Authorization", "Bearer " + token);
        }
        return client.send(request.build(), HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));
    }

    private void respond(HttpExchange exchange) throws IOException {
        backendRequests.incrementAndGet();
        int status = "failure".equals(backendMode.get()) ? 500 : 200;
        String body = status == 200
                ? "{\"documents\":[{\"policyNumber\":\"SYN-362\","
                + "\"content\":\"JVBERi1PQkpFQ1QtMzYy\"}]}"
                : "{\"message\":\"synthetic-upstream-failure\"}";
        byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, bytes.length);
        exchange.getResponseBody().write(bytes);
        exchange.close();
    }
}
