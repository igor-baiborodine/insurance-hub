package pl.altkom.asc.lab.micronaut.poc.payment.infrastructure.adapters.minio;

import io.micronaut.context.annotation.Factory;
import io.micronaut.context.annotation.Bean;
import io.micronaut.context.annotation.Value;
import io.minio.MinioClient;
import okhttp3.OkHttpClient;

import javax.inject.Singleton;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;
import java.security.SecureRandom;
import java.security.cert.X509Certificate;

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
        try {
            X509TrustManager trustAllCerts = new X509TrustManager() {
                @Override
                public void checkClientTrusted(X509Certificate[] chain, String authType) {
                }

                @Override
                public void checkServerTrusted(X509Certificate[] chain, String authType) {
                }

                @Override
                public X509Certificate[] getAcceptedIssuers() {
                    return new X509Certificate[]{};
                }
            };

            SSLContext sslContext = SSLContext.getInstance("SSL");
            sslContext.init(null, new TrustManager[]{trustAllCerts}, new SecureRandom());

            OkHttpClient httpClient = new OkHttpClient.Builder()
                    .sslSocketFactory(sslContext.getSocketFactory(), trustAllCerts)
                    .hostnameVerifier((hostname, session) -> true)
                    .build();

            return MinioClient.builder()
                    .endpoint(endpoint)
                    .credentials(accessKey, secretKey)
                    .httpClient(httpClient)
                    .build();
        } catch (Exception e) {
            throw new RuntimeException("Failed to create MinioClient with custom SSL", e);
        }
    }
}
