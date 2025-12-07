package pl.altkom.asc.lab.micronaut.poc.payment.domain;

import lombok.Getter;

import java.math.BigDecimal;
import java.time.LocalDate;
import java.time.format.DateTimeFormatter;

public class BankStatementFile {

    private final String objectKey;

    BankStatementFile(LocalDate importDate) {
        this.objectKey = constructFileNameFromDate(importDate);
    }

    String getObjectKey() {
        return objectKey;
    }

    String getProcessedObjectKey() {
        return "_processed_" + objectKey;
    }

    private String constructFileNameFromDate(LocalDate importDate) {
        return String.format("bankStatements_%d_%d_%d.csv", importDate.getYear(), importDate.getMonthValue(), importDate.getDayOfMonth());
    }

    @Getter
    static class BankStatement {
        private final String accountNumber;
        private final BigDecimal amount;
        private final LocalDate accountingDate;

        BankStatement(String accountNumber, String amountAsString, String accountingDateAsIsoDateString) {
            this.accountNumber = accountNumber;
            this.amount = new BigDecimal(amountAsString);
            this.accountingDate = LocalDate.parse(accountingDateAsIsoDateString, DateTimeFormatter.ISO_DATE);
        }
    }
}
