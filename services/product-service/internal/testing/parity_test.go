package integrationtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type acceptedHTTPCase struct {
	ID       string `json:"id"`
	Target   string `json:"target"`
	Expected struct {
		Status      int    `json:"status"`
		ContentType string `json:"contentType"`
		RawBody     string `json:"rawBody"`
	} `json:"expected"`
}

func TestHTTPComparator_AcceptsOnlyApprovedNormalization(t *testing.T) {
	// given
	expected := HTTPExpectation{
		Status:      200,
		ContentType: "application/json",
		Body: []byte(`[
			{"code":"A","covers":[{"code":"1"}],"sum":12.3400},
			{"code":"B","enabled":false}
		]`),
	}
	actual := HTTPExpectation{
		Status:      200,
		ContentType: "application/json",
		Body: []byte(`[
			{"enabled":false,"code":"B"},
			{"sum":12.3400,"covers":[{"code":"1"}],"code":"A"}
		]`),
	}

	// when
	err := CompareHTTP(expected, actual)
	// then
	if err != nil {
		t.Fatalf("approved normalization rejected: %v", err)
	}
}

func TestHTTPComparator_RejectsControlledRegressions(t *testing.T) {
	base := HTTPExpectation{
		Status:      200,
		ContentType: "application/json",
		Body: []byte(`[
			{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},
			{"code":"B","zero":0,"nullable":null}
		]`),
	}
	tests := []struct {
		name        string
		actual      HTTPExpectation
		wantMessage string
	}{
		{
			"decimal scale",
			responseLike(
				base,
				`[{"code":"A","amount":12.34,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"B","zero":0,"nullable":null}]`,
			),
			"number token",
		},
		{
			"omission",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"B","zero":0,"nullable":null}]`,
			),
			"missing field",
		},
		{
			"false becomes null",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":null,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"B","zero":0,"nullable":null}]`,
			),
			"type",
		},
		{
			"zero omitted",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"B","nullable":null}]`,
			),
			"missing field",
		},
		{
			"null omitted",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"B","zero":0}]`,
			),
			"missing field",
		},
		{
			"question variant",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"numeric","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"B","zero":0,"nullable":null}]`,
			),
			"value",
		},
		{
			"nested order",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"A"},{"code":"B"}]}]},{"code":"B","zero":0,"nullable":null}]`,
			),
			"value",
		},
		{
			"missing product",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]}]`,
			),
			"missing field",
		},
		{
			"extra product",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"B","zero":0,"nullable":null},{"code":"C"}]`,
			),
			"unexpected field",
		},
		{
			"duplicate product",
			responseLike(
				base,
				`[{"code":"A","amount":12.3400,"enabled":false,"questions":[{"type":"choice","choices":[{"code":"B"},{"code":"A"}]}]},{"code":"A"}]`,
			),
			"duplicate product code",
		},
		{
			"status",
			HTTPExpectation{
				Status:      201,
				ContentType: base.ContentType,
				Body:        base.Body,
			},
			"HTTP status",
		},
		{
			"content type",
			HTTPExpectation{
				Status:      base.Status,
				ContentType: "text/plain",
				Body:        base.Body,
			},
			"HTTP content type",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := CompareHTTP(base, test.actual)

			// then
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf(
					"error = %v, want message containing %q",
					err,
					test.wantMessage,
				)
			}
		})
	}
}

func TestGoHTTPExpectation_UsesOnlyExplicitServerErrorOverlay(t *testing.T) {
	// given
	unsafeJava := HTTPExpectation{
		Status:      500,
		ContentType: "application/json",
		Body:        []byte(`{"message":"Internal Server Error: secret"}`),
	}

	// when
	overlay, err := GoHTTPExpectation("FAIL-PRODUCT-DECODE-TYPE-001", unsafeJava)
	_, unregisteredErr := GoHTTPExpectation("UNREGISTERED-500", unsafeJava)

	// then
	if err != nil || string(overlay.Body) != genericServerErrorBody ||
		overlay.ContentType != "application/json" {
		t.Fatalf("approved overlay = %+v, error = %v", overlay, err)
	}
	if unregisteredErr == nil {
		t.Fatal("unregistered Java 500 observation was silently overlaid")
	}
}

func TestGRPCExpectations_PreserveTypedSemantics(t *testing.T) {
	// given
	body := []byte(
		`{"code":"EDGE","covers":[{"code":"C","sumInsured":12.3400},{"code":"N"}],"questions":[{"type":"choice","code":"C","choices":[{"code":"B","label":"Second"},{"code":"A","label":"First"}]},{"type":"date","code":"D"},{"type":"numeric","code":"N"}],"maxNumberOfInsured":7}`,
	)

	// when
	products, err := ParseGRPCExpectations(body)

	// then
	if err != nil || len(products) != 1 {
		t.Fatalf("typed expectations: products=%+v error=%v", products, err)
	}
	product := products[0]
	if product.Covers[0].SumInsured == nil || *product.Covers[0].SumInsured != "12.3400" ||
		product.Covers[1].SumInsured != nil || product.MaxNumberOfInsured != 7 {
		t.Fatalf("decimal presence/scale changed: %+v", product.Covers)
	}
	if product.Questions[0].Kind != "choice" || product.Questions[1].Kind != "date" ||
		product.Questions[2].Kind != "numeric" || product.Questions[0].Choices[0].Code != "B" ||
		product.Questions[0].Choices[1].Code != "A" {
		t.Fatalf("question variants/order changed: %+v", product.Questions)
	}
}

func TestGRPCExpectations_RejectUnknownQuestionVariant(t *testing.T) {
	// when
	_, err := ParseGRPCExpectations(
		[]byte(`{"code":"EDGE","questions":[{"type":"future","code":"Q"}]}`),
	)

	// then
	if err == nil || !strings.Contains(err.Error(), `invalid question type "future"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestParityTools_ConsumeEveryAcceptedDirectProductBody(t *testing.T) {
	// given
	baselineRoot, err := ResolveBaselineRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	cases := readAcceptedHTTPCases(t, baselineRoot)
	serverErrors := make(map[string]struct{})

	for _, testCase := range cases {
		t.Run(testCase.ID, func(t *testing.T) {
			observation := HTTPExpectation{
				Status:      testCase.Expected.Status,
				ContentType: testCase.Expected.ContentType,
				Body:        []byte(testCase.Expected.RawBody),
			}
			expected := observation
			if observation.Status == 500 {
				serverErrors[testCase.ID] = struct{}{}
				expected, err = GoHTTPExpectation(testCase.ID, observation)
				if err != nil {
					t.Fatal(err)
				}
			}

			// when
			compareErr := CompareHTTP(expected, expected)
			var mappingErr error
			if expected.Status == 200 {
				_, mappingErr = ParseGRPCExpectations(expected.Body)
			}

			// then
			if compareErr != nil {
				t.Errorf("accepted HTTP expectation rejected: %v", compareErr)
			}
			if mappingErr != nil {
				t.Errorf(
					"accepted gRPC expectation source rejected: %v",
					mappingErr,
				)
			}
		})
	}
	if len(cases) != 37 {
		t.Errorf("direct Product cases = %d, want 37", len(cases))
	}
	if len(serverErrors) != len(goServerErrorCases) {
		t.Errorf(
			"server-error overlays exercised = %d, want %d",
			len(serverErrors),
			len(goServerErrorCases),
		)
	}
	for id := range goServerErrorCases {
		if _, ok := serverErrors[id]; !ok {
			t.Errorf("approved server-error overlay %q has no accepted direct case", id)
		}
	}
}

