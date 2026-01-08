package pl.altkom.asc.lab.micronaut.poc.documents.domain

import javax.persistence.Column
import javax.persistence.Entity
import javax.persistence.GeneratedValue
import javax.persistence.Id
import javax.persistence.Lob


/**
 * Represents a document associated with an insurance policy.
 *
 * This entity is designed to store information about a policy's
 * document either as binary PDF data for legacy purposes or as an
 * S3 object key encoded as a ByteArray for newer implementations.
 *
 * @property id The unique identifier of the policy document. Auto-generated.
 * @property policyNumber The policy number associated with this document.
 * @property bytes The binary data representing the document or an S3 object key.
 * This can serve a dual purpose:
 * - For legacy systems, it stores the actual PDF binary data.
 * - For newer systems, it encodes the S3 object key as a ByteArray.
 */
@Entity
data class PolicyDocument(
        @Id
        @GeneratedValue
        val id: Long? = -1,
        val policyNumber: String = "",
        @Column(columnDefinition = "bytea")
        val bytes: ByteArray = ByteArray(1)
)