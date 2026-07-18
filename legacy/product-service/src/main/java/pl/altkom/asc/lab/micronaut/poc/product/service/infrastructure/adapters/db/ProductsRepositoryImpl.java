package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db;

import io.micronaut.context.annotation.Requires;
import io.reactivex.Flowable;
import io.reactivex.Maybe;
import io.reactivex.Single;
import lombok.RequiredArgsConstructor;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Products;
import pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model.ProductEntity;

import javax.inject.Singleton;
import java.util.List;

@Singleton
@RequiredArgsConstructor
@Requires(property = "products.persistence", value = "postgres")
public class ProductsRepositoryImpl implements Products {

    private final ProductsRepository postgresProductsRepository;

    @Override
    public Single<Product> add(Product product) {
        return Single.fromCallable(() ->
                postgresProductsRepository.save(ProductEntity.from(product)).toDomain());
    }

    @Override
    public Single<List<Product>> findAll() {
        return Flowable.fromIterable(postgresProductsRepository.findAll())
                .map(ProductEntity::toDomain)
                .toList();
    }

    @Override
    public Maybe<Product> findOne(String productCode) {
        return postgresProductsRepository.findByCode(productCode)
                .map(productEntity -> Maybe.just(productEntity.toDomain()))
                .orElseGet(Maybe::empty);
    }
}