func readAcceptedHTTPCases(t *testing.T, baselineRoot string) []acceptedHTTPCase {
	t.Helper()
	var result []acceptedHTTPCase
	for _, relativePath := range []string{"http/local-dev.json", "http/qa.json"} {
		content, err := os.ReadFile(
			filepath.Join(baselineRoot, filepath.FromSlash(relativePath)),
		)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		var fixture struct {
			Environment  string `json:"environment"`
			Observations []struct {
				ScenarioID string `json:"scenarioId"`
				Boundary   string `json:"boundary"`
				Response   struct {
					Status  int `json:"status"`
					Headers struct {
						ContentType string `json:"contentType"`
					} `json:"headers"`
					RawBody string `json:"rawBody"`
				} `json:"response"`
			} `json:"observations"`
		}
		if err := json.Unmarshal(content, &fixture); err != nil {
			t.Fatalf("decode %s: %v", relativePath, err)
		}
		for _, observation := range fixture.Observations {
			if observation.Boundary != "direct" {
				continue
			}
			testCase := acceptedHTTPCase{
				ID: fixture.Environment + "/" + observation.ScenarioID,
			}
			testCase.Expected.Status = observation.Response.Status
			testCase.Expected.ContentType = observation.Response.Headers.ContentType
			testCase.Expected.RawBody = observation.Response.RawBody
			result = append(result, testCase)
		}
	}
	for _, relativePath := range []string{"failures/cases.json", "data-edges/cases.json"} {
		content, err := os.ReadFile(
			filepath.Join(baselineRoot, filepath.FromSlash(relativePath)),
		)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		var fixture struct {
			Cases []acceptedHTTPCase `json:"cases"`
		}
		if err := json.Unmarshal(content, &fixture); err != nil {
			t.Fatalf("decode %s: %v", relativePath, err)
		}
		for _, testCase := range fixture.Cases {
			if strings.HasPrefix(testCase.ID, "FAIL-PRODUCT-") ||
				strings.HasPrefix(testCase.ID, "DATA-EDGES-") &&
					testCase.Expected.Status != 0 {
				result = append(result, testCase)
			}
		}
	}
	return result
}

func responseLike(base HTTPExpectation, body string) HTTPExpectation {
	return HTTPExpectation{
		Status:      base.Status,
		ContentType: base.ContentType,
		Body:        []byte(body),
	}
}
