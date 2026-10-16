package pl.altkom.asc.lab.micronaut.poc.product.service;

import brave.Span;
import brave.Tracing;
import brave.propagation.TraceContext;
import brave.sampler.Sampler;
import com.sun.net.httpserver.HttpServer;
import io.micronaut.context.ApplicationContext;
import io.micronaut.runtime.server.EmbeddedServer;
import io.micronaut.tracing.brave.BraveTracerConfiguration;
import org.junit.jupiter.api.Test;
import org.testcontainers.containers.PostgreSQLContainer;
import org.testcontainers.utility.DockerImageName;

import java.io.IOException;
import java.io.InputStream;
import java.io.ByteArrayOutputStream;
import java.net.HttpURLConnection;
import java.net.InetSocketAddress;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.HashMap;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/** A disposable Brave peer for the Product Go continuity Make target. */
public class TraceContinuityFixtureTest {

    @Test
    public void correctedSamplerPropertyBindsInMicronautRuntime() {
        requireOwnedGoFixture();
        assertThat(System.getenv("TRACING_ZIPKIN_SAMPLER_PROBABILITY")).isEqualTo("1.0");
        try (PostgreSQLContainer<?> database = new PostgreSQLContainer<>(
                DockerImageName.parse("postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193")
                        .asCompatibleSubstituteFor("postgres"))) {
            database.start();
            Map<String, Object> properties = new HashMap<>();
            properties.put("micronaut.environments", "test");
            properties.put("tracing.zipkin.enabled", true);
            properties.put("datasources.default.url", database.getJdbcUrl());
            properties.put("datasources.default.driverClassName", "org.postgresql.Driver");
            properties.put("datasources.default.username", database.getUsername());
            properties.put("datasources.default.password", database.getPassword());
            EmbeddedServer server = ApplicationContext.run(EmbeddedServer.class, properties);
            try {
                ApplicationContext context = server.getApplicationContext();
                BraveTracerConfiguration.SamplerConfiguration sampler = context.getBean(
                        BraveTracerConfiguration.SamplerConfiguration.class);
                assertThat(sampler.getProbability()).isEqualTo(1.0f);
                assertThat(context.getBean(Tracing.class).sampler().isSampled(1L)).isTrue();
            } finally {
                server.stop();
            }
        }
    }

    @Test
    public void braveClientAndServerShareGoTraceContext() throws IOException {
        String goUrl = requireOwnedGoFixture();

        for (boolean wideTraceId : Arrays.asList(false, true)) {
            for (boolean sampled : Arrays.asList(false, true)) {
                try (Tracing tracing = Tracing.newBuilder()
                        .localServiceName("product-service-java-fixture")
                        .sampler(sampled ? Sampler.ALWAYS_SAMPLE : Sampler.NEVER_SAMPLE)
                        .traceId128Bit(wideTraceId)
                        .build()) {
                    Span root = tracing.tracer().newTrace().name("java root").start();
                    Span client = tracing.tracer().newChild(root.context()).kind(Span.Kind.CLIENT)
                            .name("java to go").start();
                    try {
                        HttpURLConnection connection = open(goUrl + "/java-to-go");
                        tracing.propagation().injector(
                                (HttpURLConnection carrier, String key, String value) ->
                                        carrier.setRequestProperty(key, value))
                                .inject(client.context(), connection);
                        String[] observed = read(connection).split(";");
                        assertThat(observed).hasSize(3);
                        assertThat(observed[0]).isEqualTo(normalizeTraceId(client.context().traceIdString()));
                        assertThat(observed[1]).isEqualTo(client.context().spanIdString());
                        assertThat(observed[2]).isEqualTo(Boolean.toString(sampled));
                    } finally {
                        client.finish();
                        root.finish();
                    }
                }
            }
        }

        try (Tracing tracing = Tracing.newBuilder()
                .localServiceName("product-service-java-fixture")
                .sampler(Sampler.ALWAYS_SAMPLE)
                .build()) {
            HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
            server.createContext("/products", exchange -> {
                TraceContext extracted = tracing.propagation().extractor(
                        (com.sun.net.httpserver.HttpExchange carrier, String key) ->
                                carrier.getRequestHeaders().getFirst(key)).extract(exchange).context();
                if (extracted == null) {
                    exchange.sendResponseHeaders(400, -1);
                    exchange.close();
                    return;
                }
                Span joined = tracing.tracer().joinSpan(extracted).kind(Span.Kind.SERVER).start();
                try {
                    if (Boolean.TRUE.equals(extracted.sampled())) {
                        assertThat(joined.context().traceIdString()).isEqualTo(extracted.traceIdString());
                        assertThat(joined.context().spanIdString()).isEqualTo(extracted.spanIdString());
                    }
                    String response = normalizeTraceId(extracted.traceIdString()) + ";"
                            + extracted.spanIdString() + ";"
                            + extracted.sampled() + ";"
                            + exchange.getRequestHeaders().getFirst("traceparent");
                    byte[] bytes = response.getBytes(StandardCharsets.UTF_8);
                    exchange.sendResponseHeaders(200, bytes.length);
                    exchange.getResponseBody().write(bytes);
                } finally {
                    joined.finish();
                    exchange.close();
                }
            });
            server.start();
            try {
                assertThat(read(open(goUrl + "/go-to-java?port=" + server.getAddress().getPort())))
                        .isEqualTo("ok");
                assertThat(read(open(goUrl + "/go-to-java?sampled=0&port=" + server.getAddress().getPort())))
                        .isEqualTo("ok");
            } finally {
                server.stop(0);
            }
        }
    }

    private static HttpURLConnection open(String url) throws IOException {
        HttpURLConnection connection = (HttpURLConnection) new URL(url).openConnection();
        connection.setConnectTimeout(3000);
        connection.setReadTimeout(3000);
        return connection;
    }

    private static String requireOwnedGoFixture() {
        String goUrl = System.getenv("PRODUCT_GO_CONTINUITY_URL");
        assumeTrue(goUrl != null && goUrl.startsWith("http://127.0.0.1:"),
                "only the owned local Go fixture may invoke this test");
        return goUrl;
    }

    private static String read(HttpURLConnection connection) throws IOException {
        assertThat(connection.getResponseCode()).isEqualTo(200);
        try (InputStream stream = connection.getInputStream()) {
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            byte[] buffer = new byte[256];
            int count;
            while ((count = stream.read(buffer)) != -1) {
                bytes.write(buffer, 0, count);
            }
            return new String(bytes.toByteArray(), StandardCharsets.UTF_8);
        } finally {
            connection.disconnect();
        }
    }

    private static String normalizeTraceId(String traceId) {
        return traceId.length() == 16 ? "0000000000000000" + traceId : traceId;
    }
}
