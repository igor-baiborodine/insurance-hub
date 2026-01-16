package pl.altkom.asc.lab.micronaut.poc.dashboard.elastic;

import co.elastic.clients.elasticsearch.ElasticsearchClient;
import co.elastic.clients.json.jackson.JacksonJsonpMapper;
import co.elastic.clients.transport.ElasticsearchTransport;
import co.elastic.clients.transport.rest_client.RestClientTransport;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.apache.http.HttpHost;
import org.elasticsearch.client.RestClient;
import org.junit.jupiter.api.Test;

import pl.altkom.asc.lab.micronaut.poc.dashboard.domain.PolicyDocument;
import pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic.PolicyElasticRepository;
import pl.altkom.asc.lab.micronaut.poc.dashboard.infrastructure.adapters.elastic.config.JsonConverter;

import java.math.BigDecimal;
import java.time.LocalDate;

import static org.junit.jupiter.api.Assertions.assertNotNull;

public class PolicyElasticRepositoryTest extends EmbeddedElasticTest {

    @Test
    public void canIndexPolicy() {
        PolicyDocument policyDocument = new PolicyDocument(
                "111-111",
                LocalDate.of(2018, 1, 1),
                LocalDate.of(2018, 12, 31),
                "John Smith",
                "SAFE_HOUSE",
                BigDecimal.valueOf(1000),
                "m.smith"
        );

        ObjectMapper mapper = objectMapper();
        RestClient restClient = RestClient.builder(new HttpHost("localhost", el.getHttpPort(), "http")).build();

        ElasticsearchTransport transport = new RestClientTransport(
                restClient, new JacksonJsonpMapper(mapper));

        PolicyElasticRepository repository = new PolicyElasticRepository(
                new ElasticsearchClient(transport),
                new JsonConverter(mapper)
        );

        repository.save(policyDocument);

        PolicyDocument saved = repository.findByNumber("111-111");

        assertNotNull(saved);
    }
}
