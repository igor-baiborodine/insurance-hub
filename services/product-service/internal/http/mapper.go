package producthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

var errInvalidDomainProduct = errors.New("invalid domain product")

type productDTO struct {
	Code               string        `json:"code"`
	Name               string        `json:"name,omitempty"`
	Image              string        `json:"image,omitempty"`
	Description        string        `json:"description,omitempty"`
	Covers             []coverDTO    `json:"covers,omitempty"`
	Questions          []questionDTO `json:"questions,omitempty"`
	MaxNumberOfInsured int32         `json:"maxNumberOfInsured"`
	Icon               string        `json:"icon,omitempty"`
}

type coverDTO struct {
	Code        string          `json:"code,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Optional    bool            `json:"optional"`
	SumInsured  json.RawMessage `json:"sumInsured,omitempty"`
}

type questionDTO struct {
	Type    string      `json:"type"`
	Code    string      `json:"code,omitempty"`
	Index   int32       `json:"index"`
	Text    string      `json:"text,omitempty"`
	Choices []choiceDTO `json:"choices,omitempty"`
}

type choiceDTO struct {
	Code  string `json:"code,omitempty"`
	Label string `json:"label,omitempty"`
}

func mapProduct(ctx context.Context, product domain.Product) (productDTO, error) {
	if err := ctx.Err(); err != nil {
		return productDTO{}, err
	}
	if product.Code == "" {
		return productDTO{}, fmt.Errorf(
			"%w: product code is empty",
			errInvalidDomainProduct,
		)
	}
	mapped := productDTO{
		Code:               product.Code,
		Name:               product.Name,
		Image:              product.Image,
		Description:        product.Description,
		MaxNumberOfInsured: product.MaxNumberOfInsured,
		Icon:               product.Icon,
	}
	if len(product.Covers) != 0 {
		mapped.Covers = make([]coverDTO, 0, len(product.Covers))
	}
	for _, cover := range product.Covers {
		if err := ctx.Err(); err != nil {
			return productDTO{}, err
		}
		mappedCover := coverDTO{
			Code: cover.Code, Name: cover.Name, Description: cover.Description,
			Optional: cover.Optional,
		}
		if cover.SumInsured != nil {
			decimal := cover.SumInsured.String()
			if decimal == "" || !json.Valid([]byte(decimal)) {
				return productDTO{}, fmt.Errorf(
					"%w: cover decimal is invalid",
					errInvalidDomainProduct,
				)
			}
			mappedCover.SumInsured = json.RawMessage(decimal)
		}
		mapped.Covers = append(mapped.Covers, mappedCover)
	}
	if len(product.Questions) != 0 {
		mapped.Questions = make([]questionDTO, 0, len(product.Questions))
	}
	for _, question := range product.Questions {
		if err := ctx.Err(); err != nil {
			return productDTO{}, err
		}
		mappedQuestion := questionDTO{
			Code: question.Code, Index: question.Index, Text: question.Text,
		}
		switch kind := question.Kind().(type) {
		case domain.ChoiceQuestion:
			mappedQuestion.Type = "choice"
			if len(kind.Choices) != 0 {
				mappedQuestion.Choices = make([]choiceDTO, 0, len(kind.Choices))
			}
			for _, choice := range kind.Choices {
				if err := ctx.Err(); err != nil {
					return productDTO{}, err
				}
				mappedQuestion.Choices = append(mappedQuestion.Choices, choiceDTO{
					Code: choice.Code, Label: choice.Label,
				})
			}
		case domain.DateQuestion:
			mappedQuestion.Type = "date"
		case domain.NumericQuestion:
			mappedQuestion.Type = "numeric"
		default:
			return productDTO{}, fmt.Errorf(
				"%w: question kind is missing",
				errInvalidDomainProduct,
			)
		}
		mapped.Questions = append(mapped.Questions, mappedQuestion)
	}
	if err := ctx.Err(); err != nil {
		return productDTO{}, err
	}
	return mapped, nil
}
