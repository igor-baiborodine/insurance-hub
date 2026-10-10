package contract_test

import (
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
)

func TestProductBinary_PresenceAndDefaults(t *testing.T) {
	tests := []struct {
		name    string
		decimal *string
	}{
		{name: "absent"},
		{name: "zero with scale", decimal: proto.String("0.00")},
		{name: "trailing scale", decimal: proto.String("12.3400")},
		{
			name:    "large exact value",
			decimal: proto.String("12345678901234567890.123456789"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			original := &productv1.GetProductResponse{Product: &productv1.Product{
				Code: "ONLY",
				Covers: []*productv1.Cover{{
					Code:       "COVER",
					Optional:   false,
					SumInsured: test.decimal,
				}},
				Questions: []*productv1.Question{
					{
						Code: "Q",
						Kind: &productv1.Question_Numeric{
							Numeric: &productv1.NumericQuestion{},
						},
					},
				},
			}}
			// when
			wire, err := proto.Marshal(original)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var restored productv1.GetProductResponse
			if err := proto.Unmarshal(wire, &restored); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			// then
			if !proto.Equal(original, &restored) {
				t.Errorf("binary round trip changed Product: %v", &restored)
			}
			cover := restored.GetProduct().GetCovers()[0]
			if (cover.SumInsured == nil) != (test.decimal == nil) {
				t.Errorf("decimal presence changed: %v", cover.SumInsured)
			}
			if test.decimal != nil && cover.GetSumInsured() != *test.decimal {
				t.Errorf(
					"decimal = %q, want %q",
					cover.GetSumInsured(),
					*test.decimal,
				)
			}
			if restored.GetProduct().GetMaxNumberOfInsured() != 0 ||
				restored.GetProduct().GetQuestions()[0].GetIndex() != 0 || cover.GetOptional() {
				t.Error("int32 or boolean defaults changed")
			}
		})
	}
}

func TestQuestionVariants_SurviveBinaryRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		question *productv1.Question
	}{
		{
			name: "choice",
			question: &productv1.Question{
				Code: "Q",
				Kind: &productv1.Question_Choice{Choice: &productv1.ChoiceQuestion{
					Choices: []*productv1.Choice{{Code: "A"}, {Code: "B"}},
				}},
			},
		},
		{
			name: "date",
			question: &productv1.Question{
				Code: "Q",
				Kind: &productv1.Question_Date{Date: &productv1.DateQuestion{}},
			},
		},
		{
			name: "numeric",
			question: &productv1.Question{
				Code: "Q",
				Kind: &productv1.Question_Numeric{
					Numeric: &productv1.NumericQuestion{},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			original := test.question
			// when
			wire, err := proto.Marshal(original)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var restored productv1.Question
			if err := proto.Unmarshal(wire, &restored); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			// then
			if !proto.Equal(original, &restored) {
				t.Errorf("question variant changed: %v", &restored)
			}
		})
	}
}

func TestProductContract_Validation(t *testing.T) {
	validator, err := protovalidate.New(
		protovalidate.WithMessages(
			&productv1.GetProductRequest{},
			&productv1.GetProductResponse{},
			&productv1.Question{},
		),
		protovalidate.WithDisableLazy(),
	)
	if err != nil {
		t.Fatalf("construct validator: %v", err)
	}
	tests := []struct {
		name      string
		message   proto.Message
		wantValid bool
	}{
		{name: "empty code", message: &productv1.GetProductRequest{}},
		{
			name:      "opaque code",
			message:   &productv1.GetProductRequest{Code: " ONLY/EXTRA "},
			wantValid: true,
		},
		{name: "missing response product", message: &productv1.GetProductResponse{}},
		{name: "missing question kind", message: &productv1.Question{Code: "Q"}},
		{
			name: "choice question",
			message: &productv1.Question{Kind: &productv1.Question_Choice{
				Choice: &productv1.ChoiceQuestion{},
			}},
			wantValid: true,
		},
		{
			name: "numeric question",
			message: &productv1.Question{Kind: &productv1.Question_Numeric{
				Numeric: &productv1.NumericQuestion{},
			}},
			wantValid: true,
		},
		{
			name: "present empty decimal",
			message: &productv1.GetProductResponse{Product: &productv1.Product{
				Covers: []*productv1.Cover{{SumInsured: proto.String("")}},
				Questions: []*productv1.Question{
					{
						Kind: &productv1.Question_Date{
							Date: &productv1.DateQuestion{},
						},
					},
				},
			}},
		},
		{
			name: "absent decimal and date",
			message: &productv1.GetProductResponse{Product: &productv1.Product{
				Covers: []*productv1.Cover{{}},
				Questions: []*productv1.Question{
					{
						Kind: &productv1.Question_Date{
							Date: &productv1.DateQuestion{},
						},
					},
				},
			}},
			wantValid: true,
		},
		{
			name: "invalid decimal",
			message: &productv1.GetProductResponse{Product: &productv1.Product{
				Covers: []*productv1.Cover{
					{SumInsured: proto.String("not-a-number")},
				},
			}},
		},
		{
			name: "valid decimal scale",
			message: &productv1.GetProductResponse{Product: &productv1.Product{
				Covers: []*productv1.Cover{{SumInsured: proto.String("12.3400")}},
				Questions: []*productv1.Question{
					{
						Kind: &productv1.Question_Date{
							Date: &productv1.DateQuestion{},
						},
					},
				},
			}},
			wantValid: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := validator.Validate(test.message)
			// then
			if (err == nil) != test.wantValid {
				t.Errorf(
					"validation error = %v, want valid = %t",
					err,
					test.wantValid,
				)
			}
		})
	}
}
