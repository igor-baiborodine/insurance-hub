package pl.altkom.asc.lab.micronaut.poc.payment.domain;

import io.minio.*;
import io.minio.errors.MinioException;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.ArgumentCaptor;
import org.mockito.Captor;
import org.mockito.Mock;
import org.mockito.InjectMocks;
import org.mockito.junit.jupiter.MockitoExtension;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.math.BigDecimal;
import java.security.InvalidKeyException;
import java.security.NoSuchAlgorithmException;
import java.time.LocalDate;
import java.time.Month;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
public class InPaymentRegistrationServiceTest {

    @Mock
    private PolicyAccountRepository policyAccountRepository;
    @Mock
    private MinioClient minioClient;
    @InjectMocks
    private InPaymentRegistrationService classUnderTest;

    @Captor
    private ArgumentCaptor<PutObjectArgs> putObjectArgsCaptor;
    @Captor
    private ArgumentCaptor<RemoveObjectArgs> removeObjectArgsCaptor;

    private final String TEST_BUCKET = "payments-import";

    @BeforeEach
    void setUp() throws MinioException, IOException, NoSuchAlgorithmException, InvalidKeyException {
        lenient().when(minioClient.statObject(any(StatObjectArgs.class))).thenReturn(mock(StatObjectResponse.class));
        lenient().when(minioClient.putObject(any(PutObjectArgs.class))).thenReturn(mock(ObjectWriteResponse.class));
        doNothing().when(minioClient).removeObject(any(RemoveObjectArgs.class));    }

    @Test
    void canRegisterInPaymentsFromMinio() throws IOException, MinioException, NoSuchAlgorithmException, InvalidKeyException {
        // given
        LocalDate statementDate = LocalDate.of(2018, Month.AUGUST, 2);
        String csvContent = "TransactionId,TransactionType,AccountingDate,AccountNumber,Amount\r\n"
                + "1,A,2018-08-01,231232132131,10.21\r\n"
                + "1,A,2018-08-01,0rju130fhj20,99.25\r\n";
        ByteArrayInputStream realInputStream = new ByteArrayInputStream(csvContent.getBytes());

        GetObjectResponse mockGetObjectResponse = mock(GetObjectResponse.class);
        when(mockGetObjectResponse.read()).thenAnswer(invocation -> realInputStream.read());
        when(mockGetObjectResponse.read(any(byte[].class))).thenAnswer(invocation -> realInputStream.read(invocation.getArgument(0)));
        when(mockGetObjectResponse.read(any(byte[].class), anyInt(), anyInt())).thenAnswer(invocation -> realInputStream.read(invocation.getArgument(0), invocation.getArgument(1), invocation.getArgument(2)));
        when(mockGetObjectResponse.available()).thenAnswer(invocation -> realInputStream.available());
        doAnswer(invocation -> { realInputStream.close(); return null; }).when(mockGetObjectResponse).close();
        when(minioClient.getObject(any(GetObjectArgs.class))).thenReturn(mockGetObjectResponse);

        PolicyAccount mockAccount = mock(PolicyAccount.class);
        when(mockAccount.balanceAt(LocalDate.of(2019, 12, 31))).thenReturn(BigDecimal.ZERO); // Initial balance
        when(policyAccountRepository.findByPolicyAccountNumber("231232132131")).thenReturn(Optional.of(mockAccount));
        when(policyAccountRepository.findByPolicyAccountNumber("0rju130fhj20")).thenReturn(Optional.empty()); // No account for second entry
        // when
        classUnderTest.registerInPayments(TEST_BUCKET, statementDate);
        // then
        verify(minioClient).statObject(argThat(args ->
                args.bucket().equals(TEST_BUCKET) && args.object().equals("bankStatements_2018_8_2.csv")));
        verify(minioClient).getObject(argThat(args ->
                args.bucket().equals(TEST_BUCKET) && args.object().equals("bankStatements_2018_8_2.csv")));
        verify(mockAccount).inPayment(new BigDecimal("10.21"), LocalDate.of(2018, 8, 1));
        verify(minioClient).putObject(putObjectArgsCaptor.capture());
        PutObjectArgs capturedPutArgs = putObjectArgsCaptor.getValue();
        assertEquals(TEST_BUCKET, capturedPutArgs.bucket());
        assertEquals("bankStatements_2018_8_2.csv.processed", capturedPutArgs.object());

        verify(minioClient).removeObject(removeObjectArgsCaptor.capture());
        RemoveObjectArgs capturedRemoveArgs = removeObjectArgsCaptor.getValue();
        assertEquals(TEST_BUCKET, capturedRemoveArgs.bucket());
        assertEquals("bankStatements_2018_8_2.csv", capturedRemoveArgs.object());
    }
}