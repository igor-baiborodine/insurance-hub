package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	transport "github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/grpc"
)

func TestPaymentService_GeneratedIdentity(t *testing.T) {
	// given
	const (
		wantFile       = "payment/v1/payment_service.proto"
		wantPackage    = "payment.v1"
		wantModule     = "github.com/igor-baiborodine/insurance-hub/services/payment-service"
		wantService    = "payment.v1.PaymentService"
		wantFullMethod = "/payment.v1.PaymentService/GetAccountBalance"
	)
	wantGoPackage := wantModule + "/gen/payment/v1;paymentv1"

	// when
	descriptor := (&paymentv1.GetAccountBalanceRequest{}).ProtoReflect().
		Descriptor().
		ParentFile()

	// then
	if descriptor.Path() != wantFile || string(descriptor.Package()) != wantPackage {
		t.Errorf(
			"descriptor identity = (%q, %q), want (%q, %q)",
			descriptor.Path(),
			descriptor.Package(),
			wantFile,
			wantPackage,
		)
	}
	options, ok := descriptor.Options().(*descriptorpb.FileOptions)
	if !ok || options.GetGoPackage() != wantGoPackage {
		t.Fatalf(
			"go_package options = %T %v, want %q",
			descriptor.Options(),
			options,
			wantGoPackage,
		)
	}
	service := descriptor.Services().ByName("PaymentService")
	if service == nil || string(service.FullName()) != wantService {
		t.Errorf("service descriptor = %v, want %q", service, wantService)
	}
	if paymentv1.PaymentService_GetAccountBalance_FullMethodName != wantFullMethod {
		t.Errorf(
			"full method = %q, want %q",
			paymentv1.PaymentService_GetAccountBalance_FullMethodName,
			wantFullMethod,
		)
	}
}

func TestPaymentService_MapsAccountReads(t *testing.T) {
	// given
	created := time.Date(2024, time.January, 2, 3, 4, 5, 6000000, time.UTC)
	updated := created.Add(24 * time.Hour)
	reads := &fakeAccountReads{
		accounts: []application.Account{{
			PolicyAccountNumber: "payment-1",
			PolicyNumber:        "policy-1",
			Created:             created,
			Updated:             updated,
		}},
		balance: application.AccountBalance{
			Account: application.Account{
				PolicyAccountNumber: "payment-1",
				PolicyNumber:        "policy-1",
				Created:             created,
				Updated:             updated,
			},
			Balance: "12.3400",
		},
	}
	client := newPaymentClient(t, reads, new(bytes.Buffer))

	// when
	list, err := client.ListAccounts(context.Background(), &paymentv1.ListAccountsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := client.GetAccountBalance(
		context.Background(),
		&paymentv1.GetAccountBalanceRequest{AccountNumber: "payment-1"},
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetAccounts()) != 1 {
		t.Fatalf("accounts = %v, want one", list.GetAccounts())
	}
	account := list.GetAccounts()[0]
	if account.GetPolicyAccountNumber() != "payment-1" ||
		account.GetPolicyNumber() != "policy-1" ||
		account.GetCreated() != created.UnixMilli() ||
		account.GetUpdated() != updated.UnixMilli() {
		t.Errorf("list account = %v", account)
	}
	balanced := detail.GetAccount()
	if reads.accountNumber != "payment-1" ||
		balanced.GetPolicyAccountNumber() != "payment-1" ||
		balanced.GetPolicyNumber() != "policy-1" ||
		balanced.GetBalance() != "12.3400" ||
		balanced.GetCreated() != created.UnixMilli() ||
		balanced.GetUpdated() != updated.UnixMilli() {
		t.Errorf("detail account = %v, requested = %q", balanced, reads.accountNumber)
	}
}

func TestPaymentService_MapsFailuresWithoutSensitiveValues(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "missing", err: application.ErrAccountNotFound, code: codes.NotFound},
		{
			name: "storage unavailable",
			err:  application.ErrStorageUnavailable,
			code: codes.Unavailable,
		},
		{name: "canceled", err: context.Canceled, code: codes.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
		{
			name: "unexpected",
			err:  errors.New("private-adapter-marker"),
			code: codes.Internal,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			const accountMarker = "private-account-marker"
			var logs bytes.Buffer
			client := newPaymentClient(t, &fakeAccountReads{err: test.err}, &logs)

			// when
			response, err := client.GetAccountBalance(
				context.Background(),
				&paymentv1.GetAccountBalanceRequest{AccountNumber: accountMarker},
			)

			// then
			if response != nil || status.Code(err) != test.code {
				t.Fatalf(
					"GetAccountBalance() = (%v, %v), want (nil, %v)",
					response,
					err,
					test.code,
				)
			}
			if strings.Contains(logs.String(), accountMarker) ||
				strings.Contains(logs.String(), "private-adapter-marker") {
				t.Fatalf("logs expose private values: %s", logs.String())
			}
		})
	}
}

