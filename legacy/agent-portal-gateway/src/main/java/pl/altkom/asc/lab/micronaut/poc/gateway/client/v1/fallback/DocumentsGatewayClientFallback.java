package pl.altkom.asc.lab.micronaut.poc.gateway.client.v1.fallback;

import io.micronaut.retry.annotation.Fallback;
import lombok.extern.slf4j.Slf4j;
import org.jetbrains.annotations.NotNull;
import pl.altkom.asc.lab.micronaut.poc.documents.api.queries.finddocuments.FindDocumentsResult;
import pl.altkom.asc.lab.micronaut.poc.gateway.client.v1.DocumentsGatewayClient;

import javax.inject.Singleton;
import java.util.Collections;

@Slf4j
@Singleton
@Fallback
public class DocumentsGatewayClientFallback implements DocumentsGatewayClient {

    @NotNull
    @Override
    public FindDocumentsResult find(String policyNumber) {
        log.warn("Fallback called for find documents with policyNumber: {}, empty result returned", policyNumber);
        return new FindDocumentsResult(Collections.emptyList());
    }
}
