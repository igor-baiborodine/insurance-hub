package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
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
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.Comparator;
import java.util.Date;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class GatewayProductSecurityIT {

    private static final Set<String> HOP_HEADERS = Set.of(
            "connection", "content-length", "host", "keep-alive", "proxy-authenticate",
            "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade");
    private static final Set<String> REJECTED_CREDENTIALS = Set.of(
            "missing", "malformed", "invalid-signature", "expired", "missing-subject");
    private static final Map<String, String> REJECTED_CASES = Map.ofEntries(
            Map.entry("ACCESS-GATEWAY-LIST-MISSING-001", "missing"),
            Map.entry("ACCESS-GATEWAY-LIST-MALFORMED-001", "malformed"),
            Map.entry("ACCESS-GATEWAY-LIST-SIGNATURE-001", "invalid-signature"),
            Map.entry("ACCESS-GATEWAY-LIST-EXPIRED-001", "expired"),
            Map.entry("ACCESS-GATEWAY-LIST-SUBJECT-001", "missing-subject"),
            Map.entry("ACCESS-GATEWAY-GET-MISSING-001", "missing"),
            Map.entry("ACCESS-GATEWAY-GET-MALFORMED-001", "malformed"),
            Map.entry("ACCESS-GATEWAY-GET-SIGNATURE-001", "invalid-signature"),
            Map.entry("ACCESS-GATEWAY-GET-EXPIRED-001", "expired"),
            Map.entry("ACCESS-GATEWAY-GET-SUBJECT-001", "missing-subject"));

    private final ObjectMapper mapper = new ObjectMapper()
            .enable(DeserializationFeature.USE_BIG_DECIMAL_FOR_FLOATS);
    private final AtomicInteger forwardedRequests = new AtomicInteger();
    private final AtomicReference<List<String>> observedHeaderNames =
            new AtomicReference<>(Collections.emptyList());
    private HttpServer observer;
    private EmbeddedServer gateway;
    private String goHTTP;
    private String bearerToken;
    private JsonNode acceptedAccess;
    private JsonNode accessCases;
    private JwtTokenGenerator tokenGenerator;

    @BeforeAll
    void setup() throws IOException {
        String endpointFile = System.getProperty("product.security.endpoint.file");
        String qaAccessFile = System.getProperty("product.security.qa.access.file");
        String casesFile = System.getProperty("product.security.cases.file");
        assertThat(endpointFile).as("product.security.endpoint.file").isNotBlank();
        assertThat(qaAccessFile).as("product.security.qa.access.file").isNotBlank();
        assertThat(casesFile).as("product.security.cases.file").isNotBlank();
        JsonNode endpoint;
        try (InputStream input = Files.newInputStream(Paths.get(endpointFile))) {
            endpoint = mapper.readTree(input);
        }
        assertThat(endpoint.path("nonce").asText()).hasSize(32);
        assertThat(endpoint.path("fixture").asText()).isEqualTo("DATA-QA-001");
        assertThat(endpoint.path("rowCount").asInt()).isEqualTo(4);
        assertThat(endpoint.path("dataIdentity").asText())
                .isEqualTo("7ba18949c152e2979df14a28a67cba7f");
        assertThat(endpoint.path("reader").asText()).isEqualTo("go_product_reader");
        goHTTP = endpoint.path("http").asText();
        assertThat(goHTTP).startsWith("127.0.0.1:");
        try (InputStream input = Files.newInputStream(Paths.get(qaAccessFile))) {
            acceptedAccess = mapper.readTree(input);
        }
        assertThat(acceptedAccess.path("environment").asText()).isEqualTo("qa");
        try (InputStream input = Files.newInputStream(Paths.get(casesFile))) {
            accessCases = mapper.readTree(input);
        }

        observer = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
        observer.createContext("/products", this::forwardToGo);
        observer.start();

        byte[] signingMaterial = new byte[64];
        new SecureRandom().nextBytes(signingMaterial);
        String testSecret = Base64.getEncoder().encodeToString(signingMaterial);
        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("micronaut.server.port", -1);
        properties.put("tracing.zipkin.enabled", false);
        properties.put("micronaut.http.services.product-service.urls",
                Collections.singletonList("http://127.0.0.1:" + observer.getAddress().getPort()));
        properties.put("micronaut.security.token.jwt.signatures.secret.generator.secret", testSecret);
        gateway = ApplicationContext.run(EmbeddedServer.class, properties);
        tokenGenerator = gateway.getApplicationContext().getBean(JwtTokenGenerator.class);
        bearerToken = tokenGenerator
                .generateToken(new UserDetails("security-smoke", Collections.singletonList("UNRELATED")), 600)
                .orElseThrow(() -> new IllegalStateException("Could not generate disposable token"));
    }

    @AfterAll
    void cleanup() {
        if (gateway != null) {
            gateway.stop();
        }
        if (observer != null) {
            observer.stop(0);
        }
    }

    @Test
    void happyPath() throws IOException {
        // given
        assertThat(gateway.isRunning()).isTrue();
        forwardedRequests.set(0);

        // when
        System.out.println("PRODUCT_SECURITY_SMOKE_LIST_START");
        RawResponse list = get("/api/products");
        RawResponse get = get("/api/products/TRI");

        // then
        assertThat(list.status).isEqualTo(200);
        assertThat(get.status).isEqualTo(200);
        assertThat(list.contentType).isEqualTo("application/json");
        assertThat(get.contentType).isEqualTo("application/json");
        assertThat(forwardedRequests.get()).isEqualTo(2);
        JsonNode listBody = mapper.readTree(list.body);
        assertThat(listBody.isArray()).isTrue();
        assertThat(listBody.size()).isEqualTo(4);
        List<String> codes = new ArrayList<>();
        listBody.forEach(product -> codes.add(product.path("code").asText()));
        Collections.sort(codes);
        assertThat(codes).containsExactly("CAR", "FAI", "HSI", "TRI");
        assertThat(mapper.readTree(get.body).path("name").asText()).isEqualTo("Safe Traveller");
        assertThat(normalizeList(listBody)).isEqualTo(normalizeList(acceptedBody("ACCESS-GATEWAY-LIST-VALID-001")));
        assertThat(mapper.readTree(get.body)).isEqualTo(acceptedBody("ACCESS-GATEWAY-GET-VALID-001"));
        assertThat(observedHeaderNames.get()).isNotEmpty();
    }

    @Test
    void rejectedCredentialsStopAtGateway() throws IOException {
        // given
        Map<String, JsonNode> cases = rejectedCasesByID();
        Instant reference = Instant.now();

        for (Map.Entry<String, JsonNode> entry : cases.entrySet()) {
            // given
            String id = entry.getKey();
            JsonNode testCase = entry.getValue();
            String path = testCase.path("path").asText();
            String credential = testCase.path("credential").asText();
            assertThat(path).as("%s route", id).isEqualTo(
                    id.contains("-LIST-") ? "/api/products" : "/api/products/TRI");
            assertThat(credential).as("%s category", id).isEqualTo(REJECTED_CASES.get(id));
            assertThat(testCase.path("expected").path("status").asInt()).as("%s fixture status", id)
                    .isEqualTo(401);
            assertThat(testCase.path("expected").path("rawBody").asText()).as("%s fixture body", id)
                    .isEmpty();
            assertThat(testCase.path("expected").path("contentType").asText()).as("%s fixture type", id)
                    .isEmpty();
            assertThat(testCase.path("expected").path("downstreamRequests").asInt())
                    .as("%s fixture downstream count", id).isZero();
            assertThat(Duration.between(reference, Instant.now()).abs())
                    .as("%s bounded token clock drift", id).isLessThan(Duration.ofSeconds(120));
            forwardedRequests.set(0);
            observedHeaderNames.set(Collections.emptyList());

            // when
            RawResponse result = get(path, tokenFor(credential, reference));

            // then
            assertThat(result.status).as("%s status", id).isEqualTo(401);
            assertThat(result.body).as("%s body", id).isEmpty();
            assertThat(result.contentType).as("%s content type", id).isEmpty();
            assertThat(forwardedRequests.get()).as("%s downstream calls", id).isZero();
            assertThat(observedHeaderNames.get()).as("%s downstream headers", id).isEmpty();

            // A valid call immediately after each rejection must still reach the real Go catalog.
            forwardedRequests.set(0);
            // when
            RawResponse followingResult = get(path);
            // then
            assertThat(followingResult.status).as("%s following valid status", id).isEqualTo(200);
            assertThat(forwardedRequests.get()).as("%s following valid downstream calls", id)
                    .isEqualTo(1);
            assertAcceptedBody(path, followingResult);
        }
    }

    private Map<String, JsonNode> rejectedCasesByID() {
        Map<String, JsonNode> cases = new LinkedHashMap<>();
        for (JsonNode testCase : accessCases.path("cases")) {
            String id = testCase.path("id").asText();
            String credential = testCase.path("credential").asText();
            if (!"gateway".equals(testCase.path("target").asText()) ||
                    !(REJECTED_CREDENTIALS.contains(credential) || REJECTED_CASES.containsKey(id) ||
                            testCase.path("expected").path("status").asInt() == 401)) {
                continue;
            }
            if (cases.putIfAbsent(id, testCase) != null) {
                throw new AssertionError("REJECTION_CASE_SET_MISMATCH duplicate=" + id);
            }
        }
        if (!cases.keySet().equals(REJECTED_CASES.keySet())) {
            throw new AssertionError("REJECTION_CASE_SET_MISMATCH expected=" + REJECTED_CASES.keySet()
                    + " actual=" + cases.keySet());
        }
        return cases;
    }

    private String tokenFor(String credential, Instant reference) {
        if ("missing".equals(credential)) {
            return null;
        }
        if ("malformed".equals(credential)) {
            return "not-a-jwt";
        }
        Map<String, Object> claims = new HashMap<>();
        if (!"missing-subject".equals(credential)) {
            claims.put("sub", "security-rejection");
        }
        claims.put("roles", Collections.singletonList("CAR"));
        claims.put("iss", "auth-service");
        claims.put("iat", Date.from(reference));
        claims.put("nbf", Date.from(reference.minusSeconds(120)));
        claims.put("exp", Date.from("expired".equals(credential)
                ? reference.minusSeconds(300) : reference.plusSeconds(600)));
        String token = tokenGenerator.generateToken(claims)
                .orElseThrow(() -> new IllegalStateException("Could not generate disposable rejection token"));
        if ("invalid-signature".equals(credential)) {
            int signatureStart = token.lastIndexOf('.') + 1;
            char original = token.charAt(signatureStart);
            token = token.substring(0, signatureStart) + (original == 'A' ? 'B' : 'A')
                    + token.substring(signatureStart + 1);
        }
        return token;
    }

    private void assertAcceptedBody(String path, RawResponse response) throws IOException {
        assertThat(response.contentType).as("following valid content type").isEqualTo("application/json");
        if ("/api/products".equals(path)) {
            assertThat(normalizeList(mapper.readTree(response.body)))
                    .isEqualTo(normalizeList(acceptedBody("ACCESS-GATEWAY-LIST-VALID-001")));
        } else {
            assertThat(mapper.readTree(response.body))
                    .isEqualTo(acceptedBody("ACCESS-GATEWAY-GET-VALID-001"));
        }
    }

    private JsonNode acceptedBody(String scenarioID) throws IOException {
        for (JsonNode observation : acceptedAccess.path("observations")) {
            if (scenarioID.equals(observation.path("scenarioId").asText())) {
                return mapper.readTree(observation.path("response").path("rawBody").asText());
            }
        }
        throw new IOException("Missing accepted QA access scenario: " + scenarioID);
    }

    private List<JsonNode> normalizeList(JsonNode body) {
        List<JsonNode> products = new ArrayList<>();
        body.forEach(products::add);
        products.sort(Comparator.comparing(product -> product.path("code").asText()));
        return products;
    }

    private RawResponse get(String path) throws IOException {
        return get(path, bearerToken);
    }

    private RawResponse get(String path, String token) throws IOException {
        HttpURLConnection connection = (HttpURLConnection) new URL(gateway.getURL().toString() + path)
                .openConnection();
        connection.setRequestMethod("GET");
        connection.setRequestProperty("Accept", "application/json");
        if (token != null) {
            connection.setRequestProperty("Authorization", "Bearer " + token);
        }
        connection.setConnectTimeout(2000);
        connection.setReadTimeout(10000);
        try {
            int status = connection.getResponseCode();
            InputStream stream = status >= 400 ? connection.getErrorStream() : connection.getInputStream();
            String body = stream == null ? "" : new String(readAll(stream), StandardCharsets.UTF_8);
            String type = connection.getContentType();
            return new RawResponse(status, type == null ? "" : type.split(";")[0], body);
        } finally {
            connection.disconnect();
        }
    }

    private void forwardToGo(HttpExchange exchange) throws IOException {
        forwardedRequests.incrementAndGet();
        List<String> names = new ArrayList<>();
        for (String name : exchange.getRequestHeaders().keySet()) {
            names.add(name.toLowerCase());
        }
        Collections.sort(names);
        observedHeaderNames.set(names);

        HttpURLConnection downstream = null;
        try {
            downstream = (HttpURLConnection) new URL("http://" + goHTTP + exchange.getRequestURI())
                    .openConnection();
            downstream.setRequestMethod(exchange.getRequestMethod());
            downstream.setConnectTimeout(1000);
            downstream.setReadTimeout(3000);
            for (Map.Entry<String, List<String>> header : exchange.getRequestHeaders().entrySet()) {
                if (!HOP_HEADERS.contains(header.getKey().toLowerCase())) {
                    for (String value : header.getValue()) {
                        downstream.addRequestProperty(header.getKey(), value);
                    }
                }
            }
            int status = downstream.getResponseCode();
            for (Map.Entry<String, List<String>> header : downstream.getHeaderFields().entrySet()) {
                if (header.getKey() != null && !HOP_HEADERS.contains(header.getKey().toLowerCase())) {
                    exchange.getResponseHeaders().put(header.getKey(), new ArrayList<>(header.getValue()));
                }
            }
            InputStream stream = status >= 400 ? downstream.getErrorStream() : downstream.getInputStream();
            byte[] response = stream == null ? new byte[0] : readAll(stream);
            exchange.sendResponseHeaders(status, response.length);
            exchange.getResponseBody().write(response);
        } catch (IOException failure) {
            // This observer cannot replace a missing Go response with a successful Product body.
            exchange.sendResponseHeaders(502, -1);
        } finally {
            if (downstream != null) {
                downstream.disconnect();
            }
            exchange.close();
        }
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
