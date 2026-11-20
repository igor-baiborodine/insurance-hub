//go:build integration

package administration_test

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/config"
	pricingpostgres "github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/service"
)

func verifyReaderStartup(
	t *testing.T, ctx context.Context, admin *pgx.Conn, address *url.URL, ca, password string,
) {
	t.Helper()
	t.Run("reader access and write denials", func(t *testing.T) {
		// given
		connectionURL := *address
		connectionURL.User = url.UserPassword("go_pricing_reader", password)
		connectionURL.RawQuery = url.Values{
			"sslmode":     {"verify-full"},
			"sslrootcert": {ca},
		}.Encode()
		reader, err := pgx.Connect(ctx, connectionURL.String())
		if err != nil {
			t.Fatal("could not authenticate as the disposable Pricing reader")
		}
		t.Cleanup(func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = reader.Close(closeCtx)
		})
		before := seedSnapshot(t, ctx, admin)

		// when
		got := queryString(t, ctx, reader, readTariffCodesQuery)

		// then
		if got != "CAR,FAI,HSI,TRI" {
			t.Fatalf("reader tariff codes = %q", got)
		}
		for _, statement := range []string{denyTariffUpdateQuery, denyTariffAlterQuery} {
			// given
			tx, err := reader.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// when
			_, execErr := tx.Exec(ctx, statement)
			_ = tx.Rollback(ctx)
			// then
			var pgErr *pgconn.PgError
			if !errors.As(execErr, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("want insufficient privilege (42501), got %v", execErr)
			}
		}

		// given
		t.Setenv("PRICING_DATABASE_URL", connectionURL.String())
		t.Setenv("HTTP_ADDR", "127.0.0.1:0")
		t.Setenv("GRPC_ADDR", "127.0.0.1:0")
		t.Setenv("HEALTH_ADDR", "127.0.0.1:0")
		settings, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		started := make(startupLog, 1)
		done := make(chan error, 1)

		// when
		go func() {
			done <- service.Run(runCtx, settings, slog.New(slog.NewTextHandler(started, nil)))
		}()

		// then
		select {
		case <-started:
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("Pricing did not stop")
			}
		case err := <-done:
			t.Fatalf("Pricing stopped before serving: %v", err)
		case <-runCtx.Done():
			t.Fatal("Pricing did not start")
		}
		if seedSnapshot(t, ctx, admin) != before {
			t.Fatal("Pricing startup changed migration history or tariff data")
		}

		verifyTariffReader(t, ctx, admin, address, settings)
		if seedSnapshot(t, ctx, admin) != before {
			t.Fatal("tariff reader scenarios changed reviewed tariff data")
		}
	})
}

