package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.web;

import io.micronaut.http.annotation.Get;
import io.micronaut.http.client.annotation.Client;
import pl.altkom.asc.lab.micronaut.poc.product.service.api.v1.ProductDto;

import java.util.List;

@Client("/products")
public interface ProductsTestClient {

    @Get
    List<ProductDto> getAll();

    @Get("/{productCode}")
    ProductDto get(String productCode);
}
