package pl.altkom.asc.lab.micronaut.poc.documents.domain

import io.micronaut.context.annotation.Value
import io.micronaut.http.HttpResponse
import io.minio.MinioClient
import io.minio.PutObjectArgs
import io.minio.GetObjectArgs
import pl.altkom.asc.lab.micronaut.poc.policy.service.api.v1.events.PolicyRegisteredEvent
import java.io.ByteArrayInputStream
import java.time.format.DateTimeFormatter
import java.nio.charset.StandardCharsets
import javax.inject.Singleton

@Singleton
class PolicyDocumentService(
    private val policyDocumentRepository: PolicyDocumentRepository,
    private val reportGenerator: ReportGenerator,
    private val minioClient: MinioClient
) {

    @Value("\${documents.policies-s3-bucket}")
    private lateinit var policiesS3Bucket: String

    fun add(event: PolicyRegisteredEvent): PolicyDocument? {
        val pdfBytesResponse: HttpResponse<ByteArray>? = reportGenerator.generate(event)

        if (pdfBytesResponse?.body() != null) {
            val pdfBytes = pdfBytesResponse.body()!!
            val policyNumber = event.policy.number
            val registrationDate = event.policy.from

            val yearMonth = registrationDate.format(DateTimeFormatter.ofPattern("yyyy/MM"))
            val s3ObjectKey = "$yearMonth/$policyNumber.pdf"

            try {
                minioClient.putObject(
                    PutObjectArgs.builder()
                        .bucket(policiesS3Bucket)
                        .`object`(s3ObjectKey)
                        .stream(ByteArrayInputStream(pdfBytes), pdfBytes.size.toLong(), -1)
                        .contentType("application/pdf")
                        .build()
                )

                val keyBytes = s3ObjectKey.toByteArray(StandardCharsets.UTF_8)
                val document = PolicyDocument(policyNumber = policyNumber, bytes = keyBytes)
                policyDocumentRepository.add(document)
                return document
            } catch (e: Exception) {
                System.err.println("Error adding policy document to S3 storage or database: ${e.message}")
                e.printStackTrace()
                return null
            }
        }
        return null
    }

    
    fun retrieve(policyNumber: String): ByteArray? {
        val documents = policyDocumentRepository.findByPolicyNumber(policyNumber)
        if (documents.isEmpty()) {
            return null
        }

        val document = documents.first()
        val storedBytes = document.bytes

        if (storedBytes == null || storedBytes.isEmpty()) {
            return null
        }

        // Heuristic: Attempt to decode the stored bytes as a UTF-8 string.
        // If it decodes successfully and matches the expected S3 key pattern (YYYY/MM/policy-number.pdf),
        // assume it's an S3 key. Otherwise, assume it's actual PDF content (legacy).
        try {
            val potentialS3ObjectKey = String(storedBytes, StandardCharsets.UTF_8)

            // A typical S3 key will be relatively short and follow the YYYY/MM/ format.
            // Actual PDF data will be much larger and binary.
            val minioKeyPattern = Regex("^\\d{4}/\\d{2}/.*\\.pdf$")

            if (potentialS3ObjectKey.length < 1024 && potentialS3ObjectKey.matches(
                    minioKeyPattern
                )
            ) {
                return try {
                    minioClient.getObject(
                        GetObjectArgs.builder()
                            .bucket(policiesS3Bucket)
                            .`object`(potentialS3ObjectKey)
                            .build()
                    ).use { stream ->
                        stream.readBytes()
                    }
                } catch (e: Exception) {
                    System.err.println("Error retrieving object from S3 with key $potentialS3ObjectKey: ${e.message}")
                    e.printStackTrace()
                    null
                }
            } else {
                // Assume it's direct PDF bytes (legacy storage)
                return storedBytes
            }
        } catch (e: Exception) {
            System.err.println("Error decoding bytes as UTF-8, assuming legacy PDF data: ${e.message}")
            return storedBytes
        }
    }
}
