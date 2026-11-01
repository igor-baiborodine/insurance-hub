package pl.altkom.asc.lab.micronaut.poc.gateway;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.micronaut.context.ApplicationContext;
import org.junit.jupiter.api.Test;
import pl.altkom.asc.lab.micronaut.poc.gateway.client.v1.PolicySearchGatewayClient;

import java.io.IOException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;

public class PolicySearchGatewayClientPathTest {

    @Test
    void callsCanonicalPolicyCollectionPathWithoutTrailingSlash() throws IOException {
        // given
        AtomicInteger requests = new AtomicInteger();
        AtomicReference<String> path = new AtomicReference<>();
        HttpServer backend = HttpServer.create(
                new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
        backend.createContext("/policies", exchange -> respond(exchange, requests, path));
        backend.start();

        Map<String, Object> properties = new HashMap<>();
        properties.put("micronaut.environments", "test");
        properties.put("tracing.zipkin.enabled", false);
        properties.put("micronaut.http.services.policy-search-service.urls",
                Collections.singletonList("http://127.0.0.1:" + backend.getAddress().getPort()));

        try (ApplicationContext context = ApplicationContext.run(properties)) {
            PolicySearchGatewayClient client = context.getBean(PolicySearchGatewayClient.class);

            // when
            client.policies("canonical path").blockingGet();

            // then
            assertThat(requests).hasValue(1);
            assertThat(path).hasValue("/policies");
        } finally {
            backend.stop(0);
        }
    }

    private static void respond(HttpExchange exchange, AtomicInteger requests,
                                AtomicReference<String> path) throws IOException {
        requests.incrementAndGet();
        path.set(exchange.getRequestURI().getPath());
        byte[] response = "{\"policies\":[]}".getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, response.length);
        try (java.io.OutputStream body = exchange.getResponseBody()) {
            body.write(response);
        }
    }
}
