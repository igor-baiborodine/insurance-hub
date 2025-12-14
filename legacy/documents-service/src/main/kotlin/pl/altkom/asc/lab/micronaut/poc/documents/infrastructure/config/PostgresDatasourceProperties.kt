package pl.altkom.asc.lab.micronaut.poc.documents.infrastructure.config

import io.micronaut.context.annotation.ConfigurationProperties
import io.micronaut.core.annotation.Introspected

@Introspected
@ConfigurationProperties("postgres")
class PostgresDatasourceProperties {
    var host: String = "localhost"
    var port: Int = 5432
    var database: String = "document"
    var username: String = "document"
    var password: String = "document"
    var ssl: Boolean = false
}
