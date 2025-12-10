package pl.altkom.asc.lab.micronaut.poc.documents.infrastructure.adapters.web

import io.micronaut.http.annotation.Controller
import io.micronaut.scheduling.TaskExecutors
import io.micronaut.scheduling.annotation.ExecuteOn
import io.micronaut.validation.Validated
import pl.altkom.asc.lab.micronaut.poc.documents.api.DocumentsOperations
import pl.altkom.asc.lab.micronaut.poc.documents.api.queries.finddocuments.FindDocumentsResult
import pl.altkom.asc.lab.micronaut.poc.documents.api.queries.finddocuments.GeneratedDocument
import pl.altkom.asc.lab.micronaut.poc.documents.domain.PolicyDocumentRepository
import pl.altkom.asc.lab.micronaut.poc.documents.domain.PolicyDocumentService

@ExecuteOn(TaskExecutors.IO)
@Validated
@Controller("/documents")
class DocumentsController(
    private val policyDocumentRepository: PolicyDocumentRepository,
    private val policyDocumentService: PolicyDocumentService // Inject PolicyDocumentService
) : DocumentsOperations {

    override fun find(policyNumber: String): FindDocumentsResult {
        val policyDocuments = policyDocumentRepository.findByPolicyNumber(policyNumber)
        val generatedDocuments = policyDocuments.mapNotNull { policyDocument ->
            val actualBytes = policyDocumentService.retrieveDocumentContent(policyDocument.bytes)
            if (actualBytes != null) {
                GeneratedDocument(policyNumber, actualBytes)
            } else {
                null
            }
        }
        return FindDocumentsResult(generatedDocuments)
    }
}
