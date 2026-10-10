package postgres

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

type edgeFixture struct {
	Cases []struct {
		ID   string `json:"id"`
		Rows []struct {
			Code              *string `json:"code"`
			RawDefinitionJSON *string `json:"rawDefinitionJson"`
		} `json:"rows"`
		Expected struct {
			Outcome string `json:"outcome"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestAcceptedDataEdgesDecodeWithoutChangingFixtures(t *testing.T) {
	// given
	path := filepath.Join(
		"..", "..", "..", "..", "legacy", "product-service", "src", "test", "resources",
		"product-read-baseline", "data-edges", "cases.json",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture edgeFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 16 {
		t.Fatalf("accepted edge count changed: %d", len(fixture.Cases))
	}
	checked := 0
	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			if testCase.Expected.Outcome == "constraint-rejected" {
				return // PostgreSQL NOT NULL behavior belongs to the real reader tests.
			}
			if len(testCase.Rows) == 0 {
				t.Fatal("expected decodable rows")
			}
			checked++
			for _, row := range testCase.Rows {
				if row.Code == nil || row.RawDefinitionJSON == nil {
					t.Fatal("row has no decoder input")
				}

				// when
				product, err := DecodeProduct(
					*row.Code,
					[]byte(*row.RawDefinitionJSON),
				)

				// then
				switch testCase.Expected.Outcome {
				case "http-success":
					if err != nil || product.Code != *row.Code {
						t.Fatalf(
							"accepted success: product=%+v error=%v",
							product,
							err,
						)
					}
					assertAcceptedProduct(t, testCase.ID, product)
				case "http-failure":
					if !errors.Is(err, application.ErrInvalidDefinition) ||
						product.Code != "" ||
						len(product.Covers) != 0 ||
						len(product.Questions) != 0 {
						t.Fatalf(
							"accepted failure: product=%+v error=%v",
							product,
							err,
						)
					}
					if strings.Contains(err.Error(), *row.Code) ||
						strings.Contains(
							err.Error(),
							*row.RawDefinitionJSON,
						) {
						t.Errorf(
							"stored data leaked into decode error: %v",
							err,
						)
					}
				default:
					t.Fatalf(
						"unexpected fixture outcome %q",
						testCase.Expected.Outcome,
					)
				}
			}
		})
	}
	if checked != 14 {
		t.Errorf("decoded %d edge cases, want 14", checked)
	}
}

func assertAcceptedProduct(t *testing.T, caseID string, product domain.Product) {
	t.Helper()
	switch caseID {
	case "DATA-EDGES-RICH-001":
		if product.Name != "Rich semantics" || product.Image != "/edge.png" ||
			product.Description != "Synthetic" || product.Icon != "edge" ||
			product.MaxNumberOfInsured != 7 || len(product.Covers) != 4 ||
			len(product.Questions) != 3 {
			t.Fatalf("rich fields changed: %+v", product)
		}
		for index, want := range []string{"12.3400", "12345678901234567890.123456789", "0.00"} {
			if product.Covers[index].SumInsured == nil ||
				product.Covers[index].SumInsured.String() != want {
				t.Errorf("accepted decimal %d changed", index)
			}
		}
		if product.Covers[3].SumInsured != nil {
			t.Error("accepted null decimal became present")
		}
		choice, ok := product.Questions[0].Kind().(domain.ChoiceQuestion)
		if !ok || len(choice.Choices) != 2 || choice.Choices[0].Code != "B" ||
			choice.Choices[1].Code != "A" {
			t.Error("accepted choice order changed")
		}
		if _, ok := product.Questions[1].Kind().(domain.DateQuestion); !ok {
			t.Error("accepted date variant changed")
		}
		if _, ok := product.Questions[2].Kind().(domain.NumericQuestion); !ok {
			t.Error("accepted numeric variant changed")
		}
	case "DATA-EDGES-ABSENT-001", "DATA-EDGES-EMPTY-001":
		if len(product.Covers) != 0 || len(product.Questions) != 0 ||
			product.MaxNumberOfInsured != 0 || product.Image != "" || product.Icon != "" {
			t.Errorf("accepted empty/default semantics changed: %+v", product)
		}
	case "DATA-EDGES-PRIMITIVE-NULL-001":
		if product.MaxNumberOfInsured != 0 || len(product.Covers) != 1 ||
			product.Covers[0].Optional || len(product.Questions) != 1 ||
			product.Questions[0].Index != 0 {
			t.Errorf("accepted primitive null defaults changed: %+v", product)
		}
	case "DATA-EDGES-CHOICE-PRESENCE-001":
		if len(product.Questions) != 3 {
			t.Fatalf("accepted choice question count changed: %+v", product)
		}
		for _, question := range product.Questions {
			choice, ok := question.Kind().(domain.ChoiceQuestion)
			if !ok || len(choice.Choices) != 0 {
				t.Errorf("accepted choice presence changed: %+v", question)
			}
		}
	}
}

func TestRichDefinitionPreservesDecimalTokensVariantsAndOrder(t *testing.T) {
	// given
	raw := []byte(
		`{"name":"Rich","covers":[{"sumInsured":12.3400},{"sumInsured":12345678901234567890.123456789},{"sumInsured":0.00},{"sumInsured":null}],"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]},{"type":"date"},{"type":"numeric"}],"ignoredTopLevel":true}`,
	)

	// when
	product, err := DecodeProduct("Exact / code", raw)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if product.Code != "Exact / code" || product.Name != "Rich" || len(product.Covers) != 4 ||
		len(product.Questions) != 3 {
		t.Fatalf("lost product fields: %+v", product)
	}
	for index, want := range []string{"12.3400", "12345678901234567890.123456789", "0.00"} {
		if product.Covers[index].SumInsured == nil ||
			product.Covers[index].SumInsured.String() != want {
			t.Errorf("cover %d decimal: %+v", index, product.Covers[index].SumInsured)
		}
	}
	if product.Covers[3].SumInsured != nil {
		t.Error("null amount became present")
	}
	choice, ok := product.Questions[0].Kind().(domain.ChoiceQuestion)
	if !ok || len(choice.Choices) != 2 || choice.Choices[0].Code != "B" ||
		choice.Choices[1].Code != "A" {
		t.Errorf("choice variant/order: %+v", product.Questions[0].Kind())
	}
	if _, ok := product.Questions[1].Kind().(domain.DateQuestion); !ok {
		t.Error("date variant changed")
	}
	if _, ok := product.Questions[2].Kind().(domain.NumericQuestion); !ok {
		t.Error("numeric variant changed")
	}
}

func TestDefaultsAndMalformedShapes(t *testing.T) {
	// given
	valid := []struct {
		name string
		raw  string
	}{
		{"absent", `{}`},
		{
			"primitive null",
			`{"name":null,"maxNumberOfInsured":null,"covers":[{"optional":null}],"questions":[{"type":"numeric","index":null}]}`,
		},
		{"empty", `{"covers":[],"questions":[{"type":"choice","choices":null}]}`},
	}
	for _, testCase := range valid {
		t.Run(testCase.name, func(t *testing.T) {
			// when
			product, err := DecodeProduct("EXACT", []byte(testCase.raw))
			// then
			if err != nil || product.Code != "EXACT" ||
				product.MaxNumberOfInsured != 0 {
				t.Fatalf("defaults: product=%+v error=%v", product, err)
			}
			if testCase.name == "primitive null" &&
				(len(product.Covers) != 1 || product.Covers[0].Optional ||
					len(product.Questions) != 1 || product.Questions[0].Index != 0) {
				t.Errorf("primitive defaults changed: %+v", product)
			}
		})
	}
	invalid := []string{
		`null`,
		`[]`,
		`{"covers":null}`,
		`{"questions":null}`,
		`{"covers":{}}`,
		`{"questions":[null]}`,
		`{"covers":[null]}`,
		`{"covers":[{"sumInsured":"1.00"}]}`,
		`{"covers":[{"sumInsured":true}]}`,
		`{"covers":[{"optional":"false"}]}`,
		`{"questions":[{"type":"choice","choices":[null]}]}`,
		`{"questions":[{"type":"numeric","choices":[]}]}`,
		`{"questions":[{"type":"choice","choices":{}}]}`,
		`{"maxNumberOfInsured":2147483648}`,
		`{"questions":[{"type":"date","index":1.5}]}`,
		`{"name":42}`,
		`{"questions":[{"type":"numeric","unexpected":true}]}`,
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			// when
			product, err := DecodeProduct("EXACT", []byte(raw))
			// then
			if !errors.Is(err, application.ErrInvalidDefinition) || product.Code != "" {
				t.Errorf(
					"invalid storage accepted: product=%+v error=%v",
					product,
					err,
				)
			}
		})
	}
}
