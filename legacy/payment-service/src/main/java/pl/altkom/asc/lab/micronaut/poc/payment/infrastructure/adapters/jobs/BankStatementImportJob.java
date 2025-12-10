package pl.altkom.asc.lab.micronaut.poc.payment.infrastructure.adapters.jobs;

import io.micronaut.scheduling.annotation.Scheduled;
import java.time.LocalDate;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import pl.altkom.asc.lab.micronaut.poc.payment.domain.InPaymentRegistrationService;

import javax.inject.Singleton;

@Slf4j
@Singleton
@RequiredArgsConstructor
public class BankStatementImportJob {

    private final BankStatementImportJobCfg jobCfg;
    private final InPaymentRegistrationService inPaymentRegistrationService;

    @Scheduled(fixedRate = "${payments.import-fixed-rate}")
    public void importBankStatement() {
       log.info("Starting bank statement import job");
       inPaymentRegistrationService.registerInPayments(jobCfg.getImportS3Bucket(), LocalDate.now());
    }
}
