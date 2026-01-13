package pl.altkom.asc.lab.micronaut.poc.gateway.client.v1.fallback;

import io.micronaut.retry.annotation.Fallback;
import io.reactivex.Maybe;
import io.reactivex.Single;
import lombok.extern.slf4j.Slf4j;
import pl.altkom.asc.lab.micronaut.poc.gateway.client.v1.ProductGatewayClient;
import pl.altkom.asc.lab.micronaut.poc.product.service.api.v1.ProductDto;

import javax.inject.Singleton;
import java.util.Collections;
import java.util.List;

@Slf4j
@Singleton
@Fallback
public class ProductGatewayClientFallback implements ProductGatewayClient {

    @Override
    public Single<List<ProductDto>> getAll() {
        log.warn("Fallback called for getAll(), empty result returned");
        return Single.just(Collections.emptyList());
    }

    @Override
    public Maybe<ProductDto> get(String productCode) {
        log.warn("Fallback called for get(), empty result returned");
        return Maybe.empty();
    }
}
