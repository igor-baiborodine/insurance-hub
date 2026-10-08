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

import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.catchThrowable;

@TestInstance(TestInstance.Lifecycle.PER_CLASS)
public class ProductDatabasePermissionsIT extends BaseIT {

    private static final String CANDIDATE_ROLE = "go_product_reader";

    private final ObjectMapper objectMapper = new ObjectMapper();
    private EmbeddedServer server;
    private JsonNode fixture;
    private String candidatePassword;

    @BeforeAll
    void setup() throws Exception {
        String fixturePath = System.getProperty("baseline.db.permissions.fixture");
        assertThat(fixturePath).as("baseline.db.permissions.fixture system property").isNotBlank();
        try (InputStream input = Files.newInputStream(Paths.get(fixturePath))) {
            fixture = objectMapper.readTree(input);
        }

        server = startServer();
        candidatePassword = UUID.randomUUID().toString();
        try (Connection admin = adminConnection(); Statement statement = admin.createStatement()) {
            statement.execute("REVOKE ALL ON DATABASE " + postgresqlContainer.getDatabaseName() + " FROM PUBLIC");
            statement.execute("REVOKE ALL ON SCHEMA public FROM PUBLIC");
            statement.execute("REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC");
            statement.execute("ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA public "
                    + "REVOKE ALL ON TABLES FROM PUBLIC");
            statement.execute("CREATE ROLE " + CANDIDATE_ROLE
                    + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION PASSWORD '"
                    + candidatePassword + "'");
            statement.execute("GRANT CONNECT ON DATABASE " + postgresqlContainer.getDatabaseName()
                    + " TO " + CANDIDATE_ROLE);
            statement.execute("GRANT USAGE ON SCHEMA public TO " + CANDIDATE_ROLE);
            statement.execute("GRANT SELECT ON TABLE public.product TO " + CANDIDATE_ROLE);
        }
    }

    @AfterAll
    void cleanup() throws Exception {
        if (candidatePassword != null && postgresqlContainer.isRunning()) {
            try (Connection admin = adminConnection(); Statement statement = admin.createStatement()) {
                statement.execute("DROP OWNED BY " + CANDIDATE_ROLE);
                statement.execute("DROP ROLE IF EXISTS " + CANDIDATE_ROLE);
            }
        }
        if (server != null) {
            server.stop();
        }
    }

    @Test
    void provesCandidateRoleCanReadAndCannotMutateCatalogOrSchema() throws Exception {
        // given
        Snapshot before;
        String adminIdentity;
        try (Connection admin = adminConnection()) {
            adminIdentity = scalar(admin, "SELECT current_user");
            before = snapshot(admin);
        }

        // when
        ArrayNode actual = objectMapper.createArrayNode();
        try (Connection candidate = candidateConnection()) {
            recordRoleInspection(candidate, actual);
            for (JsonNode testCase : fixture.path("cases")) {
                if ("read".equals(testCase.path("operationClass").asText())) {
                    recordRead(candidate, testCase, actual);
                } else {
                    recordDenied(candidate, testCase, actual);
                }
            }
        }

        Snapshot after;
        try (Connection admin = adminConnection()) {
            after = snapshot(admin);
        }

        // then
        assertThat(adminIdentity).isNotEqualTo(CANDIDATE_ROLE);
        assertThat(before).usingRecursiveComparison().isEqualTo(after);
        assertThat(actual).isEqualTo(fixture.path("expectedObservations"));
    }

