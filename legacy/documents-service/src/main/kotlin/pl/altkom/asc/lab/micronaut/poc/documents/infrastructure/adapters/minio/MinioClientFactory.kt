package pl.altkom.asc.lab.micronaut.poc.documents.infrastructure.adapters.minio

import io.micronaut.context.annotation.Bean
import io.micronaut.context.annotation.Factory
import io.micronaut.context.annotation.Value
import io.minio.MinioClient
import javax.inject.Singleton

@Factory
class MinioClientFactory {

    @Value("\${minio.tenant-endpoint}")
    lateinit var endpoint: String

    @Value("\${minio.svc-access-key}")
    lateinit var accessKey: String

    @Value("\${minio.svc-secret-key}")
    lateinit var secretKey: String

    @Bean
    @Singleton
    fun minioClient(): MinioClient {
        return MinioClient.builder()
            .endpoint(endpoint)
            .credentials(accessKey, secretKey)
            .build()
    }
}
