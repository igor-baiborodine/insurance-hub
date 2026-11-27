//go:build integration

package postgres_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/telemetry"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

//go:embed testdata/seed-account-reads.sql
var seedAccountReadsQuery string

func TestRun_PaymentAccountReads(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	fixture.Exec(t, ctx, seedAccountReadsQuery)
	pool, err := postgres.OpenPool(ctx, writerSettings(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	businessTime := time.Date(2025, time.December, 31, 23, 0, 0, 0, time.UTC)
	reads, err := application.NewAccountReads(store, func() time.Time { return businessTime })
	if err != nil {
		t.Fatal(err)
	}
	before := businessCounts(t, ctx, fixture)

	// when
	accounts, err := reads.List(ctx)
	// then
	if err != nil {
		t.Fatal(err)
	}
	account := findAccount(t, accounts, "SYN-ACCOUNT-A")
	if account.PolicyNumber != "SYN-POLICY-B" ||
		!account.Created.Equal(time.Date(2025, time.December, 20, 10, 0, 0, 0, time.UTC)) ||
		!account.Updated.Equal(time.Date(2026, time.January, 2, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("account projection = %+v", account)
	}

	for _, test := range []struct {
		name    string
		date    time.Time
		balance string
	}{
		{"before entries", time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC), "0.00"},
		{"first debit effective date", time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), "-10.00"},
		{"credit effective date", time.Date(2026, 1, 7, 23, 0, 0, 0, time.UTC), "5.00"},
		{"second debit effective date", time.Date(2026, 6, 1, 23, 0, 0, 0, time.UTC), "-5.00"},
		{"outgoing debit effective date", time.Date(2026, 6, 2, 23, 0, 0, 0, time.UTC), "-7.50"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			businessTime = test.date

			// when
			got, err := reads.Balance(ctx, "SYN-ACCOUNT-A")

			// then
			if err != nil || got.Balance != test.balance {
				t.Fatalf("Balance() = (%+v, %v), want %s", got, err, test.balance)
			}
			if got.PolicyAccountNumber != "SYN-ACCOUNT-A" ||
				got.PolicyNumber != "SYN-POLICY-B" {
				t.Errorf(
					"identifiers = (%q, %q)",
					got.PolicyAccountNumber,
					got.PolicyNumber,
				)
			}
		})
	}
	t.Run("missing account", func(t *testing.T) {
		// when
		_, err := reads.Balance(ctx, "SYN-ACCOUNT-MISSING")

		// then
		if !errors.Is(err, application.ErrAccountNotFound) {
			t.Fatalf("missing Balance() error = %v, want account not found", err)
		}
	})

	t.Run("composed private HTTP", func(t *testing.T) {
		// given
		businessTime = time.Date(2026, time.January, 7, 23, 0, 0, 0, time.UTC)
		handler := composedAccountHandler(t, reads)

		// when
		list := httptest.NewRecorder()
		handler.ServeHTTP(
			list,
			httptest.NewRequest(http.MethodGet, "/payment/accounts", nil),
		)
		detail := httptest.NewRecorder()
		handler.ServeHTTP(
			detail,
			httptest.NewRequest(http.MethodGet, "/payment/accounts/SYN-ACCOUNT-A", nil),
		)
		missing := httptest.NewRecorder()
		handler.ServeHTTP(
			missing,
			httptest.NewRequest(
				http.MethodGet,
				"/payment/accounts/SYN-ACCOUNT-MISSING",
				nil,
			),
		)

		// then
		assertAccountListHTTP(t, list)
		assertAccountDetailHTTP(t, detail)
		assertMissingAccountHTTP(t, missing)
	})

	// when
	after := businessCounts(t, ctx, fixture)

	// then
	if after != before {
		t.Fatalf("account reads changed rows: before %q, after %q", before, after)
	}
}