    private void recordRoleInspection(Connection candidate, ArrayNode actual) throws SQLException {
        ObjectNode observation = actual.addObject();
        observation.put("id", "DB-ROLE-BASELINE-001");
        observation.put("currentUser", scalar(candidate, "SELECT current_user"));
        observation.put("tableOwner", scalar(candidate,
                "SELECT pg_get_userbyid(relowner) FROM pg_class "
                        + "JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace "
                        + "WHERE nspname = 'public' AND relname = 'product'"));
        observation.put("ownsDatabase", bool(candidate,
                "SELECT datdba = (SELECT oid FROM pg_roles WHERE rolname = current_user) "
                        + "FROM pg_database WHERE datname = current_database()"));
        observation.put("ownsSchema", bool(candidate,
                "SELECT nspowner = (SELECT oid FROM pg_roles WHERE rolname = current_user) "
                        + "FROM pg_namespace WHERE nspname = 'public'"));
        observation.put("ownsTable", bool(candidate,
                "SELECT relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user) "
                        + "FROM pg_class JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace "
                        + "WHERE nspname = 'public' AND relname = 'product'"));
        observation.put("memberOfRoleCount", integer(candidate,
                "SELECT count(*)::integer FROM pg_auth_members "
                        + "JOIN pg_roles member ON member.oid = pg_auth_members.member "
                        + "WHERE member.rolname = current_user"));
        observation.put("superuser", bool(candidate,
                "SELECT rolsuper FROM pg_roles WHERE rolname = current_user"));
        observation.put("createDatabase", bool(candidate,
                "SELECT rolcreatedb FROM pg_roles WHERE rolname = current_user"));
        observation.put("createRole", bool(candidate,
                "SELECT rolcreaterole FROM pg_roles WHERE rolname = current_user"));
        observation.put("inherit", bool(candidate,
                "SELECT rolinherit FROM pg_roles WHERE rolname = current_user"));
        observation.put("replication", bool(candidate,
                "SELECT rolreplication FROM pg_roles WHERE rolname = current_user"));
        observation.put("bypassRls", bool(candidate,
                "SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user"));
        observation.put("canConnect", bool(candidate,
                "SELECT has_database_privilege(current_user, current_database(), 'CONNECT')"));
        observation.put("canCreateInDatabase", bool(candidate,
                "SELECT has_database_privilege(current_user, current_database(), 'CREATE')"));
        observation.put("canUseSchema", bool(candidate,
                "SELECT has_schema_privilege(current_user, 'public', 'USAGE')"));
        observation.put("canCreateInSchema", bool(candidate,
                "SELECT has_schema_privilege(current_user, 'public', 'CREATE')"));
        observation.put("canSelect", bool(candidate,
                "SELECT has_table_privilege(current_user, 'public.product', 'SELECT')"));
        observation.put("canInsert", bool(candidate,
                "SELECT has_table_privilege(current_user, 'public.product', 'INSERT')"));
        observation.put("canUpdate", bool(candidate,
                "SELECT has_table_privilege(current_user, 'public.product', 'UPDATE')"));
        observation.put("canDelete", bool(candidate,
                "SELECT has_table_privilege(current_user, 'public.product', 'DELETE')"));
        observation.put("canTruncate", bool(candidate,
                "SELECT has_table_privilege(current_user, 'public.product', 'TRUNCATE')"));
        observation.put("publicTableGrantCount", integer(candidate,
                "SELECT count(*)::integer FROM pg_class "
                        + "JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace "
                        + "CROSS JOIN LATERAL aclexplode(COALESCE(relacl, acldefault('r', relowner))) acl "
                        + "WHERE nspname = 'public' AND relname = 'product' AND acl.grantee = 0"));
        observation.put("publicDefaultTableGrantCount", integer(candidate,
                "SELECT count(*)::integer FROM pg_default_acl "
                        + "CROSS JOIN LATERAL aclexplode(defaclacl) acl "
                        + "WHERE defaclnamespace = 'public'::regnamespace "
                        + "AND defaclobjtype = 'r' AND acl.grantee = 0"));
        observation.put("candidateDefaultTableGrantCount", integer(candidate,
                "SELECT count(*)::integer FROM pg_default_acl "
                        + "CROSS JOIN LATERAL aclexplode(defaclacl) acl "
                        + "WHERE defaclnamespace = 'public'::regnamespace AND defaclobjtype = 'r' "
                        + "AND acl.grantee = (SELECT oid FROM pg_roles WHERE rolname = current_user)"));
        observation.put("sequenceCount", integer(candidate,
                "SELECT count(*)::integer FROM information_schema.sequences WHERE sequence_schema = 'public'"));
    }

