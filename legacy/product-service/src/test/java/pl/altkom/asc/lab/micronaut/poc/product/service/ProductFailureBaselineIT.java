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
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.SQLException;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class ProductFailureBaselineIT extends BaseIT {

    private static final String FIXTURE = "product-read-baseline/failures/cases.json";
    private static final String SINGLE_DEFINITION = "{\"name\":\"Only product\",\"covers\":[],"
            + "\"questions\":[],\"maxNumberOfInsured\":0}";

    private final ObjectMapper objectMapper = new ObjectMapper();
    private EmbeddedServer server;
    private JsonNode fixture;

    @BeforeAll
    void setup() throws IOException {
        server = startServerWithDisposableConnectionTimeout(1000);
        try (InputStream input = getClass().getClassLoader().getResourceAsStream(FIXTURE)) {
            assertThat(input).as("failure fixture").isNotNull();
            fixture = objectMapper.readTree(input);
        }
    }

    @AfterAll
    void cleanup() {
        if (server != null) {
            server.stop();
        }
        if (!postgresqlContainer.isRunning()) {
            postgresqlContainer.start();
        }
    }

    @Test
    void capturesProductFailuresAndBoundaries() throws Exception {
        ArrayNode actual = objectMapper.createArrayNode();
        boolean databaseStopped = false;

        for (JsonNode testCase : fixture.path("cases")) {
            if (!"product".equals(testCase.path("target").asText())) {
                continue;
            }
            String setup = testCase.path("setup").asText();
            if ("database-unavailable".equals(setup)) {
                if (!databaseStopped) {
                    postgresqlContainer.stop();
                    databaseStopped = true;
                }
            } else {
                clearCatalog();
                if ("single".equals(setup)) {
                    insertRow("ONLY", SINGLE_DEFINITION);
                } else if ("raw-row".equals(setup)) {
                    insertRow(testCase.path("code").asText(), testCase.path("rawDefinitionJson").asText());
                }
            }
            actual.add(capture(testCase));
        }

        if (Boolean.getBoolean("baseline.failures.print")) {
            System.out.println("PRODUCT_FAILURE_OBSERVATIONS=" + actual);
        }
        assertExpected("product", actual);
    }

    private ObjectNode capture(JsonNode testCase) throws IOException {
        RawResponse response = rawGet(testCase.path("rawPath").asText());
        ObjectNode result = objectMapper.createObjectNode();
        result.put("id", testCase.path("id").asText());
        result.put("status", response.status);
        result.put("contentType", response.contentType);
        result.put("rawBody", response.body);
        return result;
    }

    private void assertExpected(String target, ArrayNode actual) {
        int actualIndex = 0;
        for (JsonNode testCase : fixture.path("cases")) {
            if (!target.equals(testCase.path("target").asText())) {
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
        InetSocketAddress address = new InetSocketAddress(server.getHost(), server.getPort());
        try (Socket socket = new Socket()) {
            socket.connect(address, 2000);
            socket.setSoTimeout(35000);
            String request = "GET " + path + " HTTP/1.1\r\nHost: " + server.getHost() + ":" + server.getPort()
                    + "\r\nAccept: application/json\r\nConnection: close\r\n\r\n";
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

    private void clearCatalog() throws SQLException {
        try (Connection connection = databaseConnection();
             PreparedStatement statement = connection.prepareStatement("DELETE FROM product")) {
            statement.executeUpdate();
        }
    }

    private void insertRow(String code, String definition) throws SQLException {
        try (Connection connection = databaseConnection();
             PreparedStatement statement = connection.prepareStatement(
                     "INSERT INTO product (code, definition) VALUES (?, ?::jsonb)")) {
            statement.setString(1, code);
            statement.setString(2, definition);
            statement.executeUpdate();
        }
    }

    private Connection databaseConnection() throws SQLException {
        return DriverManager.getConnection(postgresqlContainer.getJdbcUrl(),
                postgresqlContainer.getUsername(), postgresqlContainer.getPassword());
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
