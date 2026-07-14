package pl.altkom.asc.lab.micronaut.poc.product.service.infrastructure.adapters.db.model;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import lombok.AllArgsConstructor;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;
import pl.altkom.asc.lab.micronaut.poc.product.service.domain.Product;

import java.util.ArrayList;
import java.util.List;
import java.util.stream.Collectors;

@Getter
@Setter
@NoArgsConstructor
@AllArgsConstructor
@JsonIgnoreProperties(ignoreUnknown = true)
public class ProductDefinition {

    private String name;
    private String image;
    private String description;
    private List<CoverDefinition> covers = new ArrayList<>();
    private List<QuestionDefinition> questions = new ArrayList<>();
    private int maxNumberOfInsured;
    private String icon;

    public static ProductDefinition from(Product product) {
        List<CoverDefinition> coverDefinitions = product.getCovers() == null
                ? new ArrayList<>()
                : product.getCovers().stream()
                        .map(CoverDefinition::from)
                        .collect(Collectors.toList());
        List<QuestionDefinition> questionDefinitions = product.getQuestions() == null
                ? new ArrayList<>()
                : product.getQuestions().stream()
                        .map(QuestionDefinition::from)
                        .collect(Collectors.toList());

        return new ProductDefinition(
                product.getName(),
                product.getImage(),
                product.getDescription(),
                coverDefinitions,
                questionDefinitions,
                product.getMaxNumberOfInsured(),
                product.getIcon());
    }

    public Product toDomain(String code) {
        List<pl.altkom.asc.lab.micronaut.poc.product.service.domain.Cover> domainCovers = covers.stream()
                .map(CoverDefinition::toDomain)
                .collect(Collectors.toList());
        List<pl.altkom.asc.lab.micronaut.poc.product.service.domain.Question> domainQuestions = questions.stream()
                .map(QuestionDefinition::toDomain)
                .collect(Collectors.toList());

        return new Product(
                code,
                name,
                image,
                description,
                domainCovers,
                domainQuestions,
                maxNumberOfInsured,
                icon);
    }
}
