package pl.altkom.asc.lab.micronaut.poc.product.service;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import io.micronaut.http.HttpRequest;
import io.micronaut.http.HttpResponse;
import io.micronaut.http.client.HttpClient;
import io.micronaut.http.client.exceptions.HttpClientResponseException;
import io.micronaut.runtime.server.EmbeddedServer;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Nested;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.TestInstance;

import java.io.IOException;
import java.io.InputStream;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.SQLException;
import java.sql.Types;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class ProductDataEdgesIT extends BaseIT {

    private static final String FIXTURE = "product-read-baseline/data-edges/cases.json";

    private final ObjectMapper objectMapper = new ObjectMapper();
    private EmbeddedServer server;
    private HttpClient client;
    private JsonNode fixture;

    @BeforeAll
    void setup() throws IOException {
        server = startServer();
        client = server.getApplicationContext().createBean(HttpClient.class, server.getURL());
        try (InputStream input = getClass().getClassLoader().getResourceAsStream(FIXTURE)) {
            assertThat(input).as("synthetic data-edge fixture").isNotNull();
            fixture = objectMapper.readTree(input);
        }
    }

    @AfterAll
    void cleanup() throws SQLException {
        if (postgresqlContainer.isRunning()) {
            clearCatalog();
        }
        if (client != null) {
            client.close();
        }
        if (server != null) {
            server.stop();
        }
    }

    @Nested
    public class SyntheticDataSemantics {

        @Test
        public void capturesRegisteredDataSemantics() throws SQLException {
            // given
            ArrayNode actual = objectMapper.createArrayNode();

            // when
            for (JsonNode testCase : fixture.path("cases")) {
                JsonNode firstObservation = capture(testCase);
                for (int repetition = 1; repetition < testCase.path("repetitions").asInt(1); repetition++) {
                    assertThat(capture(testCase))
                            .as("repeated observation for %s", testCase.path("id").asText())
                            .isEqualTo(firstObservation);
                }
                actual.add(firstObservation);
            }

            // then
            if (Boolean.getBoolean("baseline.data.edges.print")) {
                System.out.println("DATA_EDGES_OBSERVATIONS=" + actual);
            }
            for (int index = 0; index < fixture.path("cases").size(); index++) {
                JsonNode expected = fixture.path("cases").get(index).path("expected");
                if (expected.isMissingNode() || expected.isNull()) {
                    assertThat(Boolean.getBoolean("baseline.data.edges.print"))
                            .as("expected observation for %s",
                                    fixture.path("cases").get(index).path("id").asText())
                            .isTrue();
                    continue;
                }
                ObjectNode actualObservation = actual.get(index).deepCopy();
                actualObservation.remove("id");
                assertThat(actualObservation).isEqualTo(expected);
            }
        }
    }

    private ObjectNode capture(JsonNode testCase) throws SQLException {
        clearCatalog();
        ObjectNode result = objectMapper.createObjectNode();
        result.put("id", testCase.path("id").asText());

        if ("database-constraint".equals(testCase.path("kind").asText())) {
            try {
                insertRows(testCase.path("rows"));
                result.put("outcome", "inserted");
            } catch (SQLException exception) {
                result.put("outcome", "constraint-rejected");
                result.put("sqlState", exception.getSQLState());
            }
            return result;
        }

        insertRows(testCase.path("rows"));
        try {
            HttpResponse<String> response = client.toBlocking().exchange(
                    HttpRequest.GET(testCase.path("requestPath").asText()), String.class);
            result.put("outcome", "http-success");
            result.put("status", response.getStatus().getCode());
            result.put("contentType", response.getContentType().map(Object::toString).orElse(""));
            result.put("rawBody", response.body());
        } catch (HttpClientResponseException exception) {
            result.put("outcome", "http-failure");
            result.put("status", exception.getStatus().getCode());
            result.put("contentType", exception.getResponse().getContentType().map(Object::toString).orElse(""));
            result.put("rawBody", exception.getResponse().getBody(String.class).orElse(""));
        }
        return result;
    }

    private void clearCatalog() throws SQLException {
        try (Connection connection = databaseConnection();
             PreparedStatement statement = connection.prepareStatement("DELETE FROM product")) {
            statement.executeUpdate();
        }
    }

    private void insertRows(JsonNode rows) throws SQLException {
        try (Connection connection = databaseConnection();
             PreparedStatement statement = connection.prepareStatement(
                     "INSERT INTO product (code, definition) VALUES (?, ?::jsonb)")) {
            for (JsonNode row : rows) {
                if (row.path("code").isNull()) {
                    statement.setNull(1, Types.VARCHAR);
                } else {
                    statement.setString(1, row.path("code").asText());
                }
                if (row.path("rawDefinitionJson").isNull()) {
                    statement.setNull(2, Types.OTHER);
                } else {
                    statement.setString(2, row.path("rawDefinitionJson").asText());
                }
                statement.executeUpdate();
            }
        }
    }

    private Connection databaseConnection() throws SQLException {
        return DriverManager.getConnection(
                postgresqlContainer.getJdbcUrl(),
                postgresqlContainer.getUsername(),
                postgresqlContainer.getPassword());
    }
}
