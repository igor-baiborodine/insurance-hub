package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db;

import io.micronaut.context.annotation.Replaces;
import io.reactivex.Maybe;
import io.reactivex.Single;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;

import javax.inject.Singleton;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

@Singleton
@Replaces(ProductsRepository.class)
public class InMemoryProductsRepository extends ProductsRepository {

    private final Map<String, Product> productsByCode = new LinkedHashMap<>();

    public InMemoryProductsRepository() {
        super(null);
    }

    @Override
    public Single<Product> add(Product product) {
        productsByCode.put(product.getCode(), product);
        return Single.just(product);
    }

    @Override
    public Single<List<Product>> findAll() {
        return Single.just(new ArrayList<>(productsByCode.values()));
    }

    @Override
    public Maybe<Product> findOne(String productCode) {
        Product product = productsByCode.get(productCode);
        return product == null ? Maybe.empty() : Maybe.just(product);
    }
}