func composedAccountHandler(t *testing.T, reads *application.AccountReads) http.Handler {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server, err := transport.NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		transport.Settings{
			RequestTimeout:  time.Second,
			MaxReceiveBytes: 1 << 20,
			MaxSendBytes:    8 << 20,
		},
		reads,
	)
	if err != nil {
		t.Fatal(err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		"passthrough:///payment",
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
		select {
		case <-serveResult:
		case <-time.After(time.Second):
			t.Error("composed gRPC server did not stop")
		}
	})
	meter := metricnoop.NewMeterProvider()
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		meter,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := gateway.NewHandler(
		paymentv1.NewPaymentServiceClient(connection),
		gateway.Settings{
			RequestTimeout: time.Second,
			MaxBodyBytes:   4096,
			TracerProvider: noop.NewTracerProvider(),
			MeterProvider:  meter,
			Propagator:     propagation.TraceContext{},
			Observer:       observer,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertAccountListHTTP(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	decoder := json.NewDecoder(strings.NewReader(recorder.Body.String()))
	decoder.UseNumber()
	var accounts []map[string]any
	if err := decoder.Decode(&accounts); err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account["policyAccountNumber"] != "SYN-ACCOUNT-A" {
			continue
		}
		if len(account) != 4 || account["policyNumber"] != "SYN-POLICY-B" {
			t.Fatalf("list account fields = %v", account)
		}
		if _, ok := account["created"].(json.Number); !ok {
			t.Errorf("created is not numeric: %T", account["created"])
		}
		if _, ok := account["updated"].(json.Number); !ok {
			t.Errorf("updated is not numeric: %T", account["updated"])
		}
		return
	}
	t.Fatalf("SYN-ACCOUNT-A missing from list: %v", accounts)
}

func assertAccountDetailHTTP(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(recorder.Body.String()))
	decoder.UseNumber()
	var account map[string]any
	if err := decoder.Decode(&account); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(account) != 5 ||
		account["policyAccountNumber"] != "SYN-ACCOUNT-A" ||
		account["policyNumber"] != "SYN-POLICY-B" ||
		account["balance"] != json.Number("5.00") {
		t.Fatalf("detail response = (%d, %v)", recorder.Code, account)
	}
	for _, field := range []string{"created", "updated"} {
		if _, ok := account[field].(json.Number); !ok {
			t.Errorf("%s is not numeric: %T", field, account[field])
		}
	}
}

func assertMissingAccountHTTP(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError || len(body) != 2 ||
		body["message"] != "Internal Server Error" ||
		strings.Contains(body["message"].(string), "SYN-ACCOUNT-MISSING") {
		t.Fatalf("missing response = (%d, %v)", recorder.Code, body)
	}
	if _, ok := body["_links"].(map[string]any); !ok {
		t.Fatalf("missing links = %T", body["_links"])
	}
}

func writerSettings(t *testing.T, fixture *postgresfixture.Fixture) config.Database {
	t.Helper()
	t.Setenv("PAYMENT_DATABASE_URL", fixture.WriterURL("payment"))
	t.Setenv("MINIO_ENDPOINT", "http://minio.example:9000")
	t.Setenv("MINIO_ACCESS_KEY", "access")
	t.Setenv("MINIO_SECRET_KEY", "secret")
	t.Setenv("KAFKA_BROKERS", "kafka.example:9093")
	t.Setenv("KAFKA_CA_FILE", "/etc/payment-service/kafka/ca.crt")
	settings, err := config.Load()
	if err != nil {
		t.Fatal("load disposable writer settings:", err)
	}
	return settings.Database
}

func findAccount(t *testing.T, accounts []application.Account, number string) application.Account {
	t.Helper()
	for _, account := range accounts {
		if account.PolicyAccountNumber == number {
			return account
		}
	}
	t.Fatalf("account %q not found in %+v", number, accounts)
	return application.Account{}
}

func businessCounts(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
) string {
	t.Helper()
	return fixture.Query(t, ctx, `
SELECT format('accounts=%s;entries=%s;receipts=%s',
    (SELECT count(*) FROM public.policy_account),
    (SELECT count(*) FROM public.accounting_entry),
    (SELECT count(*) FROM public.statement_receipt));`)
}
