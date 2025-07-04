package pl.altkom.asc.lab.micronaut.poc.payment.domain;

import io.micronaut.data.annotation.Query;
import io.micronaut.data.annotation.Repository;
import io.micronaut.data.jpa.annotation.EntityGraph;
import io.micronaut.data.repository.GenericRepository;
import pl.altkom.asc.lab.micronaut.poc.payment.service.api.v1.PolicyAccountDto;

import java.util.Collection;
import java.util.Optional;

@Repository
public interface PolicyAccountRepository extends GenericRepository<PolicyAccount, Long> {

    @EntityGraph(attributePaths = {"entries"})
    Optional<PolicyAccount> findByPolicyNumber(String policyNumber);

    @EntityGraph(attributePaths = {"entries"})
    @Query("FROM PolicyAccount p WHERE p.policyAccountNumber = :policyAccountNumber")
    Optional<PolicyAccount> findByPolicyAccountNumber(String policyAccountNumber);

    PolicyAccount save(PolicyAccount policyAccount);

    Collection<PolicyAccountDto> findAll();
}