func TestPaymentService_RejectsInvalidAccountNumberBeforeApplication(t *testing.T) {
	tests := []string{"", strings.Repeat("a", 256)}
	for _, accountNumber := range tests {
		t.Run(strings.Repeat("x", min(len(accountNumber)+1, 20)), func(t *testing.T) {
			// given
			reads := &fakeAccountReads{}
			client := newPaymentClient(t, reads, new(bytes.Buffer))

			// when
			response, err := client.GetAccountBalance(
				context.Background(),
				&paymentv1.GetAccountBalanceRequest{AccountNumber: accountNumber},
			)

			// then
			if response != nil || status.Code(err) != codes.InvalidArgument {
				t.Fatalf(
					"GetAccountBalance() = (%v, %v), want invalid argument",
					response,
					err,
				)
			}
			if reads.balanceCalls != 0 {
				t.Errorf("application calls = %d, want zero", reads.balanceCalls)
			}
		})
	}
}

func TestNewServer_RejectsMissingDependencies(t *testing.T) {
	// given
	log := logger.New(new(bytes.Buffer), "payment-service", 0)
	tracerProvider := noop.NewTracerProvider()
	propagator := propagation.TraceContext{}
	tests := []struct {
		name              string
		loggerMissing     bool
		tracerMissing     bool
		propagatorMissing bool
		readsMissing      bool
	}{
		{name: "logger", loggerMissing: true},
		{name: "tracer provider", tracerMissing: true},
		{name: "propagator", propagatorMissing: true},
		{name: "account reads", readsMissing: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			selectedLogger := log
			selectedTracer := trace.TracerProvider(tracerProvider)
			selectedPropagator := propagation.TextMapPropagator(propagator)
			selectedReads := transport.AccountReads(&fakeAccountReads{})
			if test.loggerMissing {
				selectedLogger = nil
			}
			if test.tracerMissing {
				selectedTracer = nil
			}
			if test.propagatorMissing {
				selectedPropagator = nil
			}
			if test.readsMissing {
				selectedReads = nil
			}

			// when
			server, err := transport.NewServer(
				selectedLogger,
				selectedTracer,
				selectedPropagator,
				defaultSettings(),
				selectedReads,
			)

			// then
			if err == nil || server != nil {
				t.Fatalf("NewServer() = (%v, %v), want (nil, error)", server, err)
			}
		})
	}
}

func newPaymentClient(
	t *testing.T,
	reads transport.AccountReads,
	logs *bytes.Buffer,
) paymentv1.PaymentServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "payment-service", 0),
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		defaultSettings(),
		reads,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("new server: %v", err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, grpcgo.ErrServerStopped) {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return paymentv1.NewPaymentServiceClient(connection)
}

type fakeAccountReads struct {
	accounts      []application.Account
	balance       application.AccountBalance
	err           error
	accountNumber string
	balanceCalls  int
}

func (reads *fakeAccountReads) List(context.Context) ([]application.Account, error) {
	return reads.accounts, reads.err
}

func (reads *fakeAccountReads) Balance(
	_ context.Context,
	accountNumber string,
) (application.AccountBalance, error) {
	reads.accountNumber = accountNumber
	reads.balanceCalls++
	return reads.balance, reads.err
}

func defaultSettings() transport.Settings {
	return transport.Settings{
		RequestTimeout:  time.Second,
		MaxReceiveBytes: 1048576,
		MaxSendBytes:    8388608,
	}
}
