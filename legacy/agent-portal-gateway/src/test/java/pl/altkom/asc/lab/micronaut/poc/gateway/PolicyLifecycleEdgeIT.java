package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.micronaut.context.ApplicationContext;
import io.micronaut.runtime.server.EmbeddedServer;
import io.micronaut.security.authentication.UserDetails;
import io.micronaut.security.token.jwt.generator.JwtTokenGenerator;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

import java.io.IOException;
import java.io.OutputStream;
import java.math.BigDecimal;
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
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.Iterator;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;

/**
 * Focused smoke for the authenticated Policy lifecycle routes. The embedded Gateway calls a
 * deterministic private-Policy stub directly. The retry case briefly stops and restarts that stub
 * to exercise a refused connection without additional proxy infrastructure.
 */
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class PolicyLifecycleEdgeIT {

    private static final String PRINCIPAL = "issue-442-gateway-agent";
    private static final String NOT_FOUND = "{\"code\":5,\"message\":\"not found\",\"details\":[]}";
    private static final String CONFLICT = "{\"code\":9,\"message\":\"conflict\",\"details\":[]}";
    private static final String INVALID = "{\"code\":3,\"message\":\"invalid request\",\"details\":[]}";

    private final ObjectMapper objectMapper = new ObjectMapper();
    private final LocalDate today = LocalDate.now(ZoneOffset.UTC);
    private final AtomicReference<JsonNode> lastIssue = new AtomicReference<>();
    private final AtomicInteger policyRequests = new AtomicInteger();
    private final AtomicInteger issueRequests = new AtomicInteger();
    private final AtomicBoolean terminated = new AtomicBoolean();
    private volatile HttpServer stub;
    private int stubPort;
    private EmbeddedServer gateway;
    private HttpClient client;
    private String bearerToken;

    @BeforeAll
    void setup() throws IOException {
        startStub(0);

        byte[] secretBytes = new byte[64];
        new SecureRandom().nextBytes(secretBytes);
        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("micronaut.server.port", -1);
        properties.put("micronaut.server.host", "127.0.0.1");
        properties.put("tracing.zipkin.enabled", false);
        properties.put("micronaut.http.services.policy-service.urls",
                Collections.singletonList("http://127.0.0.1:" + stubPort));
        properties.put("micronaut.http.services.policy-service.read-timeout", "5s");
        properties.put("micronaut.http.services.policy-service.pool.enabled", false);
        properties.put("micronaut.security.token.jwt.signatures.secret.generator.secret",
                Base64.getEncoder().encodeToString(secretBytes));
        gateway = ApplicationContext.run(EmbeddedServer.class, properties);
        bearerToken = gateway.getApplicationContext().getBean(JwtTokenGenerator.class)
                .generateToken(new UserDetails(PRINCIPAL, Collections.singletonList("CAR")), 300)
                .orElseThrow(() -> new IllegalStateException("Could not create disposable test token"));
        client = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(2)).build();
    }

    @AfterAll
    void cleanup() {
        if (gateway != null) {
            gateway.stop();
        }
        if (stub != null) {
            stub.stop(0);
        }
    }

    @BeforeEach
    void resetCounts() {
        lastIssue.set(null);
        policyRequests.set(0);
        issueRequests.set(0);
        terminated.set(false);
    }

    @Test
    void lifecycleKeepsPrivateValuesAndConvertsDates() throws Exception {
        // given
        String offer = createOffer(today, today.plusDays(364));

        // when
        HttpResponse<String> issued = send("POST", "/api/policies/create", issueBody(offer), bearerToken);

        // then
        assertThat(issued.statusCode()).isEqualTo(200);
        String number = singleField(issued.body(), "policyNumber");
        assertThat(policyRequests.get()).isEqualTo(1);
        assertThat(lastIssue.get().path("agentLogin").asText()).isEqualTo(PRINCIPAL);
        assertDetails(number, today, today.plusDays(364));

        // when
        HttpResponse<String> termination = send("POST", "/api/policies/terminate",
                "{\"policyNumber\":\"" + number + "\"}", bearerToken);

        // then
        assertThat(termination.statusCode()).isEqualTo(200);
        assertThat(singleField(termination.body(), "policyNumber")).isEqualTo(number);
        // One of 365 covered days remains: 120.00 x 0.00273972602739726027 = 0.33.
        JsonNode details = assertDetails(number, today.plusDays(1), today.plusDays(364));
        assertThat(details.path("totalPremium").decimalValue()).isEqualByComparingTo("0.33");
    }

    @Test
    void unauthenticatedCallsNeverReachPolicy() throws Exception {
        for (String[] call : new String[][]{
                {"GET", "/api/policies/SYN-442", ""},
                {"POST", "/api/policies/create", issueBody("SYN-OFFER")},
                {"POST", "/api/policies/terminate", "{\"policyNumber\":\"SYN-442\"}"}}) {
            // when
            HttpResponse<String> result = send(call[0], call[1], call[2], null);

            // then
            assertThat(result.statusCode()).as(call[1]).isEqualTo(401);
        }
        assertThat(policyRequests.get()).isZero();
    }

    @Test
    void rejectionsKeepTheSanitizedEnvelopeWithoutRetry() throws Exception {
        // given
        String future = createOffer(today.plusDays(1), today.plusDays(30));
        HttpResponse<String> issued = send("POST", "/api/policies/create", issueBody(future), bearerToken);
        assertThat(issued.statusCode()).isEqualTo(200);
        String futurePolicy = singleField(issued.body(), "policyNumber");

        for (String[] call : new String[][]{
                {"GET", "/api/policies/missing-442", "", "404", NOT_FOUND},
                {"POST", "/api/policies/create", issueBody("missing-offer"), "404", NOT_FOUND},
                {"POST", "/api/policies/create", issueBody(""), "400", INVALID},
                {"POST", "/api/policies/terminate", "{\"policyNumber\":\"missing-442\"}", "404", NOT_FOUND},
                {"POST", "/api/policies/terminate", "{\"policyNumber\":\"" + futurePolicy + "\"}", "409",
                        CONFLICT}}) {
            // given
            policyRequests.set(0);

            // when
            HttpResponse<String> result = send(call[0], call[1], call[2], bearerToken);

            // then
            assertThat(result.statusCode()).as(call[1] + " " + call[2]).isEqualTo(Integer.parseInt(call[3]));
            assertThat(result.body()).as(call[1]).isEqualTo(call[4]);
            assertThat(result.headers().firstValue("Content-Type")).hasValue("application/json");
            assertThat(policyRequests.get()).as("Policy requests for " + call[1]).isEqualTo(1);
        }
    }

    @Test
    void refusedConnectionIsRetriedOnceAndReachesPolicyOnce() throws Exception {
        // given
        String offer = createOffer(today, today.plusDays(364));
        restartStubAfter(Duration.ofSeconds(1));

        // when
        HttpResponse<String> result = send("POST", "/api/policies/create", issueBody(offer), bearerToken);

        // then
        assertThat(result.statusCode()).isEqualTo(200);
        assertThat(issueRequests.get()).isEqualTo(1);
        assertThat(send("GET", "/api/policies/" + singleField(result.body(), "policyNumber"), "",
                bearerToken).statusCode()).isEqualTo(200);
    }

    /** Checks the agent-visible conversion of the stub's private date-array response. */
    private JsonNode assertDetails(String number, LocalDate from, LocalDate to) throws Exception {
        HttpResponse<String> agentResponse = send("GET", "/api/policies/" + number, "", bearerToken);
        assertThat(agentResponse.statusCode()).isEqualTo(200);
        JsonNode agentRoot = objectMapper.readTree(agentResponse.body());
        assertThat(fieldNames(agentRoot)).containsExactly("policy");
        JsonNode agentDetails = agentRoot.path("policy");
        assertThat(fieldNames(agentDetails)).containsExactlyInAnyOrder("number", "dateFrom", "dateTo",
                "policyHolder", "totalPremium", "productCode", "accountNumber", "covers");
        assertThat(agentDetails.path("number").asText()).isEqualTo(number);
        assertThat(agentDetails.path("dateFrom").asText()).isEqualTo(from.toString());
        assertThat(agentDetails.path("dateTo").asText()).isEqualTo(to.toString());
        assertThat(agentDetails.path("policyHolder").asText()).isEqualTo("Synthetic Holder");
        assertThat(agentDetails.path("totalPremium").isNumber()).isTrue();
        assertThat(agentDetails.path("productCode").asText()).isEqualTo("TRI");
        assertThat(agentDetails.path("accountNumber").asText()).isEqualTo("SYN-ACCOUNT");
        assertThat(texts(agentDetails.path("covers"))).allMatch(cover -> cover.matches("C1 - [0-9]+\\.[0-9]{2}"));
        return agentDetails;
    }

    private String createOffer(LocalDate from, LocalDate to) throws Exception {
        HttpResponse<String> result = send("POST", "/api/offers/", "{\"productCode\":\"TRI\",\"policyFrom\":\""
                + from + "\",\"policyTo\":\"" + to + "\",\"selectedCovers\":[\"C1\"],\"answers\":[]}", bearerToken);
        assertThat(result.statusCode()).as(result.body()).isEqualTo(200);
        JsonNode offer = objectMapper.readTree(result.body());
        assertThat(offer.path("totalPrice").decimalValue()).isEqualByComparingTo(new BigDecimal("120.00"));
        return offer.path("offerNumber").asText();
    }

    private String issueBody(String offer) {
        return "{\"offerNumber\":\"" + offer + "\",\"policyHolder\":{\"firstName\":\"Synthetic\","
                + "\"lastName\":\"Holder\",\"taxId\":\"00000000000\"},\"agentLogin\":\"conflicting-login\"}";
    }

    private HttpResponse<String> send(String method, String path, String body, String token) throws Exception {
        HttpRequest.Builder request = HttpRequest.newBuilder()
                .uri(URI.create(gateway.getURL() + path))
                .timeout(Duration.ofSeconds(15))
                .method(method, body.isEmpty()
                        ? HttpRequest.BodyPublishers.noBody()
                        : HttpRequest.BodyPublishers.ofString(body, StandardCharsets.UTF_8));
        if (!body.isEmpty()) {
            request.header("Content-Type", "application/json");
        }
        if (token != null) {
            request.header("Authorization", "Bearer " + token);
        }
        return client.send(request.build(), HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));
    }

    private String singleField(String body, String name) throws IOException {
        JsonNode root = objectMapper.readTree(body);
        assertThat(fieldNames(root)).as(body).containsExactly(name);
        assertThat(root.path(name).asText()).isNotBlank();
        return root.path(name).asText();
    }

    private static List<String> fieldNames(JsonNode node) {
        List<String> names = new ArrayList<>();
        node.fieldNames().forEachRemaining(names::add);
        return names;
    }

    private static List<String> texts(JsonNode array) {
        List<String> values = new ArrayList<>();
        for (Iterator<JsonNode> items = array.elements(); items.hasNext(); ) {
            values.add(items.next().asText());
        }
        return values;
    }

    private static String dateArray(LocalDate date) {
        return "[" + date.getYear() + "," + date.getMonthValue() + "," + date.getDayOfMonth() + "]";
    }

    private void startStub(int port) throws IOException {
        HttpServer server = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), port), 0);
        server.createContext("/", this::respond);
        server.start();
        stub = server;
        stubPort = server.getAddress().getPort();
    }

    private void restartStubAfter(Duration delay) {
        int port = stubPort;
        stub.stop(0);
        Thread restart = new Thread(() -> {
            try {
                Thread.sleep(delay.toMillis());
                startStub(port);
            } catch (InterruptedException exception) {
                Thread.currentThread().interrupt();
            } catch (IOException exception) {
                throw new IllegalStateException(exception);
            }
        }, "policy-stub-restart");
        restart.setDaemon(true);
        restart.start();
    }

    /** The stub returns the private Policy contract for the synthetic numbers used above. */
    private void respond(HttpExchange exchange) throws IOException {
        String path = exchange.getRequestURI().getPath();
        if (path.startsWith("/policies")) {
            policyRequests.incrementAndGet();
        }
        JsonNode body = exchange.getRequestBody().available() > 0 || "POST".equals(exchange.getRequestMethod())
                ? objectMapper.readTree(exchange.getRequestBody().readAllBytes()) : objectMapper.createObjectNode();
        int status = 200;
        String response;
        if ("/offers".equals(path)) {
            boolean future = LocalDate.parse(body.path("policyFrom").asText()).isAfter(today);
            response = "{\"offerNumber\":\"" + (future ? "SYN-OFFER-FUTURE" : "SYN-OFFER")
                    + "\",\"totalPrice\":120.00,\"coversPrices\":{\"C1\":120.00}}";
        } else if ("/policies".equals(path)) {
            issueRequests.incrementAndGet();
            lastIssue.set(body);
            String offer = body.path("offerNumber").asText();
            if (offer.isEmpty()) {
                status = 400;
                response = INVALID;
            } else if (!offer.startsWith("SYN-OFFER")) {
                status = 404;
                response = NOT_FOUND;
            } else {
                response = "{\"policyNumber\":\"" + ("SYN-OFFER-FUTURE".equals(offer) ? "SYN-FUTURE" : "SYN-442")
                        + "\"}";
            }
        } else if ("/policies/terminate".equals(path)) {
            String number = body.path("policyNumber").asText();
            if ("SYN-FUTURE".equals(number)) {
                status = 409;
                response = CONFLICT;
            } else if ("SYN-442".equals(number)) {
                terminated.set(true);
                response = "{\"policyNumber\":\"SYN-442\"}";
            } else {
                status = 404;
                response = NOT_FOUND;
            }
        } else if ("/policies/SYN-442".equals(path)) {
            boolean ended = terminated.get();
            ObjectNode details = objectMapper.createObjectNode();
            details.put("number", "SYN-442");
            details.set("dateFrom", objectMapper.readTree(dateArray(ended ? today.plusDays(1) : today)));
            details.set("dateTo", objectMapper.readTree(dateArray(today.plusDays(364))));
            details.put("policyHolder", "Synthetic Holder");
            details.put("totalPremium", new BigDecimal(ended ? "0.33" : "120.00"));
            details.put("productCode", "TRI");
            details.put("accountNumber", "SYN-ACCOUNT");
            details.putArray("covers").add(ended ? "C1 - 0.33" : "C1 - 120.00");
            response = "{\"policy\":" + details + "}";
        } else {
            status = 404;
            response = NOT_FOUND;
        }
        byte[] bytes = response.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, bytes.length);
        try (OutputStream output = exchange.getResponseBody()) {
            output.write(bytes);
        }
    }
}
