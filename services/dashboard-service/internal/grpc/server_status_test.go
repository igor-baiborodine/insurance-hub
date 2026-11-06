package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

func TestRPCOperation_UsesFixedMethodAllowlist(t *testing.T) {
	for method, want := range map[string]string{
		dashboardv1.DashboardService_GetTotalSales_FullMethodName:  telemetry.OperationTotalSales,
		dashboardv1.DashboardService_GetAgentSales_FullMethodName:  telemetry.OperationAgentSales,
		dashboardv1.DashboardService_GetSalesTrends_FullMethodName: telemetry.OperationSalesTrends,
		"/private.Service/AgentLogin":                              "",
	} {
		if got := rpcOperation(method); got != want {
			t.Errorf("rpcOperation(%q) = %q, want %q", method, got, want)
		}
	}
}

func TestRPCStatus_PreservesCanonicalNamesInLogsAndMetrics(t *testing.T) {
	// given
	cases := []struct {
		code codes.Code
		name string
	}{
		{codes.OK, "OK"},
		{codes.Canceled, "CANCELLED"},
		{codes.Unknown, "UNKNOWN"},
		{codes.InvalidArgument, "INVALID_ARGUMENT"},
		{codes.DeadlineExceeded, "DEADLINE_EXCEEDED"},
		{codes.NotFound, "NOT_FOUND"},
		{codes.AlreadyExists, "ALREADY_EXISTS"},
		{codes.PermissionDenied, "PERMISSION_DENIED"},
		{codes.ResourceExhausted, "RESOURCE_EXHAUSTED"},
		{codes.FailedPrecondition, "FAILED_PRECONDITION"},
		{codes.Aborted, "ABORTED"},
		{codes.OutOfRange, "OUT_OF_RANGE"},
		{codes.Unimplemented, "UNIMPLEMENTED"},
		{codes.Internal, "INTERNAL"},
		{codes.Unavailable, "UNAVAILABLE"},
		{codes.DataLoss, "DATA_LOSS"},
		{codes.Unauthenticated, "UNAUTHENTICATED"},
		{codes.Code(999), "UNKNOWN"},
	}
	for _, test := range cases {
		t.Run(test.code.String(), func(t *testing.T) {
			// given
			var output bytes.Buffer
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			observer, err := telemetry.NewRequestObserver(
				logger.New(&output, "dashboard-service", 0),
				provider,
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			request := observer.StartGRPC(ctx, telemetry.OperationTotalSales)
			// when
			request.Finish(ctx, rpcCodeName(test.code))
			// then
			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["rpc_status_code"] != test.name {
				t.Errorf(
					"logged status = %v, want %s",
					entry["rpc_status_code"],
					test.name,
				)
			}
			var result metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &result); err != nil {
				t.Fatal(err)
			}
			observations := 0
			for _, scope := range result.ScopeMetrics {
				for _, measurement := range scope.Metrics {
					switch data := measurement.Data.(type) {
					case metricdata.Sum[int64]:
						for _, point := range data.DataPoints {
							status, _ := point.Attributes.Value(
								"rpc.response.status_code",
							)
							if status.AsString() != test.name ||
								point.Value != 1 {
								t.Errorf("counter = %#v", point)
							}
							observations++
						}
					case metricdata.Histogram[float64]:
						for _, point := range data.DataPoints {
							status, _ := point.Attributes.Value(
								"rpc.response.status_code",
							)
							if status.AsString() != test.name ||
								point.Count != 1 {
								t.Errorf("duration = %#v", point)
							}
							observations++
						}
					}
				}
			}
			if observations != 2 {
				t.Errorf(
					"observations = %d, want counter and duration",
					observations,
				)
			}
		})
	}
}
