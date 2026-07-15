package pl.altkom.asc.lab.micronaut.poc.product.service.domain;

import lombok.Getter;
import lombok.NoArgsConstructor;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;

@Getter
@NoArgsConstructor
public class Product {
    private String code;
    private String name;
    private String image;
    private String description;
    private List<Cover> covers;
    private List<Question> questions;
    private int maxNumberOfInsured;
    private String icon;

    public Product(
            String code,
            String name,
            String image,
            String description,
            List<Cover> covers,
            List<Question> questions,
            int maxNumberOfInsured,
            String icon) {
        this.code = code;
        this.name = name;
        this.image = image;
        this.description = description;
        this.covers = covers;
        this.questions = questions;
        this.maxNumberOfInsured = maxNumberOfInsured;
        this.icon = icon;
    }

    public Product(String code, String name, String image, String description, int maxNumberOfInsured, String icon) {
        this.code = code;
        this.name = name;
        this.image = image;
        this.description = description;
        this.maxNumberOfInsured = maxNumberOfInsured;
        this.covers = new ArrayList<>();
        this.questions = new ArrayList<>();
        this.icon = icon;
    }

    public void addCover(String code, String name, String description, boolean isOptional, BigDecimal sumInsured) {
        covers.add(new Cover(code, name, description, isOptional, sumInsured));
    }

    public void addQuestions(List<Question> questions) {
        if (this.questions == null) {
            this.questions = new ArrayList<>();
        }
        this.questions.addAll(questions);
    }
}
