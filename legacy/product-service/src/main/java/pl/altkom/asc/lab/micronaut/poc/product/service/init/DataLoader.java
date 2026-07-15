package pl.altkom.asc.lab.micronaut.poc.product.service.init;

import io.micronaut.context.event.ApplicationEventListener;
import io.micronaut.runtime.server.event.ServerStartupEvent;
import lombok.RequiredArgsConstructor;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Products;

import javax.inject.Singleton;
import java.util.function.Supplier;

@Singleton
@RequiredArgsConstructor
public class DataLoader implements ApplicationEventListener<ServerStartupEvent> {

    private final Products productsRepository;

    @Override
    public void onApplicationEvent(ServerStartupEvent serverStartupEvent) {
        seedIfMissing("CAR", DemoProductsFactory::car);
        seedIfMissing("FAI", DemoProductsFactory::farm);
        seedIfMissing("HSI", DemoProductsFactory::house);
        seedIfMissing("TRI", DemoProductsFactory::travel);
    }

    private void seedIfMissing(String productCode, Supplier<Product> productSupplier) {
        if (productsRepository.findOne(productCode).blockingGet() == null) {
            productsRepository.add(productSupplier.get()).blockingGet();
        }
    }
}
