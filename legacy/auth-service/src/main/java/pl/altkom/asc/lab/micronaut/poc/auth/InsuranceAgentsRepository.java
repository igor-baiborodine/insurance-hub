package pl.altkom.asc.lab.micronaut.poc.auth;

import java.util.Optional;

import io.micronaut.data.jdbc.annotation.JdbcRepository;
import io.micronaut.data.repository.CrudRepository;

@JdbcRepository
public interface InsuranceAgentsRepository extends CrudRepository<InsuranceAgent,Long> {
    Optional<InsuranceAgent> findByLogin(String login);
}
