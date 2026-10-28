package productgrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

type passthroughStatsHandler struct{ stats.Handler }

func (passthroughStatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

func (passthroughStatsHandler) HandleRPC(context.Context, stats.RPCStats) {}

func TestRequestStats_FinalRPCStatusControlsCompletion(t *testing.T) {
	// given
	var logs bytes.Buffer
	reader := sdkmetric.NewManualReader()
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = metrics.Shutdown(context.Background()) })
	observer, err := telemetry.NewRequestObserver(
		logger.New(&logs, "product-service", 0),
		noop.NewTracerProvider(), metrics, propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	statsHandler := &requestStatsHandler{Handler: passthroughStatsHandler{}, observer: observer}
	cases := []struct {
		name, method, outcome, status string
		endError                      error
	}{
		{
			name:    "successful list",
			method:  productv1.ProductService_ListProducts_FullMethodName,
			outcome: "success",
			status:  "OK",
		},
		{
			name:    "oversized list response",
			method:  productv1.ProductService_ListProducts_FullMethodName,
			outcome: "invalid_request",
			status:  "RESOURCE_EXHAUSTED",
			endError: status.Error(
				codes.ResourceExhausted,
				"response exceeds send limit",
			),
		},
		{
			name:     "missing product",
			method:   productv1.ProductService_GetProduct_FullMethodName,
			outcome:  "not_found",
			status:   "NOT_FOUND",
			endError: status.Error(codes.NotFound, "product not found"),
		},
		{
			name:     "canceled read",
			method:   productv1.ProductService_GetProduct_FullMethodName,
			outcome:  "cancelled",
			status:   "CANCELLED",
			endError: status.Error(codes.Canceled, "request canceled"),
		},
		{
			name:     "timed out read",
			method:   productv1.ProductService_GetProduct_FullMethodName,
			outcome:  "deadline_exceeded",
			status:   "DEADLINE_EXCEEDED",
			endError: status.Error(codes.DeadlineExceeded, "request deadline exceeded"),
		},
		{
			name:     "unavailable storage",
			method:   productv1.ProductService_GetProduct_FullMethodName,
			outcome:  "storage_unavailable",
			status:   "UNAVAILABLE",
			endError: status.Error(codes.Unavailable, "storage unavailable"),
		},
		{
			name:     "internal failure",
			method:   productv1.ProductService_GetProduct_FullMethodName,
			outcome:  "internal_error",
			status:   "INTERNAL",
			endError: status.Error(codes.Internal, "internal failure"),
		},
		{
			name: "unregistered method", method: "/product.v1.ProductService/Unknown",
		},
	}

	// when
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := statsHandler.TagRPC(context.Background(), &stats.RPCTagInfo{
				FullMethodName: test.method,
			})
			statsHandler.HandleRPC(ctx, &stats.End{Error: test.endError})
		})
	}

	// then
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) != len(cases)-1 {
		t.Fatalf(
			"completion logs = %d, want %d: %s",
			len(records),
			len(cases)-1,
			logs.String(),
		)
	}
	for index, want := range cases[:len(cases)-1] {
		if records[index]["outcome"] != want.outcome ||
			records[index]["rpc_status_code"] != want.status {
			t.Errorf("completion %d = %v, want outcome=%s status=%s",
				index, records[index], want.outcome, want.status)
		}
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	var requests int64
	for _, scope := range collected.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if instrument.Name != "insurance_hub.product.requests" {
				continue
			}
			for _, point := range instrument.Data.(metricdata.Sum[int64]).DataPoints {
				requests += point.Value
			}
		}
	}
	if requests != int64(len(cases)-1) {
		t.Errorf("request count = %d, want %d", requests, len(cases)-1)
	}
}

func TestRequestStats_WithoutObserver(t *testing.T) {
	// given
	handler := &requestStatsHandler{Handler: passthroughStatsHandler{}}
	// when
	ctx := handler.TagRPC(
		context.Background(),
		&stats.RPCTagInfo{
			FullMethodName: productv1.ProductService_ListProducts_FullMethodName,
		},
	)
	handler.HandleRPC(ctx, &stats.End{})
	// then
	if ctx.Value(requestStateKey{}) != nil {
		t.Fatal("observation created without observer")
	}
}
