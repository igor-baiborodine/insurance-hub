package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
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

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.SecureRandom;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;
import static org.slf4j.LoggerFactory.getLogger;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class GatewayFailureBaselineIT {

    private final ObjectMapper objectMapper = new ObjectMapper();
    private final AtomicInteger backendRequests = new AtomicInteger();
    private final List<String> backendPaths = Collections.synchronizedList(new ArrayList<>());
    private final AtomicReference<JsonNode> activeCase = new AtomicReference<>();
    private HttpServer productServer;
    private ServerSocket unavailableBackend;
    private Thread unavailableBackendThread;
    private int productPort;
    private EmbeddedServer gatewayServer;
    private String bearerToken;
    private JsonNode fixture;
    private Logger fallbackLogger;
    private ListAppender<ILoggingEvent> fallbackEvents;

    @BeforeAll
    void setup() throws IOException {
        String fixturePath = System.getProperty("baseline.failure.fixture");
        assertThat(fixturePath).as("baseline.failure.fixture system property").isNotBlank();
        try (InputStream input = Files.newInputStream(Path.of(fixturePath))) {
            fixture = objectMapper.readTree(input);
        }

        productServer = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
        productServer.createContext("/", this::respondFromActiveCase);
        productServer.start();
        productPort = productServer.getAddress().getPort();

        byte[] secretBytes = new byte[64];
        new SecureRandom().nextBytes(secretBytes);
        String testSecret = Base64.getEncoder().encodeToString(secretBytes);

        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("micronaut.server.port", -1);
        properties.put("tracing.zipkin.enabled", false);
        properties.put("micronaut.http.services.product-service.urls",
                Collections.singletonList("http://127.0.0.1:" + productPort));
        properties.put("micronaut.http.services.product-service.read-timeout", "1s");
        properties.put("micronaut.security.token.jwt.signatures.secret.generator.secret", testSecret);
        gatewayServer = ApplicationContext.run(EmbeddedServer.class, properties);
        bearerToken = gatewayServer.getApplicationContext().getBean(JwtTokenGenerator.class)
                .generateToken(new UserDetails("baseline-failures", Collections.singletonList("CAR")), 300)
                .orElseThrow(() -> new IllegalStateException("Could not generate disposable test token"));

        fallbackEvents = new ListAppender<>();
        fallbackEvents.start();
        fallbackLogger = (Logger) getLogger("pl.altkom.asc.lab.micronaut.poc.gateway.client.v1.fallback"
                + ".ProductGatewayClientFallback");
        fallbackLogger.addAppender(fallbackEvents);
    }

    @AfterAll
    void cleanup() {
        if (fallbackLogger != null && fallbackEvents != null) {
            fallbackLogger.detachAppender(fallbackEvents);
            fallbackEvents.stop();
        }
        if (gatewayServer != null) {
            gatewayServer.stop();
        }
        if (productServer != null) {
            productServer.stop(0);
        }
        closeUnavailableBackend();
    }

    @Test
    void capturesGatewayFailuresRetriesAndFallbacks() throws Exception {
        ArrayNode actual = objectMapper.createArrayNode();
        boolean backendStopped = false;

        for (JsonNode testCase : fixture.path("cases")) {
            if (!"gateway".equals(testCase.path("target").asText())) {
                continue;
            }
            activeCase.set(testCase);
            backendRequests.set(0);
            backendPaths.clear();
            fallbackEvents.list.clear();
            if (testCase.path("backendUnavailable").asBoolean(false) && !backendStopped) {
                productServer.stop(0);
                productServer = null;
                startUnavailableBackend();
                backendStopped = true;
            }

            RawResponse response = rawGet(testCase.path("rawPath").asText());
            ObjectNode observation = objectMapper.createObjectNode();
            observation.put("id", testCase.path("id").asText());
            observation.put("status", response.status);
            observation.put("contentType", response.contentType);
            observation.put("rawBody", response.body);
            observation.put("downstreamAttempts", backendRequests.get());
            observation.put("fallbackInvocations", fallbackEvents.list.size());
            ArrayNode paths = observation.putArray("downstreamRawPaths");
            synchronized (backendPaths) {
                backendPaths.forEach(paths::add);
            }
            actual.add(observation);
        }

        if (Boolean.getBoolean("baseline.failures.print")) {
            System.out.println("GATEWAY_FAILURE_OBSERVATIONS=" + actual);
        }
        assertExpected(actual);
    }

    private void respondFromActiveCase(HttpExchange exchange) throws IOException {
        JsonNode testCase = activeCase.get();
        backendRequests.incrementAndGet();
        backendPaths.add(exchange.getRequestURI().toASCIIString());
        byte[] body = testCase.path("backendBody").asText("").getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(testCase.path("backendStatus").asInt(500), body.length);
        try (java.io.OutputStream response = exchange.getResponseBody()) {
            response.write(body);
        }
    }

    private void startUnavailableBackend() throws IOException {
        unavailableBackend = new ServerSocket();
        unavailableBackend.setReuseAddress(true);
        unavailableBackend.bind(new InetSocketAddress(InetAddress.getLoopbackAddress(), productPort));
        unavailableBackendThread = new Thread(() -> {
            while (!unavailableBackend.isClosed()) {
                try (Socket ignored = unavailableBackend.accept()) {
                    backendRequests.incrementAndGet();
                } catch (IOException exception) {
                    if (!unavailableBackend.isClosed()) {
                        throw new IllegalStateException(exception);
                    }
                }
            }
        }, "product-unavailable-backend");
        unavailableBackendThread.setDaemon(true);
        unavailableBackendThread.start();
    }

    private void closeUnavailableBackend() {
        if (unavailableBackend != null) {
            try {
                unavailableBackend.close();
            } catch (IOException ignored) {
                // Test-owned socket is already closed.
            }
        }
        if (unavailableBackendThread != null) {
            try {
                unavailableBackendThread.join(2000);
            } catch (InterruptedException exception) {
                Thread.currentThread().interrupt();
            }
        }
    }

    private void assertExpected(ArrayNode actual) {
        int actualIndex = 0;
        for (JsonNode testCase : fixture.path("cases")) {
            if (!"gateway".equals(testCase.path("target").asText())) {
                continue;
            }
            JsonNode expected = testCase.path("expected");
            if (expected.isNull() || expected.isMissingNode()) {
                assertThat(Boolean.getBoolean("baseline.failures.print"))
                        .as("expected observation for %s", testCase.path("id").asText())
                        .isTrue();
            } else {
                ObjectNode observation = actual.get(actualIndex).deepCopy();
                observation.remove("id");
                assertThat(observation).isEqualTo(expected);
            }
            actualIndex++;
        }
    }

    private RawResponse rawGet(String path) throws IOException {
        InetSocketAddress address = new InetSocketAddress(gatewayServer.getHost(), gatewayServer.getPort());
        try (Socket socket = new Socket()) {
            socket.connect(address, 2000);
            socket.setSoTimeout(35000);
            String request = "GET " + path + " HTTP/1.1\r\nHost: " + gatewayServer.getHost() + ":"
                    + gatewayServer.getPort() + "\r\nAccept: application/json\r\nAuthorization: Bearer "
                    + bearerToken + "\r\nConnection: close\r\n\r\n";
            socket.getOutputStream().write(request.getBytes(StandardCharsets.US_ASCII));
            socket.getOutputStream().flush();
            return RawResponse.parse(readAll(socket.getInputStream()));
        }
    }

    private byte[] readAll(InputStream input) throws IOException {
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        byte[] buffer = new byte[4096];
        int count;
        while ((count = input.read(buffer)) >= 0) {
            output.write(buffer, 0, count);
        }
        return output.toByteArray();
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

        private static RawResponse parse(byte[] bytes) {
            String response = new String(bytes, StandardCharsets.UTF_8);
            int separator = response.indexOf("\r\n\r\n");
            assertThat(separator).as("HTTP header terminator").isGreaterThanOrEqualTo(0);
            String headers = response.substring(0, separator);
            String[] lines = headers.split("\r\n");
            int status = Integer.parseInt(lines[0].split(" ")[1]);
            String contentType = "";
            boolean chunked = false;
            for (String line : lines) {
                if (line.regionMatches(true, 0, "Content-Type:", 0, 13)) {
                    contentType = line.substring(13).trim().split(";")[0];
                }
                if (line.equalsIgnoreCase("Transfer-Encoding: chunked")) {
                    chunked = true;
                }
            }
            String body = response.substring(separator + 4);
            return new RawResponse(status, contentType, chunked ? decodeChunked(body) : body);
        }

        private static String decodeChunked(String value) {
            StringBuilder decoded = new StringBuilder();
            int offset = 0;
            while (offset < value.length()) {
                int lineEnd = value.indexOf("\r\n", offset);
                int length = Integer.parseInt(value.substring(offset, lineEnd).trim(), 16);
                if (length == 0) {
                    break;
                }
                int contentStart = lineEnd + 2;
                decoded.append(value, contentStart, contentStart + length);
                offset = contentStart + length + 2;
            }
            return decoded.toString();
        }
    }
}
