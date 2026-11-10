package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/config"
	dashboardelasticsearch "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/elasticsearch"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

func TestNewProductionProjection_ValidatesDependenciesAndConfiguration(t *testing.T) {
	// given
	observer := testDependencyObserver(t)
	tests := []struct {
		name     string
		settings config.Elasticsearch
		observer *telemetry.DependencyObserver
	}{
		{name: "missing observer"},
		{
			name: "unreadable CA",
			settings: config.Elasticsearch{
				URL:     "https://example:9200",
				Index:   dashboardelasticsearch.PolicyIndex,
				CAFile:  t.TempDir() + "/missing.crt",
				Timeout: time.Second,
			},
			observer: observer,
		},
		{
			name: "invalid client configuration",
			settings: config.Elasticsearch{
				URL: "invalid", Index: dashboardelasticsearch.PolicyIndex,
				Timeout: time.Second,
			},
			observer: observer,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			projection, err := newProductionProjection(test.settings, test.observer)

			// then
			if err == nil || projection != nil ||
				strings.Contains(err.Error(), "missing.crt") {
				t.Fatalf("newProductionProjection() = (%v, %v)", projection, err)
			}
		})
	}
}

func TestNewProductionProjection_AcceptsOrdinaryWriter(t *testing.T) {
	projection, err := newProductionProjection(
		config.Elasticsearch{
			URL:     "http://example:9200",
			Index:   dashboardelasticsearch.PolicyIndex,
			Timeout: time.Second,
		},
		testDependencyObserver(t),
	)
	if err != nil || projection == nil {
		t.Fatalf("newProductionProjection() = (%v, %v)", projection, err)
	}
	projection.CloseIdleConnections()
}

func TestProductionProjection_ProjectsChecksAndCloses(t *testing.T) {
	// given
	requests := make([]string, 0, 2)
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Elastic-Product", "Elasticsearch")
			requests = append(requests, request.Method+" "+request.URL.RequestURI())
			switch request.URL.Path {
			case "/_security/user/_has_privileges":
				_, _ = writer.Write(
					[]byte(
						`{"has_all_requested":true,"index":{"policy_stats":{"index":true}}}`,
					),
				)
			case "/policy_stats/_doc/SYN-PROJECTION":
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				if !strings.Contains(
					string(body),
					`"number":"SYN-PROJECTION"`,
				) {
					t.Errorf("projection body = %s", body)
				}
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write(
					[]byte(`{"_id":"SYN-PROJECTION","result":"created"}`),
				)
			default:
				t.Errorf("unexpected request = %s", request.URL.RequestURI())
				writer.WriteHeader(http.StatusNotFound)
			}
		}),
	)
	defer server.Close()
	projection, err := newProductionProjection(
		config.Elasticsearch{
			URL: server.URL, Index: dashboardelasticsearch.PolicyIndex,
			Timeout: time.Second,
		},
		testDependencyObserver(t),
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	checkErr := projection.Check(context.Background())
	projectErr := projection.ProjectPolicy(
		context.Background(),
		application.PolicyEvent{Number: "SYN-PROJECTION"},
	)
	projection.CloseIdleConnections()

	// then
	if checkErr != nil || projectErr != nil {
		t.Fatalf("projection errors = %v / %v", checkErr, projectErr)
	}
	want := []string{
		"POST /_security/user/_has_privileges",
		"PUT /policy_stats/_doc/SYN-PROJECTION?refresh=true",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %q, want %q", requests, want)
	}
}

func testDependencyObserver(t *testing.T) *telemetry.DependencyObserver {
	t.Helper()
	observer, err := telemetry.NewDependencyObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		metricnoop.NewMeterProvider(),
		tracenoop.NewTracerProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return observer
}
