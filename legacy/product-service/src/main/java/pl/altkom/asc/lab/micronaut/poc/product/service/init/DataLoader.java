package pl.altkom.asc.lab.micronaut.poc.product.service.init;

import io.micronaut.context.event.ApplicationEventListener;
import io.micronaut.runtime.server.event.ServerStartupEvent;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Products;

import javax.inject.Singleton;
import java.util.function.Supplier;

@Slf4j
@Singleton
@RequiredArgsConstructor
public class DataLoader implements ApplicationEventListener<ServerStartupEvent> {

    private final Products productsRepository;

    @Override
    public void onApplicationEvent(ServerStartupEvent serverStartupEvent) {
        log.info("Starting data seeding...");
        seedIfMissing("CAR", DemoProductsFactory::car);
        seedIfMissing("FAI", DemoProductsFactory::farm);
        seedIfMissing("HSI", DemoProductsFactory::house);
        seedIfMissing("TRI", DemoProductsFactory::travel);
        log.info("Data seeding finished.");
    }

    private void seedIfMissing(String productCode, Supplier<Product> productSupplier) {
        if (productsRepository.findOne(productCode).blockingGet() == null) {
            log.info("Product {} missing. Seeding...", productCode);
            productsRepository.add(productSupplier.get()).blockingGet();
        } else {
            log.info("Product {} already exists.", productCode);
        }
    }
}
