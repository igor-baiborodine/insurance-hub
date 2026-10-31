package policyhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

func TestHandler_RequestTelemetryRecordsBoundedOutcomesOnly(t *testing.T) {
	const private = "private-query-marker"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observer, err := telemetry.NewRequestObserver(
		logger, noop.NewTracerProvider(), meterProvider, propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(
		logger,
		Settings{RequestTimeout: time.Second, Observer: observer},
		func(_ context.Context, query string) ([]domain.Policy, error) {
			if query == private {
				return nil, errors.New(private)
			}
			return nil, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method string
		target string
		status int
	}{
		{http.MethodGet, "/policies?q=", http.StatusOK},
		{http.MethodGet, "/policies", http.StatusBadRequest},
		{http.MethodGet, "/policies?q=" + private, http.StatusInternalServerError},
		{http.MethodPost, "/policies?q=*", http.StatusMethodNotAllowed},
		{http.MethodGet, "/other", http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
		if recorder.Code != test.status {
			t.Fatalf(
				"%s %s status = %d, want %d",
				test.method,
				test.target,
				recorder.Code,
				test.status,
			)
		}
	}
	if strings.Contains(logs.String(), private) {
		t.Fatalf("completion logs exposed query or error detail: %s", logs.String())
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf(
			"completion logs = %d, want four selected-route requests: %s",
			len(lines),
			logs.String(),
		)
	}
	for index, want := range []string{"success", "invalid_request", "internal_error", "invalid_request"} {
		var record map[string]any
		if err := json.Unmarshal([]byte(lines[index]), &record); err != nil {
			t.Fatal(err)
		}
		if record["outcome"] != want {
			t.Errorf(
				"completion %d outcome = %v, want %s",
				index,
				record["outcome"],
				want,
			)
		}
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			switch data := instrument.Data.(type) {
			case metricdata.Sum[int64]:
				var count int64
				for _, point := range data.DataPoints {
					count += point.Value
				}
				if count != 4 {
					t.Errorf("request count = %d, want 4", count)
				}
			case metricdata.Histogram[float64]:
				var count uint64
				for _, point := range data.DataPoints {
					count += point.Count
				}
				if count != 4 {
					t.Errorf("duration count = %d, want 4", count)
				}
			}
		}
	}
}
