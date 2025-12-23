package pl.altkom.asc.lab.micronaut.poc.auth;

import lombok.extern.slf4j.Slf4j;
import org.reactivestreams.Publisher;

import java.util.Optional;

import javax.inject.Singleton;

import io.micronaut.http.HttpRequest;
import io.micronaut.security.authentication.AuthenticationFailed;
import io.micronaut.security.authentication.AuthenticationProvider;
import io.micronaut.security.authentication.AuthenticationRequest;
import io.micronaut.security.authentication.AuthenticationResponse;
import io.reactivex.Flowable;
import lombok.RequiredArgsConstructor;

@Slf4j
@Singleton
@RequiredArgsConstructor
public class AuthProvider implements AuthenticationProvider {

    private final InsuranceAgentsRepository insuranceAgents;

    @Override
    public Publisher<AuthenticationResponse> authenticate(HttpRequest<?> httpRequest, AuthenticationRequest<?, ?> authenticationRequest) {
        String username = (String) authenticationRequest.getIdentity();
        Optional<InsuranceAgent> agent = insuranceAgents.findByLogin(username);

        log.info("Attempting to authenticate user: {}", username);

        if (agent.isPresent() && agent.get().passwordMatches((String) authenticationRequest.getSecret())) {
            log.info("User authenticated: {}", username);
            return Flowable.just(createUserDetails(agent.get()));
        }
        log.info("User authentication failed: {}", username);
        return Flowable.just(new AuthenticationFailed());
    }

    private InsuranceAgentDetails createUserDetails(InsuranceAgent agent) {
        return new InsuranceAgentDetails(agent.getLogin(), agent.getAvatar(), agent.availableProductCodes());
    }
}
