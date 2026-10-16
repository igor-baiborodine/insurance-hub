package producthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

func TestHandler_RequestTelemetry_RecordsRoutedOutcomesOnly(t *testing.T) {
	// given
	var logs bytes.Buffer
	reader := sdkmetric.NewManualReader()
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observer, err := telemetry.NewRequestObserver(
		logger.New(&logs, "product-service", 0),
		noop.NewTracerProvider(), metrics, propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(
		logger.New(io.Discard, "product-service", 0),
		Settings{RequestTimeout: time.Second, Observer: observer},
		func(context.Context) ([]domain.Product, error) {
			return []domain.Product{{Code: "CAR"}}, nil
		},
		func(context.Context, string) (domain.Product, error) {
			return domain.Product{}, application.ErrProductNotFound
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, path, outcome string
		status                      int
	}{
		{"list success", http.MethodGet, "/products", "success", http.StatusOK},
		{
			"missing product",
			http.MethodGet,
			"/products/MISSING",
			"not_found",
			http.StatusNotFound,
		},
		{
			"unsupported method",
			http.MethodPost,
			"/products",
			"invalid_request",
			http.StatusMethodNotAllowed,
		},
		{"unmatched path", http.MethodGet, "/other", "", http.StatusNotFound},
	}

	// when
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequest(test.method, test.path, nil),
			)
			if response.Code != test.status {
				t.Errorf("HTTP status = %d, want %d", response.Code, test.status)
			}
		})
	}

	// then
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("completion logs = %d, want 3: %s", len(lines), logs.String())
	}
	for index, want := range []struct {
		outcome string
		status  float64
	}{
		{"success", 200}, {"not_found", 404}, {"invalid_request", 405},
	} {
		var record map[string]any
		if err := json.Unmarshal([]byte(lines[index]), &record); err != nil {
			t.Fatal(err)
		}
		if record["outcome"] != want.outcome || record["http_status_code"] != want.status {
			t.Errorf("completion %d = %v, want outcome=%s status=%v",
				index, record, want.outcome, want.status)
		}
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	seenCounter, seenDuration := false, false
	for _, scope := range collected.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			switch data := instrument.Data.(type) {
			case metricdata.Sum[int64]:
				if instrument.Name != "insurance_hub.product.requests" {
					t.Errorf("unexpected counter %q", instrument.Name)
				}
				seenCounter = true
				var count int64
				for _, point := range data.DataPoints {
					count += point.Value
				}
				if count != 3 {
					t.Errorf("request count = %d, want 3", count)
				}
			case metricdata.Histogram[float64]:
				if instrument.Name != "insurance_hub.product.request.duration" {
					t.Errorf("unexpected histogram %q", instrument.Name)
				}
				seenDuration = true
				var count uint64
				for _, point := range data.DataPoints {
					count += point.Count
				}
				if count != 3 {
					t.Errorf("duration count = %d, want 3", count)
				}
			}
		}
	}
	if !seenCounter || !seenDuration {
		t.Errorf(
			"request counter seen=%t duration histogram seen=%t",
			seenCounter,
			seenDuration,
		)
	}
}

type shortTelemetryWriter struct{ *httptest.ResponseRecorder }

func (writer *shortTelemetryWriter) Write(body []byte) (int, error) {
	return writer.ResponseRecorder.Write(body[:len(body)/2])
}

func TestHandler_RequestTelemetry_ReportsShortResponseWrite(t *testing.T) {
	// given
	var logs bytes.Buffer
	log := logger.New(&logs, "product-service", 0)
	observer, err := telemetry.NewRequestObserver(
		log,
		noop.NewTracerProvider(),
		sdkmetric.NewMeterProvider(),
		propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(log, Settings{RequestTimeout: time.Second, Observer: observer},
		func(context.Context) ([]domain.Product, error) {
			return []domain.Product{{Code: "CAR"}}, nil
		},
		func(context.Context, string) (domain.Product, error) {
			return domain.Product{}, errors.New("unused")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	writer := &shortTelemetryWriter{ResponseRecorder: httptest.NewRecorder()}

	// when
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/products", nil))

	// then
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("completion log: %v; output=%s", err, logs.String())
	}
	if writer.Code != http.StatusOK || record["outcome"] != "internal_error" ||
		record["http_status_code"] != float64(200) {
		t.Fatalf("status=%d completion=%v", writer.Code, record)
	}
}
