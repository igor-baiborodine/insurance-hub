package pl.altkom.asc.lab.micronaut.poc.auth;

import io.micronaut.core.annotation.Creator;
import io.micronaut.data.annotation.Id;
import io.micronaut.data.annotation.MappedEntity;
import lombok.Data;
import lombok.NoArgsConstructor;

import java.util.Arrays;
import java.util.Collection;
import java.util.List;
import java.util.UUID;

@Data
@NoArgsConstructor
@MappedEntity
public class InsuranceAgent {

    @Id
    private UUID id;

    private String login;
    private String password;
    private String avatar;

    /**
     * Stored in DB as a single string: "TRI;HSI;FAI;CAR"
     */
    private String availableProducts;

    @Creator
    public InsuranceAgent(UUID id, String login, String password, String avatar, String availableProducts) {
        this.id = id;
        this.login = login;
        this.password = password;
        this.avatar = avatar;
        this.availableProducts = availableProducts;
    }

    public static InsuranceAgent of(UUID id, String login, String password, String avatar, List<String> availableProducts) {
        return new InsuranceAgent(id, login, password, avatar, String.join(";", availableProducts));
    }

    public boolean passwordMatches(String passwordToTest) {
        return this.password != null && this.password.equals(passwordToTest);
    }

    public Collection<String> availableProductCodes() {
        if (availableProducts == null || availableProducts.isBlank()) {
            return List.of();
        }
        return Arrays.asList(availableProducts.split(";"));
    }
}
