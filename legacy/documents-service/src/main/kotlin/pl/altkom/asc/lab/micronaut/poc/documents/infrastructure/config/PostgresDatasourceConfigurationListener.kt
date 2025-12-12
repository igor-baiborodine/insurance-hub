package pl.altkom.asc.lab.micronaut.poc.documents.infrastructure.config

import io.micronaut.configuration.jdbc.hikari.DatasourceConfiguration
import io.micronaut.context.event.BeanCreatedEvent
import io.micronaut.context.event.BeanCreatedEventListener
import java.net.URLEncoder
import java.nio.charset.StandardCharsets
import javax.inject.Singleton


@Singleton
class PostgresDatasourceConfigurationListener(
    private val postgres: PostgresDatasourceProperties
) : BeanCreatedEventListener<DatasourceConfiguration> {

    override fun onCreated(event: BeanCreatedEvent<DatasourceConfiguration>): DatasourceConfiguration {
        val cfg = event.bean

        if (cfg.name == "default") {
            cfg.driverClassName = "org.postgresql.Driver"
            cfg.username = postgres.username
            cfg.password = postgres.password
            cfg.url = buildJdbcUrl()
        }
       return cfg
    }

    private fun buildJdbcUrl(): String {
        val user = encode(postgres.username)
        val password = encode(postgres.password)
        val sslMode = if (postgres.ssl) "require" else "disable"

        return "jdbc:postgresql://${postgres.host}:${postgres.port}/${postgres.database}" +
                "?sslmode=$sslMode&user=$user&password=$password"
    }

    private fun encode(value: String): String =
        URLEncoder.encode(value, StandardCharsets.UTF_8.toString())
}
