package integrationtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const genericServerErrorBody = `{"message":"Internal Server Error"}`

// HTTPExpectation is the stable part of an accepted direct HTTP observation.
// Headers other than Content-Type are deliberately outside parity comparison.
type HTTPExpectation struct {
	Status      int
	ContentType string
	Body        []byte
}

// CompareHTTP applies the accepted direct HTTP normalization rules. JSON object-key order and
// top-level product-list order are ignored. Number tokens, field presence, nested-array order,
// status, and content type remain exact.
func CompareHTTP(expected, actual HTTPExpectation) error {
	if expected.Status != actual.Status {
		return fmt.Errorf("HTTP status: got %d, want %d", actual.Status, expected.Status)
	}
	if expected.ContentType != actual.ContentType {
		return fmt.Errorf(
			"HTTP content type: got %q, want %q",
			actual.ContentType,
			expected.ContentType,
		)
	}
	want, err := decodeLosslessJSON(expected.Body)
	if err != nil {
		return fmt.Errorf("decode expected HTTP body: %w", err)
	}
	got, err := decodeLosslessJSON(actual.Body)
	if err != nil {
		return fmt.Errorf("decode actual HTTP body: %w", err)
	}
	if err := compareJSON("$", want, got, true); err != nil {
		return fmt.Errorf("HTTP body: %w", err)
	}
	return nil
}

// GoHTTPExpectation retains accepted Java observations except for explicitly approved unsafe
// server-error cases. It never rewrites the shared fixture.
func GoHTTPExpectation(caseID string, observation HTTPExpectation) (HTTPExpectation, error) {
	if observation.Status != 500 {
		return observation, nil
	}
	if _, ok := goServerErrorCases[caseID]; !ok {
		return HTTPExpectation{}, fmt.Errorf(
			"case %q has no approved Go server-error overlay",
			caseID,
		)
	}
	return HTTPExpectation{
		Status:      observation.Status,
		ContentType: "application/json",
		Body:        []byte(genericServerErrorBody),
	}, nil
}

var goServerErrorCases = map[string]struct{}{
	"FAIL-PRODUCT-DECODE-TYPE-001":      {},
	"FAIL-PRODUCT-DECODE-STRUCTURE-001": {},
	"FAIL-PRODUCT-DATABASE-LIST-001":    {},
	"FAIL-PRODUCT-DATABASE-GET-001":     {},
	"DATA-EDGES-NULL-COVERS-001":        {},
	"DATA-EDGES-NULL-QUESTIONS-001":     {},
	"DATA-EDGES-UNKNOWN-COVER-001":      {},
	"DATA-EDGES-UNKNOWN-QUESTION-001":   {},
	"DATA-EDGES-UNKNOWN-CHOICE-001":     {},
	"DATA-EDGES-SUBTYPE-UNKNOWN-001":    {},
	"DATA-EDGES-SUBTYPE-ABSENT-001":     {},
	"DATA-EDGES-SUBTYPE-NULL-001":       {},
}

func decodeLosslessJSON(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func compareJSON(path string, expected, actual any, topLevel bool) error {
	switch want := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			return typeMismatch(path, expected, actual)
		}
		if err := compareKeys(path, want, got); err != nil {
			return err
		}
		keys := make([]string, 0, len(want))
		for key := range want {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := compareJSON(
				path+"."+key,
				want[key],
				got[key],
				false,
			); err != nil {
				return err
			}
		}
		return nil
	case []any:
		got, ok := actual.([]any)
		if !ok {
			return typeMismatch(path, expected, actual)
		}
		if topLevel {
			return compareProductLists(path, want, got)
		}
		if len(want) != len(got) {
			return fmt.Errorf("%s length: got %d, want %d", path, len(got), len(want))
		}
		for index := range want {
			if err := compareJSON(
				fmt.Sprintf("%s[%d]", path, index),
				want[index],
				got[index],
				false,
			); err != nil {
				return err
			}
		}
		return nil
	case json.Number:
		got, ok := actual.(json.Number)
		if !ok {
			return typeMismatch(path, expected, actual)
		}
		if want.String() != got.String() {
			return fmt.Errorf("%s number token: got %q, want %q", path, got, want)
		}
		return nil
	default:
		if fmt.Sprintf("%T", expected) != fmt.Sprintf("%T", actual) {
			return typeMismatch(path, expected, actual)
		}
		if expected != actual {
			return fmt.Errorf("%s value: got %v, want %v", path, actual, expected)
		}
		return nil
	}
}

func compareKeys(path string, expected, actual map[string]any) error {
	for key := range expected {
		if _, ok := actual[key]; !ok {
			return fmt.Errorf("%s missing field %q", path, key)
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("%s unexpected field %q", path, key)
		}
	}
	return nil
}

func compareProductLists(path string, expected, actual []any) error {
	want, err := productsByCode(path, expected)
	if err != nil {
		return fmt.Errorf("expected product list: %w", err)
	}
	got, err := productsByCode(path, actual)
	if err != nil {
		return fmt.Errorf("actual product list: %w", err)
	}
	if err := compareKeys(path, want, got); err != nil {
		return err
	}
	codes := make([]string, 0, len(want))
	for code := range want {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		if err := compareJSON(
			path+"[code="+code+"]",
			want[code],
			got[code],
			false,
		); err != nil {
			return err
		}
	}
	return nil
}

