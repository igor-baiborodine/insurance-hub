package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSearcher_UsesAcceptedIndexFieldsAndLimitAndPreservesHitOrder(t *testing.T) {
	// given
	const configuredIndex = "policy-views"
	var captured searchRequest
	var capturedKeys map[string]json.RawMessage
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			w.Header().Set("X-Elastic-Product", "Elasticsearch")
			w.Header().Set("Content-Type", "application/json")
			if request.Method != http.MethodPost ||
				request.URL.Path != "/"+configuredIndex+"/_search" {
				t.Errorf("request = %s %s", request.Method, request.URL.Path)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			if err := json.Unmarshal(body, &captured); err != nil {
				t.Error(err)
			}
			if err := json.Unmarshal(body, &capturedKeys); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"hits":{"hits":[
			{"_source":{"number":"first","dateFrom":"2024-02-29","policyHolder":"Holder"}},
			{"_source":{"number":"second","dateFrom":null,"dateTo":"2025-06-01"}},
			{"_source":{"number":null,"policyHolder":""}}
		]}}`))
		}),
	)
	defer server.Close()
	client, err := New(Config{
		Endpoint: server.URL,
		Index:    configuredIndex,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	searcher, err := NewSearcher(client)
	if err != nil {
		t.Fatal(err)
	}

	// when
	policies, err := searcher.SearchPolicies(context.Background(), `number:"POL 229"`)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if captured.Size != 100 ||
		captured.Query.QueryString.Query != `number:"POL 229"` ||
		!slices.Equal(
			captured.Query.QueryString.Fields,
			[]string{"number", "policyHolder"},
		) {
		t.Fatalf("request = %+v", captured)
	}
	if len(capturedKeys) != 2 || capturedKeys["sort"] != nil {
		t.Fatalf("request added server-side ordering: keys=%v", capturedKeys)
	}
	if got := []string{
		policies[0].Number,
		policies[1].Number,
		policies[2].Number,
	}; !slices.Equal(
		got,
		[]string{"first", "second", ""},
	) {
		t.Fatalf("hit order = %v", got)
	}
	if policies[0].DateFrom.String() != "2024-02-29" || policies[1].DateFrom != nil ||
		policies[1].DateTo.String() != "2025-06-01" || policies[2].PolicyHolder != "" {
		t.Fatalf("decoded policies = %+v", policies)
	}
}

func TestSearcher_PreservesSuccessfulEmptyHitSet(t *testing.T) {
	// given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	searcher, err := NewSearcher(client)
	if err != nil {
		t.Fatal(err)
	}

	// when
	policies, err := searcher.SearchPolicies(context.Background(), "NO_MATCH_TOKEN")

	// then
	if err != nil || len(policies) != 0 {
		t.Fatalf("empty search = %#v, %v", policies, err)
	}
}

func TestSearcher_ClassifiesFailuresWithoutLeakingResponseDetails(t *testing.T) {
	// given
	tests := []struct {
		name string
		body string
		code int
	}{
		{"upstream status", `{"private":"marker"}`, http.StatusBadRequest},
		{"malformed response", `{`, http.StatusOK},
		{"trailing response", `{"hits":{"hits":[]}} {}`, http.StatusOK},
		{
			"invalid date",
			`{"hits":{"hits":[{"_source":{"dateFrom":"not-a-date"}}]}}`,
			http.StatusOK,
		},
		{"too many hits", responseWithHits(101), http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("X-Elastic-Product", "Elasticsearch")
					w.WriteHeader(test.code)
					_, _ = w.Write([]byte(test.body))
				}),
			)
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			searcher, err := NewSearcher(client)
			if err != nil {
				t.Fatal(err)
			}

			// when
			_, err = searcher.SearchPolicies(
				context.Background(),
				"private-query-marker",
			)

			// then
			if !errors.Is(err, ErrSearchFailed) && !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "private") ||
				strings.Contains(err.Error(), "not-a-date") {
				t.Fatalf("error disclosed response or query detail: %v", err)
			}
		})
	}
}

func TestSearcher_PropagatesCancellationAndRejectsMissingClient(t *testing.T) {
	// given
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			w.Header().Set("X-Elastic-Product", "Elasticsearch")
			<-request.Context().Done()
		}),
	)
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	searcher, err := NewSearcher(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// when
	_, searchErr := searcher.SearchPolicies(ctx, "*")
	_, constructorErr := NewSearcher(nil)

	// then
	if !errors.Is(searchErr, context.Canceled) ||
		!errors.Is(constructorErr, ErrClientRequired) {
		t.Fatalf("errors = %v / %v", searchErr, constructorErr)
	}
}

func responseWithHits(count int) string {
	var body strings.Builder
	body.WriteString(`{"hits":{"hits":[`)
	for index := range count {
		if index > 0 {
			body.WriteByte(',')
		}
		_, _ = fmt.Fprintf(&body, `{"_source":{"number":"%d"}}`, index)
	}
	body.WriteString(`]}}`)
	return body.String()
}
