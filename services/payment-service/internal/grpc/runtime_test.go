package grpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	transport "github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/telemetry"
)

func TestServer_FinalStatusMetricsAndSafeCompletion(t *testing.T) {
	// given
	cases := []struct {
		name, operation, outcome, status string
		code                             codes.Code
		reads                            *fakeAccountReads
		call                             func(context.Context, paymentv1.PaymentServiceClient) error
	}{
		{
			name:      "list success",
			operation: "list_accounts",
			outcome:   "success",
			status:    "OK",
			reads: &fakeAccountReads{accounts: []application.Account{
				{
					PolicyAccountNumber: "private-account",
					PolicyNumber:        "private-policy",
				},
			}},
			call: func(ctx context.Context, client paymentv1.PaymentServiceClient) error {
				_, err := client.ListAccounts(ctx, &paymentv1.ListAccountsRequest{})
				return err
			},
		},
		{
			name:      "list failure",
			operation: "list_accounts",
			outcome:   "internal_error",
			status:    "INTERNAL",
			code:      codes.Internal,
			reads:     &fakeAccountReads{err: errors.New("private-list-error")},
			call: func(ctx context.Context, client paymentv1.PaymentServiceClient) error {
				_, err := client.ListAccounts(ctx, &paymentv1.ListAccountsRequest{})
				return err
			},
		},
		{
			name:      "detail success",
			operation: "get_account_balance",
			outcome:   "success",
			status:    "OK",
			reads: &fakeAccountReads{balance: application.AccountBalance{
				Account: application.Account{
					PolicyAccountNumber: "private-account",
					PolicyNumber:        "private-policy",
				},
				Balance: "123.45",
			}},
			call: detailRuntimeCall,
		},
		{
			name:      "detail missing",
			operation: "get_account_balance",
			outcome:   "invalid_request",
			status:    "NOT_FOUND",
			code:      codes.NotFound,
			reads:     &fakeAccountReads{err: application.ErrAccountNotFound},
			call:      detailRuntimeCall,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// given
			var logs bytes.Buffer
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			observer, err := telemetry.NewRequestObserver(
				logger.New(&logs, "payment-service", 0),
				provider,
			)
			if err != nil {
				t.Fatal(err)
			}
			settings := defaultSettings()
			settings.Observer = observer
			client, stop := runtimeClient(t, settings, test.reads, &logs)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			// when
			err = test.call(ctx, client)
			stop()

			// then
			if status.Code(err) != test.code {
				t.Fatalf("code=%s want=%s: %v", status.Code(err), test.code, err)
			}
			if strings.Contains(logs.String(), "private-") ||
				strings.Contains(logs.String(), "123.45") {
				t.Fatalf("sensitive logs: %s", logs.String())
			}
			assertRuntimeCompletion(
				t,
				logs.Bytes(),
				test.operation,
				test.outcome,
				test.status,
			)
			assertRuntimeMetric(
				t,
				ctx,
				reader,
				test.operation,
				test.outcome,
				test.status,
			)
		})
	}
}

func detailRuntimeCall(ctx context.Context, client paymentv1.PaymentServiceClient) error {
	_, err := client.GetAccountBalance(
		ctx,
		&paymentv1.GetAccountBalanceRequest{AccountNumber: "private-account"},
	)
	return err
}

func assertRuntimeCompletion(
	t *testing.T,
	logs []byte,
	operation string,
	outcome string,
	statusCode string,
) {
	t.Helper()
	completions := 0
	for _, line := range bytes.Split(bytes.TrimSpace(logs), []byte("\n")) {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] != "request completed" {
			continue
		}
		completions++
		if entry["operation"] != operation || entry["transport"] != "grpc" ||
			entry["outcome"] != outcome || entry["rpc_status_code"] != statusCode {
			t.Errorf("completion=%v", entry)
		}
	}
	if completions != 1 {
		t.Fatalf("completions=%d want=1", completions)
	}
}

func assertRuntimeMetric(
	t *testing.T,
	ctx context.Context,
	reader *sdkmetric.ManualReader,
	operation string,
	outcome string,
	statusCode string,
) {
	t.Helper()
	var result metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &result); err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	for _, scope := range result.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name != "insurance_hub.payment.requests" {
				continue
			}
			for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
				count += point.Value
				attrs := map[string]string{}
				for _, attr := range point.Attributes.ToSlice() {
					attrs[string(attr.Key)] = attr.Value.AsString()
				}
				if len(attrs) != 4 ||
					attrs["insurance_hub.payment.operation"] != operation ||
					attrs["insurance_hub.payment.transport"] != "grpc" ||
					attrs["insurance_hub.payment.outcome"] != outcome ||
					attrs["rpc.response.status_code"] != statusCode {
					t.Errorf("unsafe or incorrect labels: %v", attrs)
				}
			}
		}
	}
	if count != 1 {
		t.Errorf("request count=%d want=1", count)
	}
}

func runtimeClient(
	t *testing.T,
	settings transport.Settings,
	reads transport.AccountReads,
	logs *bytes.Buffer,
) (paymentv1.PaymentServiceClient, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "payment-service", 0),
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		settings,
		reads,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = connection.Close()
		drained := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(time.Second):
			server.Stop()
			t.Fatal("server handlers did not drain")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	}
	t.Cleanup(stop)
	return paymentv1.NewPaymentServiceClient(connection), stop
}
