package productgrpc

import (
	"errors"
	"fmt"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

var errInvalidDomainProduct = errors.New("invalid domain product")

func mapProduct(product domain.Product) (*productv1.Product, error) {
	mapped := &productv1.Product{
		Code:               product.Code,
		Name:               product.Name,
		Image:              product.Image,
		Description:        product.Description,
		Covers:             make([]*productv1.Cover, 0, len(product.Covers)),
		Questions:          make([]*productv1.Question, 0, len(product.Questions)),
		MaxNumberOfInsured: product.MaxNumberOfInsured,
		Icon:               product.Icon,
	}
	if mapped.Code == "" {
		return nil, fmt.Errorf("%w: product code is empty", errInvalidDomainProduct)
	}
	for _, cover := range product.Covers {
		mappedCover := &productv1.Cover{
			Code:        cover.Code,
			Name:        cover.Name,
			Description: cover.Description,
			Optional:    cover.Optional,
		}
		if cover.SumInsured != nil {
			decimal := cover.SumInsured.String()
			if decimal == "" {
				return nil, fmt.Errorf(
					"%w: cover decimal is empty",
					errInvalidDomainProduct,
				)
			}
			mappedCover.SumInsured = &decimal
		}
		mapped.Covers = append(mapped.Covers, mappedCover)
	}
	for _, question := range product.Questions {
		mappedQuestion := &productv1.Question{
			Code:  question.Code,
			Index: question.Index,
			Text:  question.Text,
		}
		switch kind := question.Kind().(type) {
		case domain.ChoiceQuestion:
			choices := make([]*productv1.Choice, 0, len(kind.Choices))
			for _, choice := range kind.Choices {
				choices = append(choices, &productv1.Choice{
					Code: choice.Code, Label: choice.Label,
				})
			}
			mappedQuestion.Kind = &productv1.Question_Choice{
				Choice: &productv1.ChoiceQuestion{Choices: choices},
			}
		case domain.DateQuestion:
			mappedQuestion.Kind = &productv1.Question_Date{
				Date: &productv1.DateQuestion{},
			}
		case domain.NumericQuestion:
			mappedQuestion.Kind = &productv1.Question_Numeric{
				Numeric: &productv1.NumericQuestion{},
			}
		default:
			return nil, fmt.Errorf(
				"%w: question kind is missing",
				errInvalidDomainProduct,
			)
		}
		mapped.Questions = append(mapped.Questions, mappedQuestion)
	}
	return mapped, nil
}
