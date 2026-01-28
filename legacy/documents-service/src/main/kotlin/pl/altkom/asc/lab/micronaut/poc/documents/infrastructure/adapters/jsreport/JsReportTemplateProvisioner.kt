package pl.altkom.asc.lab.micronaut.poc.documents.infrastructure.adapters.jsreport

import com.fasterxml.jackson.databind.JsonNode
import io.micronaut.context.annotation.Value
import io.micronaut.context.event.ApplicationEventListener
import io.micronaut.http.HttpHeaders
import io.micronaut.http.HttpRequest
import io.micronaut.http.HttpStatus
import io.micronaut.http.client.HttpClient
import io.micronaut.http.client.exceptions.HttpClientResponseException
import io.micronaut.runtime.server.event.ServerStartupEvent
import org.slf4j.LoggerFactory
import java.net.URL
import java.net.URLEncoder
import java.nio.charset.StandardCharsets
import javax.inject.Singleton

private const val TEMPLATE_NAME = "POLICY"
private const val TEMPLATE_RESOURCE = "/policy.template"
private const val JSREPORT_TEMPLATES_ENDPOINT = "/odata/templates"
private const val TEMPLATE_RECIPE = "chrome-pdf"
private const val TEMPLATE_ENGINE = "handlebars"
private const val JSON_MEDIA_TYPE = "application/json;odata.metadata=minimal;odata.streaming=true;IEEE754Compatible=false;charset=utf-8"

@Singleton
class JsReportTemplateProvisioner : ApplicationEventListener<ServerStartupEvent> {

    companion object {
        private val LOG = LoggerFactory.getLogger(JsReportTemplateProvisioner::class.java)
    }

    @field:Value("\${jsreport.host}")
    private lateinit var host: String

    @field:Value("\${jsreport.port}")
    private lateinit var port: String

    override fun onApplicationEvent(event: ServerStartupEvent) {
        LOG.info("Validating jsreport template '{}'", TEMPLATE_NAME)
        ensurePolicyTemplate()
    }

    private fun ensurePolicyTemplate() {
        val templateContent = loadTemplate()
        HttpClient.create(URL("http", host, port.toInt(), "")).use { client ->
            val templateId = findExistingTemplateId(client)
            if (templateId == null) {
                LOG.info("Creating jsreport template '{}'", TEMPLATE_NAME)
                sendCreateRequest(client, templateContent)
            } else {
                LOG.info("jsreport template '{}' already exists (shortid={}), skipping provisioning", TEMPLATE_NAME, templateId)
            }
        }
    }

    private fun loadTemplate(): String {
        val resource = this::class.java.getResourceAsStream(TEMPLATE_RESOURCE)
                ?: throw IllegalStateException("Missing template resource $TEMPLATE_RESOURCE")
        return resource.bufferedReader(StandardCharsets.UTF_8).use { it.readText() }
    }

    private fun findExistingTemplateId(client: HttpClient): String? {
        val filter = URLEncoder.encode("name eq '$TEMPLATE_NAME'", StandardCharsets.UTF_8)
        val request = HttpRequest.GET<Any>("$JSREPORT_TEMPLATES_ENDPOINT?\$filter=$filter")
                .header(HttpHeaders.ACCEPT, JSON_MEDIA_TYPE)
                .header(HttpHeaders.CONTENT_TYPE, JSON_MEDIA_TYPE)
        val response = try {
            client.toBlocking().retrieve(request, JsonNode::class.java)
        } catch (ex: HttpClientResponseException) {
            if (ex.status == HttpStatus.NOT_FOUND) {
                LOG.debug("jsreport template '{}' not found", TEMPLATE_NAME)
                return null
            }
            throw ex
        }

        return extractTemplateId(response)
    }

    private fun extractTemplateId(node: JsonNode?): String? {
        if (node == null) return null
        val candidate = when {
            node.has("value") && node["value"].isArray -> firstElement(node["value"])
            node.isArray -> firstElement(node)
            node.has("data") && node["data"].isArray -> firstElement(node["data"])
            else -> node
        }
        return candidate?.let { getTemplateId(it) }
    }

    private fun sendCreateRequest(client: HttpClient, content: String) {
        val payload = JsReportTemplatePayload(TEMPLATE_NAME, content)
        val request = HttpRequest.POST(JSREPORT_TEMPLATES_ENDPOINT, payload)
                .header(HttpHeaders.CONTENT_TYPE, JSON_MEDIA_TYPE)
                .header(HttpHeaders.ACCEPT, JSON_MEDIA_TYPE)
        client.toBlocking().exchange(request, ByteArray::class.java)
    }

    private fun firstElement(arrayNode: JsonNode): JsonNode? {
        val iterator = arrayNode.elements()
        return if (iterator.hasNext()) iterator.next() else null
    }

    private fun getTemplateId(node: JsonNode): String? {
        return node["_id"]?.takeUnless { it.isNull }?.asText()
                ?: node["shortid"]?.takeUnless { it.isNull }?.asText()
    }
}

private data class JsReportTemplatePayload(
        val name: String,
        val content: String,
        val engine: String = TEMPLATE_ENGINE,
        val recipe: String = TEMPLATE_RECIPE
)
