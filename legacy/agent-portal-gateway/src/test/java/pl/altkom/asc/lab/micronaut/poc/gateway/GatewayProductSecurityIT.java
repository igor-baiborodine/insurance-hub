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
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collections;
import java.util.Comparator;
import java.util.HashMap;
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

    @BeforeAll
    void setup() throws IOException {
        String endpointFile = System.getProperty("product.security.endpoint.file");
        String qaAccessFile = System.getProperty("product.security.qa.access.file");
        assertThat(endpointFile).as("product.security.endpoint.file").isNotBlank();
        assertThat(qaAccessFile).as("product.security.qa.access.file").isNotBlank();
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
        bearerToken = gateway.getApplicationContext().getBean(JwtTokenGenerator.class)
                .generateToken(new UserDetails("security-smoke", Collections.singletonList("UNRELATED")), 300)
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
        HttpURLConnection connection = (HttpURLConnection) new URL(gateway.getURL().toString() + path)
                .openConnection();
        connection.setRequestMethod("GET");
        connection.setRequestProperty("Accept", "application/json");
        connection.setRequestProperty("Authorization", "Bearer " + bearerToken);
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
