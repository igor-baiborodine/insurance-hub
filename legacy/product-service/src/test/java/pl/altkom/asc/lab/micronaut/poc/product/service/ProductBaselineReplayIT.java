package pl.altkom.asc.lab.micronaut.poc.product.service;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import io.micronaut.http.HttpRequest;
import io.micronaut.http.HttpResponse;
import io.micronaut.http.client.HttpClient;
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
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;

import static org.assertj.core.api.Assertions.assertThat;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class ProductBaselineReplayIT extends BaseIT {

    private final ObjectMapper objectMapper = new ObjectMapper();
    private EmbeddedServer server;
    private HttpClient classUnderTest;
    private JsonNode catalogFixture;
    private JsonNode httpFixture;

    @BeforeAll
    void setup() throws IOException, SQLException {
        String fixtureSet = System.getProperty("baseline.fixture.set");
        assertThat(fixtureSet).as("baseline.fixture.set").isIn("local-dev", "qa");
        catalogFixture = readFixture("product-read-baseline/catalog/" + fixtureSet + ".json");
        httpFixture = readFixture("product-read-baseline/http/" + fixtureSet + ".json");

        server = startServer();
        classUnderTest = server.getApplicationContext().createBean(HttpClient.class, server.getURL());
        replaceCatalog(catalogFixture.path("rows"));
    }

    @AfterAll
    void cleanup() {
        if (classUnderTest != null) {
            classUnderTest.close();
        }
        if (server != null) {
            server.stop();
        }
    }

    @Nested
    public class AcceptedDirectObservations {

        @Test
        public void happyPath() throws IOException, SQLException {
            // given
            List<JsonNode> accepted = new ArrayList<>();
            httpFixture.path("observations").forEach(observation -> {
                if ("direct".equals(observation.path("boundary").asText())) {
                    accepted.add(observation);
                }
            });

            // when
            List<HttpResponse<String>> result = new ArrayList<>();
            for (JsonNode observation : accepted) {
                result.add(classUnderTest.toBlocking().exchange(
                        HttpRequest.GET(observation.path("request").path("path").asText()), String.class));
            }

            // then
            assertThat(result).hasSameSizeAs(accepted);
            for (int index = 0; index < accepted.size(); index++) {
                JsonNode expected = accepted.get(index);
                HttpResponse<String> actual = result.get(index);
                assertThat(actual.getStatus().getCode())
                        .as(expected.path("scenarioId").asText() + " status")
                        .isEqualTo(expected.path("response").path("status").asInt());
                assertThat(actual.getContentType().map(Object::toString).orElse(""))
                        .as(expected.path("scenarioId").asText() + " content type")
                        .isEqualTo(expected.path("response").path("headers").path("contentType").asText());
                assertThat(normalizeBody(actual.body()))
                        .as(expected.path("scenarioId").asText() + " response body")
                        .isEqualTo(normalizeBody(expected.path("response").path("rawBody").asText()));
            }
            assertStoredRowsRemainExact();
        }
    }

    private JsonNode readFixture(String resource) throws IOException {
        try (InputStream input = getClass().getClassLoader().getResourceAsStream(resource)) {
            assertThat(input).as(resource).isNotNull();
            return objectMapper.readTree(input);
        }
    }

    private JsonNode normalizeBody(String rawBody) throws IOException {
        JsonNode result = objectMapper.readTree(rawBody);
        if (result.isArray()) {
            List<JsonNode> values = new ArrayList<>();
            result.forEach(values::add);
            values.sort(Comparator.comparing(value -> value.path("code").asText()));
            ArrayNode sorted = objectMapper.createArrayNode();
            values.forEach(sorted::add);
            return sorted;
        }
        return result;
    }

    private void replaceCatalog(JsonNode rows) throws SQLException {
        try (Connection connection = databaseConnection()) {
            connection.setAutoCommit(false);
            try (PreparedStatement delete = connection.prepareStatement("DELETE FROM product")) {
                delete.executeUpdate();
            }
            try (PreparedStatement insert = connection.prepareStatement(
                    "INSERT INTO product (code, definition) VALUES (?, ?::jsonb)")) {
                for (JsonNode row : rows) {
                    insert.setString(1, row.path("code").asText());
                    insert.setString(2, row.path("rawLosslessDefinitionJson").asText());
                    insert.executeUpdate();
                }
            }
            connection.commit();
        }
    }

    private void assertStoredRowsRemainExact() throws SQLException {
        try (Connection connection = databaseConnection();
             PreparedStatement statement = connection.prepareStatement(
                     "SELECT code, definition::text FROM product ORDER BY code");
             ResultSet rows = statement.executeQuery()) {
            int index = 0;
            while (rows.next()) {
                JsonNode expected = catalogFixture.path("rows").get(index++);
                assertThat(rows.getString(1)).isEqualTo(expected.path("code").asText());
                assertThat(rows.getString(2)).isEqualTo(expected.path("rawLosslessDefinitionJson").asText());
            }
            assertThat(index).isEqualTo(catalogFixture.path("rows").size());
        }
    }

    private Connection databaseConnection() throws SQLException {
        return DriverManager.getConnection(
                postgresqlContainer.getJdbcUrl(),
                postgresqlContainer.getUsername(),
                postgresqlContainer.getPassword());
    }
}
