package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
)

var errInvalidRPCProduct = errors.New("invalid RPC product")

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

func mapGatewayResponse(ctx context.Context, value any) (any, bool, error) {
	switch response := value.(type) {
	case *productv1.Product:
		mapped, err := mapProduct(ctx, response)
		return mapped, true, err
	case []*productv1.Product:
		mapped := make([]productDTO, 0, len(response))
		for _, product := range response {
			item, err := mapProduct(ctx, product)
			if err != nil {
				return nil, true, err
			}
			mapped = append(mapped, item)
		}
		return mapped, true, nil
	default:
		return nil, false, nil
	}
}

func mapProduct(ctx context.Context, product *productv1.Product) (productDTO, error) {
	if err := ctx.Err(); err != nil {
		return productDTO{}, err
	}
	if product == nil || product.GetCode() == "" {
		return productDTO{}, fmt.Errorf("%w: product code is empty", errInvalidRPCProduct)
	}
	mapped := productDTO{
		Code:               product.GetCode(),
		Name:               product.GetName(),
		Image:              product.GetImage(),
		Description:        product.GetDescription(),
		MaxNumberOfInsured: product.GetMaxNumberOfInsured(),
		Icon:               product.GetIcon(),
	}
	if len(product.GetCovers()) != 0 {
		mapped.Covers = make([]coverDTO, 0, len(product.GetCovers()))
	}
	for _, cover := range product.GetCovers() {
		if err := ctx.Err(); err != nil {
			return productDTO{}, err
		}
		if cover == nil {
			return productDTO{}, fmt.Errorf(
				"%w: cover is missing",
				errInvalidRPCProduct,
			)
		}
		mappedCover := coverDTO{
			Code:        cover.GetCode(),
			Name:        cover.GetName(),
			Description: cover.GetDescription(),
			Optional:    cover.GetOptional(),
		}
		if cover.SumInsured != nil {
			decimal := cover.GetSumInsured()
			if decimal == "" || !json.Valid([]byte(decimal)) {
				return productDTO{}, fmt.Errorf(
					"%w: cover decimal is invalid",
					errInvalidRPCProduct,
				)
			}
			mappedCover.SumInsured = json.RawMessage(decimal)
		}
		mapped.Covers = append(mapped.Covers, mappedCover)
	}
	if len(product.GetQuestions()) != 0 {
		mapped.Questions = make([]questionDTO, 0, len(product.GetQuestions()))
	}
	for _, question := range product.GetQuestions() {
		if err := ctx.Err(); err != nil {
			return productDTO{}, err
		}
		if question == nil {
			return productDTO{}, fmt.Errorf(
				"%w: question is missing",
				errInvalidRPCProduct,
			)
		}
		mappedQuestion := questionDTO{
			Code:  question.GetCode(),
			Index: question.GetIndex(),
			Text:  question.GetText(),
		}
		switch kind := question.Kind.(type) {
		case *productv1.Question_Choice:
			mappedQuestion.Type = "choice"
			if kind.Choice != nil && len(kind.Choice.GetChoices()) != 0 {
				mappedQuestion.Choices = make(
					[]choiceDTO,
					0,
					len(kind.Choice.GetChoices()),
				)
			}
			if kind.Choice != nil {
				for _, choice := range kind.Choice.GetChoices() {
					if choice == nil {
						return productDTO{}, fmt.Errorf(
							"%w: choice is missing",
							errInvalidRPCProduct,
						)
					}
					mappedQuestion.Choices = append(
						mappedQuestion.Choices,
						choiceDTO{
							Code:  choice.GetCode(),
							Label: choice.GetLabel(),
						},
					)
				}
			}
		case *productv1.Question_Date:
			mappedQuestion.Type = "date"
		case *productv1.Question_Numeric:
			mappedQuestion.Type = "numeric"
		default:
			return productDTO{}, fmt.Errorf(
				"%w: question kind is missing",
				errInvalidRPCProduct,
			)
		}
		mapped.Questions = append(mapped.Questions, mappedQuestion)
	}
	return mapped, nil
}
