package pl.altkom.asc.lab.micronaut.poc.documents.infrastructure.adapters.minio

import io.micronaut.context.annotation.Bean
import io.micronaut.context.annotation.Factory
import io.micronaut.context.annotation.Value
import io.minio.MinioClient
import okhttp3.OkHttpClient
import java.security.SecureRandom
import java.security.cert.X509Certificate
import javax.inject.Singleton
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

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
        val trustAllCerts = object : X509TrustManager {
            override fun checkClientTrusted(
                chain: Array<out X509Certificate>?,
                authType: String?
            ) {
            }

            override fun checkServerTrusted(
                chain: Array<out X509Certificate>?,
                authType: String?
            ) {
            }

            override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf()
        }

        val sslContext = SSLContext.getInstance("SSL")
        sslContext.init(null, arrayOf<TrustManager>(trustAllCerts), SecureRandom())

        val httpClient = OkHttpClient.Builder()
            .sslSocketFactory(sslContext.socketFactory, trustAllCerts)
            .hostnameVerifier { _, _ -> true }
            .build()

        return MinioClient.builder()
            .endpoint(endpoint)
            .credentials(accessKey, secretKey)
            .httpClient(httpClient)
            .build()
    }
}
