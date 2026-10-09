// Package postgres maps stored Product rows into domain values.
package postgres

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

type productDefinition struct {
	Name               *string         `json:"name"`
	Image              *string         `json:"image"`
	Description        *string         `json:"description"`
	Covers             json.RawMessage `json:"covers"`
	Questions          json.RawMessage `json:"questions"`
	MaxNumberOfInsured *int32          `json:"maxNumberOfInsured"`
	Icon               *string         `json:"icon"`
}

type coverDefinition struct {
	Code        *string         `json:"code"`
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Optional    *bool           `json:"optional"`
	SumInsured  json.RawMessage `json:"sumInsured"`
}

type questionFields struct {
	Type  *string `json:"type"`
	Code  *string `json:"code"`
	Index *int32  `json:"index"`
	Text  *string `json:"text"`
}

type choiceQuestionDefinition struct {
	questionFields
	Choices json.RawMessage `json:"choices"`
}

type choiceDefinition struct {
	Code  *string `json:"code"`
	Label *string `json:"label"`
}

// DecodeProduct accepts a SQL key separately from the raw definition JSONB.
// It never returns a partly decoded Product or exposes the stored definition in errors.
func DecodeProduct(code string, rawDefinition []byte) (domain.Product, error) {
	var definition productDefinition
	if err := decodeObject(rawDefinition, &definition, false); err != nil {
		return domain.Product{}, corrupt("definition")
	}
	covers, err := decodeCovers(definition.Covers)
	if err != nil {
		return domain.Product{}, err
	}
	questions, err := decodeQuestions(definition.Questions)
	if err != nil {
		return domain.Product{}, err
	}
	return domain.Product{
		Code:               code,
		Name:               value(definition.Name),
		Image:              value(definition.Image),
		Description:        value(definition.Description),
		Covers:             covers,
		Questions:          questions,
		MaxNumberOfInsured: integer(definition.MaxNumberOfInsured),
		Icon:               value(definition.Icon),
	}, nil
}

func decodeCovers(raw json.RawMessage) ([]domain.Cover, error) {
	items, err := decodeArray(raw, false, "covers")
	if err != nil {
		return nil, err
	}
	covers := make([]domain.Cover, 0, len(items))
	for _, item := range items {
		var stored coverDefinition
		if err := decodeObject(item, &stored, true); err != nil {
			return nil, corrupt("cover")
		}
		cover := domain.Cover{
			Code:        value(stored.Code),
			Name:        value(stored.Name),
			Description: value(stored.Description),
			Optional:    boolean(stored.Optional),
		}
		if present(stored.SumInsured) {
			amount, err := domain.NewDecimal(string(stored.SumInsured))
			if err != nil {
				return nil, corrupt("sumInsured")
			}
			cover.SumInsured = &amount
		}
		covers = append(covers, cover)
	}
	return covers, nil
}

func decodeQuestions(raw json.RawMessage) ([]domain.Question, error) {
	items, err := decodeArray(raw, false, "questions")
	if err != nil {
		return nil, err
	}
	questions := make([]domain.Question, 0, len(items))
	for _, item := range items {
		var header questionFields
		if err := decodeObject(item, &header, false); err != nil || header.Type == nil {
			return nil, corrupt("question")
		}
		var kind domain.QuestionKind
		switch *header.Type {
		case "choice":
			var stored choiceQuestionDefinition
			if err := decodeObject(item, &stored, true); err != nil {
				return nil, corrupt("choice question")
			}
			choices, err := decodeChoices(stored.Choices)
			if err != nil {
				return nil, err
			}
			kind = domain.ChoiceQuestion{Choices: choices}
		case "date", "numeric":
			if err := decodeObject(item, &header, true); err != nil {
				return nil, corrupt("question")
			}
			if *header.Type == "date" {
				kind = domain.DateQuestion{}
			} else {
				kind = domain.NumericQuestion{}
			}
		default:
			return nil, corrupt("question type")
		}
		question, err := domain.NewQuestion(
			value(header.Code), integer(header.Index), value(header.Text), kind,
		)
		if err != nil {
			return nil, corrupt("question")
		}
		questions = append(questions, question)
	}
	return questions, nil
}

func decodeChoices(raw json.RawMessage) ([]domain.Choice, error) {
	items, err := decodeArray(raw, true, "choices")
	if err != nil {
		return nil, err
	}
	choices := make([]domain.Choice, 0, len(items))
	for _, item := range items {
		var stored choiceDefinition
		if err := decodeObject(item, &stored, true); err != nil {
			return nil, corrupt("choice")
		}
		choices = append(
			choices,
			domain.Choice{Code: value(stored.Code), Label: value(stored.Label)},
		)
	}
	return choices, nil
}

func decodeArray(raw json.RawMessage, allowNull bool, field string) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (allowNull && bytes.Equal(raw, []byte("null"))) {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, corrupt(field)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, corrupt(field)
	}
	return items, nil
}

func decodeObject(raw []byte, destination any, strict bool) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return application.ErrInvalidDefinition
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return application.ErrInvalidDefinition
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return application.ErrInvalidDefinition
	}
	return nil
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}

func integer(input *int32) int32 {
	if input == nil {
		return 0
	}
	return *input
}

func boolean(input *bool) bool {
	return input != nil && *input
}

func present(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null"))
}

func corrupt(field string) error {
	return fmt.Errorf("%w: %s", application.ErrInvalidDefinition, field)
}
