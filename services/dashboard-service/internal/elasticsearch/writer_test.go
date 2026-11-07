package elasticsearch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestWriter_ReplacesExactDocumentWithRefreshAndReceivedPremium(t *testing.T) {
	// given
	var requests [][]byte
	var paths []string
	server := httptest.NewServer(
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			requests = append(requests, body)
			paths = append(paths, request.Method+" "+request.URL.RequestURI())
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"_id":"SYN-290-001","result":"created"}`))
		}),
	)
	defer server.Close()
	client, err := New(
		Config{Endpoint: server.URL, Index: ProjectionIndex, Timeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewWriter(client)
	if err != nil {
		t.Fatal(err)
	}
	holder := "Synthetic Holder A"
	product := "HSI"
	agent := "synthetic-agent"
	premium := writerPremium(t, "9007199254740993.1234567")
	policy := domain.PolicyProjection{
		Number: "SYN-290-001", From: writerDate(t, "2026-01-01"),
		To: writerDate(t, "2026-12-31"), PolicyHolder: &holder,
		ProductCode: &product, TotalPremium: premium, AgentLogin: &agent,
	}
	replacement := policy
	replacement.AgentLogin = nil

	// when
	if err := writer.ReplacePolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	err = writer.ReplacePolicy(context.Background(), replacement)

	// then
	if err != nil || len(requests) != 2 ||
		paths[0] != "PUT /policy_stats_issue_290/_doc/SYN-290-001?refresh=true" ||
		paths[1] != paths[0] {
		t.Fatalf("replacement paths = %v, error = %v", paths, err)
	}
	wantFirst := []byte(
		`{"number":"SYN-290-001","from":"2026-01-01","to":"2026-12-31","policyHolder":"Synthetic Holder A","productCode":"HSI","totalPremium":9007199254740993.1234567,"agentLogin":"synthetic-agent"}`,
	)
	wantSecond := []byte(
		`{"number":"SYN-290-001","from":"2026-01-01","to":"2026-12-31","policyHolder":"Synthetic Holder A","productCode":"HSI","totalPremium":9007199254740993.1234567,"agentLogin":null}`,
	)
	if !bytes.Equal(requests[0], wantFirst) || !bytes.Equal(requests[1], wantSecond) {
		t.Fatalf("replacement bodies = %s / %s", requests[0], requests[1])
	}
}

func TestWriter_EmitsAllSevenFieldsForNullAndEmptyValues(t *testing.T) {
	// given
	var got []byte
	server := httptest.NewServer(
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			got, _ = io.ReadAll(request.Body)
			_, _ = response.Write([]byte(`{"_id":"123","result":"created"}`))
		}),
	)
	defer server.Close()
	client, err := New(
		Config{Endpoint: server.URL, Index: ProjectionIndex, Timeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewWriter(client)
	if err != nil {
		t.Fatal(err)
	}
	empty := ""

	// when
	err = writer.ReplacePolicy(context.Background(), domain.PolicyProjection{
		Number: "123", ProductCode: &empty, AgentLogin: &empty,
	})

	// then
	want := []byte(
		`{"number":"123","from":null,"to":null,"policyHolder":null,"productCode":"","totalPremium":null,"agentLogin":""}`,
	)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("null/empty document = %s, %v; want %s", got, err, want)
	}
}

func TestWriter_ClassifiesRejectedAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "rejected", status: http.StatusForbidden, body: `{"private":"marker"}`},
		{name: "incomplete success", status: http.StatusOK, body: `{"result":"created"}`},
		{
			name:   "oversized",
			status: http.StatusOK,
			body:   strings.Repeat("x", maximumWriterResponseBytes+1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			server := httptest.NewServer(
				http.HandlerFunc(
					func(response http.ResponseWriter, _ *http.Request) {
						response.Header().
							Set("X-Elastic-Product", "Elasticsearch")
						response.WriteHeader(test.status)
						_, _ = response.Write([]byte(test.body))
					},
				),
			)
			defer server.Close()
			client, err := New(
				Config{
					Endpoint: server.URL,
					Index:    ProjectionIndex,
					Timeout:  time.Second,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := NewWriter(client)
			if err != nil {
				t.Fatal(err)
			}

			// when
			err = writer.ReplacePolicy(
				context.Background(),
				domain.PolicyProjection{Number: "SYN-290-FAIL"},
			)

			// then
			if !errors.Is(err, ErrWriteFailed) ||
				strings.Contains(err.Error(), "marker") {
				t.Fatalf("write failure = %v", err)
			}
		})
	}
}

func TestWriter_PropagatesCancellationAndRequiresIsolatedClient(t *testing.T) {
	// given
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			close(started)
			select {
			case <-request.Context().Done():
			case <-release:
			}
		}),
	)
	defer server.Close()
	defer close(release)
	client, err := New(
		Config{Endpoint: server.URL, Index: ProjectionIndex, Timeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewWriter(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	// when
	go func() {
		result <- writer.ReplacePolicy(ctx, domain.PolicyProjection{Number: "SYN-290-CANCEL"})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Elasticsearch replacement did not start")
	}
	cancel()
	writeErr := <-result
	canonicalClient, err := New(
		Config{Endpoint: server.URL, Index: PolicyIndex, Timeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, canonicalErr := NewWriter(canonicalClient)
	_, missingErr := NewWriter(nil)

	// then
	if !errors.Is(writeErr, context.Canceled) || canonicalErr == nil ||
		!errors.Is(missingErr, ErrClientRequired) {
		t.Fatalf("errors = %v / %v / %v", writeErr, canonicalErr, missingErr)
	}
}

func writerDate(t *testing.T, value string) *domain.Date {
	t.Helper()
	date, err := domain.ParseDate(value)
	if err != nil {
		t.Fatal(err)
	}
	return &date
}

func writerPremium(t *testing.T, value string) *domain.Premium {
	t.Helper()
	premium, err := domain.ParsePremium(value)
	if err != nil {
		t.Fatal(err)
	}
	return &premium
}