func verifyTariffReader(
	t *testing.T,
	ctx context.Context,
	admin *pgx.Conn,
	address *url.URL,
	settings config.Config,
) {
	t.Helper()
	execSQL(t, ctx, admin, setupReaderScenariosQuery)
	defer execSQL(t, ctx, admin, deleteReaderScenariosQuery)

	pool, err := pricingpostgres.OpenPool(ctx, settings.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reader, err := pricingpostgres.NewReader(
		pool,
		settings.Database.AcquireTimeout,
		settings.Database.QueryTimeout,
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("production tariff reader preserves one ordered snapshot", func(t *testing.T) {
		// when
		seeded, found, readErr := reader.GetTariff(ctx, "FAI")
		ordered, orderedFound, orderedErr := reader.GetTariff(ctx, "ORDERED")

		// then
		if readErr != nil || !found || len(seeded.BaseRules) != 4 ||
			seeded.BaseRules[0].Condition != nil || len(seeded.MarkupRules) != 2 ||
			seeded.MarkupRules[0].Factor.String() != "1.50" {
			t.Fatalf("seeded tariff=%+v found=%t error=%v", seeded, found, readErr)
		}
		if orderedErr != nil || !orderedFound || len(ordered.BaseRules) != 2 ||
			ordered.BaseRules[0].CoverCode != "C1" ||
			ordered.BaseRules[1].CoverCode != "C2" ||
			ordered.MarkupRules[0].Factor.String() != "1.50" ||
			ordered.MarkupRules[1].Factor.String() != "1.25" {
			t.Fatalf(
				"ordered tariff=%+v found=%t error=%v",
				ordered,
				orderedFound,
				orderedErr,
			)
		}
		assertNoReaderConnections(t, pool)
	})

	t.Run(
		"production tariff reader rejects unknown ambiguous and unusable tariffs",
		func(t *testing.T) {
			// when
			missing, found, missingErr := reader.GetTariff(ctx, "UNKNOWN")
			ambiguous, ambiguousFound, ambiguousErr := reader.GetTariff(
				ctx,
				"AMBIGUOUS",
			)
			incomplete, incompleteFound, incompleteErr := reader.GetTariff(
				ctx,
				"INCOMPLETE",
			)
			unusable, unusableFound, unusableErr := reader.GetTariff(ctx, "UNUSABLE")

			// then
			if missing.Code != "" || found || missingErr != nil {
				t.Errorf(
					"missing tariff=%+v found=%t error=%v",
					missing,
					found,
					missingErr,
				)
			}
			if ambiguous.Code != "" || ambiguousFound ||
				!errors.Is(ambiguousErr, application.ErrTariffAmbiguous) {
				t.Errorf(
					"ambiguous tariff=%+v found=%t error=%v",
					ambiguous,
					ambiguousFound,
					ambiguousErr,
				)
			}
			if incomplete.Code != "" || incompleteFound ||
				!errors.Is(incompleteErr, application.ErrTariffInvalid) {
				t.Errorf(
					"incomplete tariff=%+v found=%t error=%v",
					incomplete,
					incompleteFound,
					incompleteErr,
				)
			}
			if unusable.Code != "" || unusableFound ||
				!errors.Is(unusableErr, application.ErrTariffInvalid) {
				t.Errorf(
					"unusable tariff=%+v found=%t error=%v",
					unusable,
					unusableFound,
					unusableErr,
				)
			}
			assertNoReaderConnections(t, pool)
		},
	)

	t.Run("observed tariff reader separates decoded and invalid tariffs", func(t *testing.T) {
		// given
		metricReader := sdkmetric.NewManualReader()
		meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
		defer func() { _ = meterProvider.Shutdown(context.Background()) }()
		observer, observerErr := pricingpostgres.NewObserver(
			pool,
			tracenoop.NewTracerProvider(),
			meterProvider,
		)
		if observerErr != nil {
			t.Fatal(observerErr)
		}
		defer func() { _ = observer.Close() }()
		observed, readerErr := pricingpostgres.NewReader(
			pool,
			settings.Database.AcquireTimeout,
			settings.Database.QueryTimeout,
			observer,
		)
		if readerErr != nil {
			t.Fatal(readerErr)
		}

		// when
		_, found, readErr := observed.GetTariff(ctx, "FAI")
		_, _, unusableErr := observed.GetTariff(ctx, "UNUSABLE")
		var collected metricdata.ResourceMetrics
		collectErr := metricReader.Collect(context.Background(), &collected)

		// then
		if readErr != nil || !found ||
			!errors.Is(unusableErr, application.ErrTariffInvalid) {
			t.Fatalf("observed reads = (%t, %v), %v", found, readErr, unusableErr)
		}
		if collectErr != nil {
			t.Fatal(collectErr)
		}
		got := map[string]int64{}
		for _, scope := range collected.ScopeMetrics {
			for _, measurement := range scope.Metrics {
				if measurement.Name != "insurance_hub.pricing.database.operations" {
					continue
				}
				for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
					stage, _ := point.Attributes.Value(
						"insurance_hub.pricing.database.stage",
					)
					outcome, _ := point.Attributes.Value(
						"insurance_hub.pricing.database.outcome",
					)
					got[stage.AsString()+"/"+outcome.AsString()] += point.Value
				}
			}
		}
		want := map[string]int64{
			"acquire/success":     2,
			"query/success":       2,
			"decode/success":      1,
			"decode/invalid_data": 1,
		}
		if len(got) != len(want) {
			t.Errorf("database operations = %v, want %v", got, want)
		}
		for key, count := range want {
			if got[key] != count {
				t.Errorf(
					"database operation %s = %d, want %d",
					key,
					got[key],
					count,
				)
			}
		}
		assertNoReaderConnections(t, pool)
	})

	t.Run("production tariff reader never mixes a committed rule pair", func(t *testing.T) {
		// given
		scenarioCtx, cancelScenario := context.WithCancel(ctx)
		defer cancelScenario()
		writer, connectErr := pgx.Connect(scenarioCtx, address.String())
		if connectErr != nil {
			t.Fatal(connectErr)
		}
		defer func() {
			closeCtx, cancelClose := context.WithTimeout(
				context.Background(),
				time.Second,
			)
			defer cancelClose()
			_ = writer.Close(closeCtx)
		}()
		writerDone := make(chan error, 1)
		start := make(chan struct{})
		go func() {
			<-start
			for index := range 40 {
				formula, factor := "10.00", "2.00"
				if index%2 == 0 {
					formula, factor = "20.00", "3.00"
				}
				tx, beginErr := writer.Begin(scenarioCtx)
				if beginErr != nil {
					writerDone <- beginErr
					return
				}
				if _, updateErr := tx.Exec(
					scenarioCtx,
					"UPDATE public.base_price_rules SET price_formula = $1 WHERE tariff_id = 9006",
					formula,
				); updateErr != nil {
					_ = tx.Rollback(scenarioCtx)
					writerDone <- updateErr
					return
				}
				runtime.Gosched()
				if _, updateErr := tx.Exec(
					scenarioCtx,
					"UPDATE public.discount_markup_rule SET param_value = $1 WHERE tariff_id = 9006",
					factor,
				); updateErr != nil {
					_ = tx.Rollback(scenarioCtx)
					writerDone <- updateErr
					return
				}
				if commitErr := tx.Commit(scenarioCtx); commitErr != nil {
					writerDone <- commitErr
					return
				}
			}
			writerDone <- nil
		}()
		close(start)

		// when
		reads := 0
		for {
			tariff, found, readErr := reader.GetTariff(ctx, "PAIR")
			if readErr != nil || !found || len(tariff.BaseRules) != 1 ||
				len(tariff.MarkupRules) != 1 {
				t.Fatalf(
					"paired tariff=%+v found=%t error=%v",
					tariff,
					found,
					readErr,
				)
			}
			formula := tariff.BaseRules[0].Formula
			factor := tariff.MarkupRules[0].Factor.String()
			if !((formula == "10.00" && factor == "2.00") ||
				(formula == "20.00" && factor == "3.00")) {
				t.Fatalf("mixed tariff pair formula=%q factor=%q", formula, factor)
			}
			reads++
			select {
			case writerErr := <-writerDone:
				if writerErr != nil {
					t.Fatal(writerErr)
				}
				if reads == 0 {
					t.Fatal("coherent snapshot scenario performed no reads")
				}
				assertNoReaderConnections(t, pool)
				return
			default:
			}
		}
	})

	t.Run(
		"production tariff reader releases resources on cancellation",
		func(t *testing.T) {
			// given
			canceledCtx, cancel := context.WithCancel(ctx)
			cancel()

			// when
			canceledTariff, canceledFound, canceledErr := reader.GetTariff(
				canceledCtx,
				"FAI",
			)

			// then
			if canceledTariff.Code != "" || canceledFound ||
				!errors.Is(canceledErr, context.Canceled) ||
				errors.Is(canceledErr, application.ErrReaderUnavailable) {
				t.Fatalf(
					"canceled tariff=%+v found=%t error=%v",
					canceledTariff,
					canceledFound,
					canceledErr,
				)
			}
			assertNoReaderConnections(t, pool)
		},
	)

	t.Run("production tariff reader classifies unavailable storage", func(t *testing.T) {
		// given
		pool.Close()

		// when
		tariff, found, readErr := reader.GetTariff(ctx, "FAI")

		// then
		if tariff.Code != "" || found ||
			!errors.Is(readErr, application.ErrReaderUnavailable) {
			t.Fatalf("unavailable tariff=%+v found=%t error=%v", tariff, found, readErr)
		}
		assertNoReaderConnections(t, pool)
	})
}

func assertNoReaderConnections(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("reader retained %d acquired connections", acquired)
	}
}

type startupLog chan struct{}

func (signal startupLog) Write(value []byte) (int, error) {
	if strings.Contains(string(value), "service started") {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	return len(value), nil
}
