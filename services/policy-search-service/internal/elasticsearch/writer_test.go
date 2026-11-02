package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

func TestWriter_ReplacesExactNumberKeyedDocument(t *testing.T) {
	// given
	var gotMethod string
	var gotPath string
	var gotDocument policyDocument
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			gotMethod = request.Method
			gotPath = request.URL.Path
			if err := json.NewDecoder(request.Body).Decode(&gotDocument); err != nil {
				t.Error(err)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"result":"created"}`))
		},
	))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewWriter(client)
	if err != nil {
		t.Fatal(err)
	}
	policy := domain.Policy{
		Number:       "SYN-230-001",
		DateFrom:     writerDate(t, "2026-01-01"),
		DateTo:       writerDate(t, "2026-06-30"),
		PolicyHolder: "Synthetic Holder A",
	}

	// when
	err = writer.ReplacePolicy(context.Background(), policy)

	// then
	wantFrom := "2026-01-01"
	wantTo := "2026-06-30"
	want := policyDocument{
		Number:       "SYN-230-001",
		DateFrom:     &wantFrom,
		DateTo:       &wantTo,
		PolicyHolder: "Synthetic Holder A",
	}
	if err != nil || gotMethod != http.MethodPut ||
		gotPath != "/"+PolicyIndex+"/_doc/SYN-230-001" ||
		!reflect.DeepEqual(gotDocument, want) {
		t.Fatalf(
			"replacement = %s %s %+v, %v; want %+v",
			gotMethod,
			gotPath,
			gotDocument,
			err,
			want,
		)
	}
}

func TestWriter_PreservesNullDates(t *testing.T) {
	// given
	var got policyDocument
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			_, _ = response.Write([]byte(`{"result":"created"}`))
		},
	))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
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
		domain.Policy{Number: "SYN-230-NULL", PolicyHolder: "Synthetic Holder"},
	)

	// then
	if err != nil || got.DateFrom != nil || got.DateTo != nil {
		t.Fatalf("null-date replacement = %+v, %v", got, err)
	}
}

func TestWriter_ClassifiesFailuresWithoutLeakingResponseDetails(t *testing.T) {
	// given
	const privateMarker = "private-write-marker"
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			response.WriteHeader(http.StatusForbidden)
			_, _ = response.Write([]byte(`{"private":"` + privateMarker + `"}`))
		},
	))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
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
		domain.Policy{Number: "SYN-230-DENIED"},
	)

	// then
	if !errors.Is(err, ErrWriteFailed) || strings.Contains(err.Error(), privateMarker) {
		t.Fatalf("write failure = %v", err)
	}
}

func TestWriter_PropagatesCancellationAndRejectsMissingClient(t *testing.T) {
	// given
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			close(started)
			select {
			case <-request.Context().Done():
			case <-release:
			}
		},
	))
	defer server.Close()
	defer close(release)
	client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
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
		result <- writer.ReplacePolicy(ctx, domain.Policy{Number: "SYN-230-CANCEL"})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Elasticsearch replacement did not start")
	}
	cancel()
	var writeErr error
	select {
	case writeErr = <-result:
	case <-time.After(time.Second):
		t.Fatal("Elasticsearch replacement ignored cancellation")
	}
	_, constructorErr := NewWriter(nil)

	// then
	if !errors.Is(writeErr, context.Canceled) ||
		!errors.Is(constructorErr, ErrClientRequired) {
		t.Fatalf("errors = %v / %v", writeErr, constructorErr)
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
