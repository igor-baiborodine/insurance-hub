package pl.altkom.asc.lab.micronaut.poc.payment.infrastructure.adapters.minio;

import io.micronaut.context.annotation.Factory;
import io.micronaut.context.annotation.Bean;
import io.micronaut.context.annotation.Value;
import io.minio.MinioClient;

import javax.inject.Singleton;

@Factory
public class MinioClientFactory {

    @Value("${minio.tenant-endpoint}")
    private String endpoint;

    @Value("${minio.svc-access-key}")
    private String accessKey;

    @Value("${minio.svc-secret-key}")
    private String secretKey;

    @Bean
    @Singleton
    public MinioClient minioClient() {
        return MinioClient.builder()
                .endpoint(endpoint)
                .credentials(accessKey, secretKey)
                .build();
    }
}