func productsByCode(path string, products []any) (map[string]any, error) {
	result := make(map[string]any, len(products))
	for index, value := range products {
		product, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not an object", path, index)
		}
		code, ok := product["code"].(string)
		if !ok || strings.TrimSpace(code) == "" {
			return nil, fmt.Errorf("%s[%d] has no non-empty string code", path, index)
		}
		if _, duplicate := result[code]; duplicate {
			return nil, fmt.Errorf("%s has duplicate product code %q", path, code)
		}
		result[code] = product
	}
	return result, nil
}

func typeMismatch(path string, expected, actual any) error {
	return fmt.Errorf("%s type: got %T, want %T", path, actual, expected)
}

// GRPCProductExpectation is deliberately independent of generated protobuf types and JSON
// serialization. Later adapter tests compare generated-client responses to these typed values.
type GRPCProductExpectation struct {
	Code               string
	Name               string
	Image              string
	Description        string
	Covers             []GRPCCoverExpectation
	Questions          []GRPCQuestionExpectation
	MaxNumberOfInsured int32
	Icon               string
}

type GRPCCoverExpectation struct {
	Code, Name, Description string
	Optional                bool
	SumInsured              *string
}

type GRPCQuestionExpectation struct {
	Code, Text string
	Index      int32
	Kind       string
	Choices    []GRPCChoiceExpectation
}

type GRPCChoiceExpectation struct{ Code, Label string }

// ParseGRPCExpectations maps an accepted HTTP success body into transport-neutral typed values.
// It preserves decimal lexemes and derives variants from the legacy discriminator.
func ParseGRPCExpectations(body []byte) ([]GRPCProductExpectation, error) {
	value, err := decodeLosslessJSON(body)
	if err != nil {
		return nil, fmt.Errorf("decode gRPC expectation source: %w", err)
	}
	objects := []any{value}
	if list, ok := value.([]any); ok {
		objects = list
	}
	products := make([]GRPCProductExpectation, 0, len(objects))
	for index, object := range objects {
		product, err := parseGRPCProduct(object)
		if err != nil {
			return nil, fmt.Errorf("product %d: %w", index, err)
		}
		products = append(products, product)
	}
	return products, nil
}

func parseGRPCProduct(value any) (GRPCProductExpectation, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return GRPCProductExpectation{}, fmt.Errorf("got %T, want object", value)
	}
	var product GRPCProductExpectation
	product.Code, _ = object["code"].(string)
	product.Name, _ = object["name"].(string)
	product.Image, _ = object["image"].(string)
	product.Description, _ = object["description"].(string)
	product.Icon, _ = object["icon"].(string)
	if number, ok := object["maxNumberOfInsured"].(json.Number); ok {
		value, parseErr := number.Int64()
		if parseErr != nil || value < -2147483648 || value > 2147483647 {
			return GRPCProductExpectation{}, fmt.Errorf(
				"invalid maxNumberOfInsured %q",
				number,
			)
		}
		product.MaxNumberOfInsured = int32(value)
	}
	for index, value := range optionalArray(object, "covers") {
		cover, err := parseGRPCCover(value)
		if err != nil {
			return GRPCProductExpectation{}, fmt.Errorf("cover %d: %w", index, err)
		}
		product.Covers = append(product.Covers, cover)
	}
	for index, value := range optionalArray(object, "questions") {
		question, err := parseGRPCQuestion(value)
		if err != nil {
			return GRPCProductExpectation{}, fmt.Errorf("question %d: %w", index, err)
		}
		product.Questions = append(product.Questions, question)
	}
	return product, nil
}

func parseGRPCCover(value any) (GRPCCoverExpectation, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return GRPCCoverExpectation{}, fmt.Errorf("got %T, want object", value)
	}
	cover := GRPCCoverExpectation{}
	cover.Code, _ = object["code"].(string)
	cover.Name, _ = object["name"].(string)
	cover.Description, _ = object["description"].(string)
	cover.Optional, _ = object["optional"].(bool)
	if number, ok := object["sumInsured"].(json.Number); ok {
		lexeme := number.String()
		cover.SumInsured = &lexeme
	}
	return cover, nil
}

func parseGRPCQuestion(value any) (GRPCQuestionExpectation, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return GRPCQuestionExpectation{}, fmt.Errorf("got %T, want object", value)
	}
	question := GRPCQuestionExpectation{}
	question.Code, _ = object["code"].(string)
	question.Text, _ = object["text"].(string)
	question.Kind, _ = object["type"].(string)
	if question.Kind != "choice" && question.Kind != "date" && question.Kind != "numeric" {
		return GRPCQuestionExpectation{}, fmt.Errorf(
			"invalid question type %q",
			question.Kind,
		)
	}
	if number, ok := object["index"].(json.Number); ok {
		value, parseErr := number.Int64()
		if parseErr != nil || value < -2147483648 || value > 2147483647 {
			return GRPCQuestionExpectation{}, fmt.Errorf(
				"invalid question index %q",
				number,
			)
		}
		question.Index = int32(value)
	}
	if question.Kind != "choice" {
		if _, exists := object["choices"]; exists {
			return GRPCQuestionExpectation{}, errors.New(
				"non-choice question has choices",
			)
		}
		return question, nil
	}
	for index, value := range optionalArray(object, "choices") {
		choice, ok := value.(map[string]any)
		if !ok {
			return GRPCQuestionExpectation{}, fmt.Errorf(
				"choice %d: got %T, want object",
				index,
				value,
			)
		}
		question.Choices = append(question.Choices, GRPCChoiceExpectation{
			Code:  stringValue(choice, "code"),
			Label: stringValue(choice, "label"),
		})
	}
	return question, nil
}

func optionalArray(object map[string]any, key string) []any {
	values, _ := object[key].([]any)
	return values
}

func stringValue(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}
