package pl.altkom.asc.lab.micronaut.poc.product.service;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.micronaut.runtime.server.EmbeddedServer;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.security.SecureRandom;
import java.util.Base64;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class ProductAccessBaselineIT extends BaseIT {

    private final ObjectMapper objectMapper = new ObjectMapper();
    private final byte[] signingKey = new byte[64];
    private EmbeddedServer server;
    private JsonNode fixture;

    @BeforeAll
    void setup() throws IOException {
        String fixturePath = System.getProperty("baseline.access.fixture");
        assertThat(fixturePath).as("baseline.access.fixture system property").isNotBlank();
        try (InputStream input = Files.newInputStream(Paths.get(fixturePath))) {
            fixture = objectMapper.readTree(input);
        }
        new SecureRandom().nextBytes(signingKey);
        server = startServer();
    }

    @AfterAll
    void cleanup() {
        if (server != null) {
            server.stop();
        }
    }

    @Test
    void capturesDirectProductCredentialBehavior() throws Exception {
        // given
        ArrayNode actual = objectMapper.createArrayNode();

        // when
        for (JsonNode testCase : fixture.path("cases")) {
            if (!"product".equals(testCase.path("target").asText())) {
                continue;
            }
            RawResponse response = get(testCase.path("path").asText(), tokenFor(testCase.path("credential").asText()));
            ObjectNode observation = objectMapper.createObjectNode();
            observation.put("id", testCase.path("id").asText());
            observation.put("status", response.status);
            observation.put("contentType", response.contentType);
            observation.put("bodyType", objectMapper.readTree(response.body).isArray() ? "array" : "object");
            actual.add(observation);
        }

        // then
        if (Boolean.getBoolean("baseline.access.print")) {
            System.out.println("PRODUCT_ACCESS_OBSERVATIONS=" + actual);
        }
        assertExpected(actual);
    }

    private String tokenFor(String credential) throws Exception {
        if ("missing".equals(credential)) {
            return null;
        }
        if ("malformed".equals(credential)) {
            return "not-a-jwt";
        }

        long now = System.currentTimeMillis() / 1000;
        long expiration = "expired".equals(credential) ? now - 60 : now + 300;
        long notBefore = "not-before".equals(credential) ? now + 300 : now - 5;
        String issuer = "issuer-variation".equals(credential) ? "different-issuer" : "auth-service";
        String audience = "audience-variation".equals(credential) ? ",\"aud\":[\"different-audience\"]" : "";
        String role = "role-variation".equals(credential) ? "UNRELATED" : "CAR";
        String payload = "{\"sub\":\"baseline-access\",\"roles\":[\"" + role + "\"],\"iss\":\"" + issuer
                + "\",\"nbf\":" + notBefore + ",\"exp\":" + expiration + audience + "}";
        String token = sign(payload);
        return "invalid-signature".equals(credential) ? corruptSignature(token) : token;
    }

    private String sign(String payload) throws Exception {
        Base64.Encoder encoder = Base64.getUrlEncoder().withoutPadding();
        String content = encoder.encodeToString("{\"alg\":\"HS256\",\"typ\":\"JWT\"}"
                .getBytes(StandardCharsets.UTF_8)) + "."
                + encoder.encodeToString(payload.getBytes(StandardCharsets.UTF_8));
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(signingKey, "HmacSHA256"));
        return content + "." + encoder.encodeToString(mac.doFinal(content.getBytes(StandardCharsets.US_ASCII)));
    }

    private String corruptSignature(String token) {
        int signatureStart = token.lastIndexOf('.') + 1;
        char first = token.charAt(signatureStart);
        return token.substring(0, signatureStart) + (first == 'A' ? 'B' : 'A')
                + token.substring(signatureStart + 1);
    }

    private RawResponse get(String path, String token) throws IOException {
        URL url = new URL(server.getURL().toString() + path);
        HttpURLConnection connection = (HttpURLConnection) url.openConnection();
        connection.setRequestMethod("GET");
        connection.setRequestProperty("Accept", "application/json");
        if (token != null) {
            connection.setRequestProperty("Authorization", "Bearer " + token);
        }
        int status = connection.getResponseCode();
        InputStream stream = status >= 400 ? connection.getErrorStream() : connection.getInputStream();
        String body = stream == null ? "" : new String(readAll(stream), StandardCharsets.UTF_8);
        String contentType = connection.getContentType();
        connection.disconnect();
        return new RawResponse(status, contentType == null ? "" : contentType.split(";")[0], body);
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

    private void assertExpected(ArrayNode actual) {
        int actualIndex = 0;
        for (JsonNode testCase : fixture.path("cases")) {
            if (!"product".equals(testCase.path("target").asText())) {
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