    private void recordRead(Connection candidate, JsonNode testCase, ArrayNode actual) throws SQLException {
        ObjectNode observation = actual.addObject();
        observation.put("id", testCase.path("id").asText());
        observation.put("status", "succeeded");
        if ("DB-READ-LIST-001".equals(testCase.path("id").asText())) {
            observation.put("result", integer(candidate, testCase.path("sql").asText()));
        } else {
            observation.put("result", scalar(candidate, testCase.path("sql").asText()));
        }
    }

    private void recordDenied(Connection candidate, JsonNode testCase, ArrayNode actual) {
        Throwable result = catchThrowable(() -> {
            try (Statement statement = candidate.createStatement()) {
                statement.execute(testCase.path("sql").asText());
            }
        });
        assertThat(result).as(testCase.path("id").asText()).isInstanceOf(SQLException.class);
        SQLException sqlException = (SQLException) result;

        ObjectNode observation = actual.addObject();
        observation.put("id", testCase.path("id").asText());
        observation.put("status", "denied");
        observation.put("sqlState", sqlException.getSQLState());
    }

    private Connection adminConnection() throws SQLException {
        return DriverManager.getConnection(
                postgresqlContainer.getJdbcUrl(),
                postgresqlContainer.getUsername(),
                postgresqlContainer.getPassword());
    }

    private Connection candidateConnection() throws SQLException {
        return DriverManager.getConnection(
                postgresqlContainer.getJdbcUrl(),
                CANDIDATE_ROLE,
                candidatePassword);
    }

    private Snapshot snapshot(Connection connection) throws SQLException {
        return new Snapshot(
                integer(connection, "SELECT count(*)::integer FROM public.product"),
                scalar(connection, "SELECT md5(string_agg(code || E'\\n' || definition::text, E'\\n' ORDER BY code)) "
                        + "FROM public.product"),
                scalar(connection, "SELECT md5(string_agg(column_name || ':' || data_type || ':' || is_nullable, "
                        + "',' ORDER BY ordinal_position)) FROM information_schema.columns "
                        + "WHERE table_schema = 'public' AND table_name = 'product'"),
                integer(connection, "SELECT count(*)::integer FROM information_schema.tables "
                        + "WHERE table_schema = 'public' AND table_name = 'permission_probe'"),
                integer(connection, "SELECT count(*)::integer FROM information_schema.columns "
                        + "WHERE table_schema = 'public' AND table_name = 'product' "
                        + "AND column_name = 'permission_probe'"));
    }

    private String scalar(Connection connection, String sql) throws SQLException {
        try (Statement statement = connection.createStatement(); ResultSet result = statement.executeQuery(sql)) {
            assertThat(result.next()).isTrue();
            return result.getString(1);
        }
    }

    private int integer(Connection connection, String sql) throws SQLException {
        try (Statement statement = connection.createStatement(); ResultSet result = statement.executeQuery(sql)) {
            assertThat(result.next()).isTrue();
            return result.getInt(1);
        }
    }

    private boolean bool(Connection connection, String sql) throws SQLException {
        try (Statement statement = connection.createStatement(); ResultSet result = statement.executeQuery(sql)) {
            assertThat(result.next()).isTrue();
            return result.getBoolean(1);
        }
    }

    private static final class Snapshot {
        private final int rowCount;
        private final String dataIdentity;
        private final String schemaIdentity;
        private final int probeTableCount;
        private final int probeColumnCount;

        private Snapshot(int rowCount, String dataIdentity, String schemaIdentity,
                         int probeTableCount, int probeColumnCount) {
            this.rowCount = rowCount;
            this.dataIdentity = dataIdentity;
            this.schemaIdentity = schemaIdentity;
            this.probeTableCount = probeTableCount;
            this.probeColumnCount = probeColumnCount;
        }
    }
}
