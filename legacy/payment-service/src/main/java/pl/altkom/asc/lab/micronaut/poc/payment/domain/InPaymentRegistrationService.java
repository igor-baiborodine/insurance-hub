package pl.altkom.asc.lab.micronaut.poc.payment.domain;

import io.minio.GetObjectArgs;
import io.minio.MinioClient;
import io.minio.errors.MinioException;
import io.minio.PutObjectArgs;
import io.minio.RemoveObjectArgs;
import io.minio.StatObjectArgs;
import io.minio.errors.ErrorResponseException;

import org.apache.commons.csv.CSVFormat;
import org.apache.commons.csv.CSVRecord;

import java.io.BufferedReader;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.IOException;
import java.security.InvalidKeyException;
import java.security.NoSuchAlgorithmException;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.List;

import javax.inject.Singleton;
import javax.transaction.Transactional;

import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;

import pl.altkom.asc.lab.micronaut.poc.payment.service.api.v1.exceptions.BankStatementsFileNotFound;
import pl.altkom.asc.lab.micronaut.poc.payment.service.api.v1.exceptions.BankStatementsFileReadingError;

import static pl.altkom.asc.lab.micronaut.poc.payment.domain.BankStatementFile.BankStatement;

@Slf4j
@Singleton
@RequiredArgsConstructor
public class InPaymentRegistrationService {

    private final PolicyAccountRepository policyAccountRepository;
    private final MinioClient minioClient;

    @Transactional
    public void registerInPayments(String s3BucketName, LocalDate date) {
        BankStatementFile fileToImport = new BankStatementFile(date);
        String objectKey = fileToImport.getObjectKey();
        String processedObjectKey = fileToImport.getProcessedObjectKey();

        if (!objectExists(s3BucketName, objectKey)) {
            log.info("Bank statement file not found in S3 bucket {}: {}", s3BucketName, objectKey);
            return;
        }

        List<BankStatement> bankStatements = readBankStatementsFromMinio(s3BucketName, objectKey);
        bankStatements.forEach(this::registerInPayment);

        markBankStatementProcessedInMinio(s3BucketName, objectKey, processedObjectKey);
    }

    private boolean objectExists(String bucketName, String objectKey) {
        try {
            minioClient.statObject(
                    StatObjectArgs.builder()
                            .bucket(bucketName)
                            .object(objectKey)
                            .build());
            return true;
        } catch (ErrorResponseException e) {
            if ("NoSuchKey".equals(e.errorResponse().code())) {
                return false;
            }
            log.error("Error checking existence of object {} in bucket {}: {}", objectKey, bucketName, e.getMessage());
            throw new BankStatementsFileReadingError(e);
        } catch (MinioException | IOException | NoSuchAlgorithmException | InvalidKeyException e) {
            log.error("Unexpected error checking existence of object {} in bucket {}: {}", objectKey, bucketName, e.getMessage());
            throw new BankStatementsFileReadingError(e);
        }
    }


    private List<BankStatement> readBankStatementsFromMinio(String bucketName, String objectKey) {
        try (InputStream is = minioClient.getObject(
                GetObjectArgs.builder()
                        .bucket(bucketName)
                        .object(objectKey)
                        .build());
            BufferedReader reader = new BufferedReader(new InputStreamReader(is))) {
            List<BankStatement> statements = new ArrayList<>();

            Iterable<CSVRecord> records = CSVFormat
                    .RFC4180
                    .withFirstRecordAsHeader()
                    .parse(reader);
            records.forEach(row -> statements.add(readRow(row)));

            return statements;
        } catch (ErrorResponseException e) {
            log.error("MinIO error while reading object {} from bucket {}: {}", objectKey, bucketName, e.getMessage());
            if ("NoSuchKey".equals(e.errorResponse().code())) {
                throw new BankStatementsFileNotFound(e);
            }
            throw new BankStatementsFileReadingError(e);
        } catch (MinioException | IOException | NoSuchAlgorithmException | InvalidKeyException e) {
            log.error("Error while reading object {} from bucket {}: {}", objectKey, bucketName, e.getMessage());
            throw new BankStatementsFileReadingError(e);
        }
    }

    private void markBankStatementProcessedInMinio(String bucketName, String originalObjectKey, String processedObjectKey) {
        try (InputStream originalContent = minioClient.getObject(
                GetObjectArgs.builder()
                        .bucket(bucketName)
                        .object(originalObjectKey)
                        .build())) {

            minioClient.putObject(
                    PutObjectArgs.builder()
                            .bucket(bucketName)
                            .object(processedObjectKey)
                            .stream(originalContent, -1, 10485760)
                            .build());
        } catch (ErrorResponseException e) {
            log.error("MinIO error while retrieving original object {} from bucket {}: {}", originalObjectKey, bucketName, e.getMessage());
            throw new RuntimeException("Failed to mark bank statement as processed in MinIO", e);
        } catch (MinioException | IOException | NoSuchAlgorithmException | InvalidKeyException e) {
            log.error("Error marking object {} as processed in bucket {}: {}", originalObjectKey, bucketName, e.getMessage());
            throw new RuntimeException("Failed to mark bank statement as processed in MinIO", e);
        }

        try {
            minioClient.removeObject(
                    RemoveObjectArgs.builder()
                            .bucket(bucketName)
                            .object(originalObjectKey)
                            .build());
        } catch (ErrorResponseException e) {
            log.error("MinIO error while removing original object {} from bucket {}: {}", originalObjectKey, bucketName, e.getMessage());
            throw new RuntimeException("Failed to remove original bank statement from MinIO after processing", e);
        } catch (MinioException | IOException | NoSuchAlgorithmException | InvalidKeyException e) {
            log.error("Error removing original object {} from bucket {}: {}", originalObjectKey, bucketName, e.getMessage());
            throw new RuntimeException("Failed to remove original bank statement from MinIO after processing", e);
        }
    }

    private BankStatement readRow(CSVRecord row) {
        String accountingDate = row.get(2);
        String accountNumber = row.get(3);
        String amountAsString = row.get(4);
        return new BankStatement(accountNumber, amountAsString, accountingDate);
    }

    private void registerInPayment(BankStatement bankStatement) {
        policyAccountRepository
                .findByPolicyAccountNumber(bankStatement.getAccountNumber())
                .ifPresent(account ->
                        account.inPayment(bankStatement.getAmount(), bankStatement.getAccountingDate()));
    }
}
