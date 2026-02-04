package pl.altkom.asc.lab.micronaut.poc.policy.search.readmodel;

import lombok.*;

import java.time.LocalDate;

@Builder
@Getter
@ToString
@NoArgsConstructor
@AllArgsConstructor
public class PolicyView {
    private String number;
    private LocalDate dateFrom;
    private LocalDate dateTo;
    private String policyHolder;

    public PolicyView(String number) {
        this.number = number;
    }
}
