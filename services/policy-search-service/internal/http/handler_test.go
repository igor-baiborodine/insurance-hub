package policyhttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

func TestHandler_PreservesQueryBindingAndWireShape(t *testing.T) {
	dateFrom, err := domain.ParseDate("2024-02-29")
	if err != nil {
		t.Fatal(err)
	}
	dateTo, err := domain.ParseDate("2025-06-01")
	if err != nil {
		t.Fatal(err)
	}
	queries := make(chan string, 8)
	handler := testHandler(
		t,
		time.Second,
		func(_ context.Context, query string) ([]domain.Policy, error) {
			queries <- query
			if strings.Contains(query, "SYN229") {
				return []domain.Policy{
					{
						Number:       "SYN229-POPULATED",
						DateFrom:     &dateFrom,
						DateTo:       &dateTo,
						PolicyHolder: "Synthetic Populated",
					},
					{Number: "", PolicyHolder: ""},
				}, nil
			}
			return nil, nil
		},
	)
	tests := []struct {
		name      string
		target    string
		wantCode  int
		wantBody  string
		wantQuery *string
	}{
		{
			name:     "omitted",
			target:   "/policies",
			wantCode: http.StatusBadRequest,
			wantBody: `{"message":"Required QueryValue [q] not specified","path":"/queryText",` +
				`"_links":{"self":{"href":"/policies","templated":false}}}`,
		},
		{
			name: "empty", target: "/policies?q=", wantCode: http.StatusOK,
			wantBody: `{}`, wantQuery: pointer(""),
		},
		{
			name: "whitespace", target: "/policies?q=%20%20", wantCode: http.StatusOK,
			wantBody: `{}`, wantQuery: pointer("  "),
		},
		{
			name:     "duplicate uses first",
			target:   "/policies?q=number%3A%22SYN229-POPULATED%22&q=number%3A%22NO%22",
			wantCode: http.StatusOK,
			wantBody: `{"policies":[{"number":"SYN229-POPULATED","dateFrom":"2024-02-29",` +
				`"dateTo":"2025-06-01","policyHolder":"Synthetic Populated"},{}]}`,
			wantQuery: pointer(`number:"SYN229-POPULATED"`),
		},
		{
			name:     "duplicate reversed uses first",
			target:   "/policies?q=number%3A%22NO%22&q=number%3A%22SYN229-POPULATED%22",
			wantCode: http.StatusOK, wantBody: `{}`, wantQuery: pointer(`number:"NO"`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, test.target, nil),
			)
			if recorder.Code != test.wantCode ||
				recorder.Header().Get("Content-Type") != "application/json" ||
				recorder.Body.String() != test.wantBody {
				t.Fatalf(
					"response = (%d, %q, %q), want (%d, application/json, %q)",
					recorder.Code,
					recorder.Header().Get("Content-Type"),
					recorder.Body.String(),
					test.wantCode,
					test.wantBody,
				)
			}
			if test.wantQuery != nil {
				select {
				case got := <-queries:
					if got != *test.wantQuery {
						t.Fatalf(
							"query = %q, want %q",
							got,
							*test.wantQuery,
						)
					}
				default:
					t.Fatal("search was not called")
				}
			}
		})
	}
}

func TestHandler_MapsAllSearchFailuresToSanitizedFrameworkBody(t *testing.T) {
	const private = "private-query-index-url-policy-upstream"
	for _, cause := range []error{
		errors.New(private + " malformed query"),
		errors.New(private + " missing index"),
		errors.New(private + " connection refused"),
		context.DeadlineExceeded,
		errors.New(private + " invalid upstream response"),
		errors.New(private + " unreadable document"),
	} {
		handler := testHandler(
			t,
			time.Second,
			func(context.Context, string) ([]domain.Policy, error) {
				return nil, cause
			},
		)
		recorder := httptest.NewRecorder()
		target := "/policies?q=number%3A%22SYN229%22"
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		want := `{"message":"Internal Server Error","_links":{"self":{"href":` +
			`"/policies?q=number%3A%22SYN229%22","templated":false}}}`
		if recorder.Code != http.StatusInternalServerError ||
			recorder.Body.String() != want ||
			strings.Contains(recorder.Body.String(), private) {
			t.Fatalf("response = (%d, %q)", recorder.Code, recorder.Body.String())
		}
	}
}

func TestHandler_UsesRequestDeadlineAndStopsOnCallerCancellation(t *testing.T) {
	deadlineSeen := make(chan time.Time, 1)
	handler := testHandler(
		t,
		20*time.Millisecond,
		func(ctx context.Context, _ string) ([]domain.Policy, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				return nil, errors.New("deadline missing")
			}
			deadlineSeen <- deadline
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/policies?q=*", nil))
	if recorder.Code != http.StatusInternalServerError ||
		time.Until(<-deadlineSeen) > 20*time.Millisecond {
		t.Fatalf("deadline response = (%d, %q)", recorder.Code, recorder.Body.String())
	}

	callerCtx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/policies?q=*", nil).WithContext(callerCtx),
	)
	if recorder.Body.Len() != 0 {
		t.Fatalf("caller-cancelled response body = %q", recorder.Body.String())
	}
}

func TestHandler_ExposesNoAdditionalBusinessRoute(t *testing.T) {
	handler := testHandler(
		t,
		time.Second,
		func(context.Context, string) ([]domain.Policy, error) {
			return nil, nil
		},
	)
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/other", http.StatusNotFound},
		{http.MethodPost, "/policies?q=*", http.StatusMethodNotAllowed},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.status {
			t.Errorf(
				"%s %s status = %d, want %d",
				test.method,
				test.path,
				recorder.Code,
				test.status,
			)
		}
	}
}

func testHandler(t *testing.T, timeout time.Duration, search SearchPolicies) http.Handler {
	t.Helper()
	handler, err := NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings{RequestTimeout: timeout},
		search,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func pointer(value string) *string { return &value }
