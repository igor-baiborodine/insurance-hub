package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db;

import io.micronaut.data.annotation.Repository;
import io.micronaut.data.repository.CrudRepository;
import pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model.ProductEntity;

import java.util.Optional;

@Repository
public interface ProductsRepository extends CrudRepository<ProductEntity, String> {

    Optional<ProductEntity> findByCode(String code);

    ProductEntity save(ProductEntity productEntity);
}
