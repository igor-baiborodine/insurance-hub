//go:build integration

package postgres_test

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

//go:embed testdata/sql/read-issuance-state.sql
var readIssuanceStateQuery string

//go:embed testdata/sql/read-offer-state.sql
var readOfferStateQuery string

//go:embed testdata/sql/insert-nullable-aggregates.sql
var insertNullableAggregatesQuery string

//go:embed testdata/sql/append-policy-version.sql
var appendPolicyVersionQuery string

//go:embed sql/schema.sql
var sqlcSchema string

var errForcedLateFailure = errors.New("forced failure after every issuance write")

func TestRun_PolicyStorageOnAdoptedData(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.LoadJavaData(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	pool, err := postgres.OpenPool(ctx, postgres.PoolSettings{
		URL: fixture.WriterURL(
			"policy",
		),
		MaxConnections: 4,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	businessDate := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

	// when
	offer, offerErr := store.Offer(ctx, postgresfixture.JavaOfferNumber)
	existing, policyErr := store.Policy(ctx, postgresfixture.JavaPolicyNumber)

	// then
	if offerErr != nil || optionalString(offer.TotalPrice) != "123.45" ||
		optionalString(offer.Status) != "NEW" ||
		!optionalMapEqual(
			offer.CoverPrices,
			map[string]string{"C1": "100.00", "C2": "23.45"},
		) ||
		!optionalMapEqual(
			offer.Answers,
			map[string]string{"NUM_OF_CLAIM": "0", "TYPE": "APARTMENT"},
		) {
		t.Fatalf("stored offer: %+v (%v)", offer, offerErr)
	}
	// Java expires an offer 30 days after creation.
	if offer.CreationDate == nil || offer.CreationDate.AddDate(0, 0, 30).Before(businessDate) {
		t.Fatal("the controlled business date does not keep the stored offer valid")
	}
	requireJavaPolicy(t, existing, policyErr)
	if _, err := store.Policy(ctx, "missing"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("missing policy: %v", err)
	}
	requireNullableAggregates(t, ctx, fixture, store)
	issuance := issuanceFrom(offer, "5f3d2a1b-7c6e-4d8f-9a0b-1c2d3e4f5a66")

	t.Run("forced rollback exposes no write", func(t *testing.T) {
		// when
		err := store.InTransaction(
			ctx,
			func(ctx context.Context, tx postgres.Transaction) error {
				if _, err := tx.Issue(ctx, issuance); err != nil {
					return err
				}
				return errForcedLateFailure
			},
		)

		// then
		if !errors.Is(err, errForcedLateFailure) {
			t.Fatalf("expected the forced failure, got %v", err)
		}
		requireIssuanceState(t, ctx, fixture, issuance.Number,
			"policies=0;versions=0;covers=0;premium=0;intents=0;allIntents=0")
		requireUnchangedPolicy(t, ctx, store, existing)
	})

	t.Run("commit exposes aggregate and one intent", func(t *testing.T) {
		// when
		policyID, err := store.Issue(ctx, issuance)

		// then
		if err != nil || policyID == 0 {
			t.Fatalf("issue: %d (%v)", policyID, err)
		}
		requireIssuanceState(t, ctx, fixture, issuance.Number,
			"policies=1;versions=1;covers=2;premium=123.45;intents=1;allIntents=1")
		issued, err := store.Policy(ctx, issuance.Number)
		if err != nil || issued.ID != policyID || len(issued.Versions) != 1 ||
			!reflect.DeepEqual(issued.Versions[0].Covers, issuance.Version.Covers) ||
			optionalString(issued.Versions[0].TotalPremium) != "123.45" {
			t.Fatalf("issued policy: %+v (%v)", issued, err)
		}
		requireUnchangedPolicy(t, ctx, store, existing)
	})

	t.Run("concurrent append does not split the aggregate snapshot", func(t *testing.T) {
		tracer := newCoverQueryTracer()
		t.Cleanup(tracer.release)
		configuration, err := pgxpool.ParseConfig(fixture.WriterURL("policy"))
		if err != nil {
			t.Fatal(err)
		}
		configuration.ConnConfig.Tracer = tracer
		concurrentPool, err := pgxpool.NewWithConfig(ctx, configuration)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(concurrentPool.Close)
		concurrentStore, err := postgres.NewStore(concurrentPool)
		if err != nil {
			t.Fatal(err)
		}
		type readResult struct {
			policy postgres.Policy
			err    error
		}
		result := make(chan readResult, 1)
		go func() {
			policy, err := concurrentStore.Policy(ctx, postgresfixture.JavaPolicyNumber)
			result <- readResult{policy: policy, err: err}
		}()
		select {
		case <-tracer.coversStarted:
		case <-ctx.Done():
			t.Fatal("policy read did not reach the cover query")
		}
		fixture.Exec(t, ctx, appendPolicyVersionQuery)
		tracer.release()
		var read readResult
		select {
		case read = <-result:
		case <-ctx.Done():
			t.Fatal("policy read did not complete after the concurrent append")
		}
		requireJavaPolicy(t, read.policy, read.err)
		current, err := store.Policy(ctx, postgresfixture.JavaPolicyNumber)
		if err != nil || len(current.Versions) != 3 ||
			optionalInt64(current.Versions[2].Number) != 3 ||
			len(current.Versions[2].Covers) != 1 ||
			optionalString(current.Versions[2].Covers[0].Code) != "CONCURRENT" {
			t.Fatalf(
				"concurrent version did not commit after the snapshot: %+v (%v)",
				current,
				err,
			)
		}
	})

	t.Run("failed child row leaves no part of the offer", func(t *testing.T) {
		// given
		offer := newTestOffer(t, "0b6c7f1e-2d4a-4c3b-9e8f-7a6b5c4d3e21")
		offer.Answers["NOTE"] = strings.Repeat("x", 256)
		before := readOfferState(t, ctx, fixture, offer.Number)

		// when
		err := store.CreateOffer(ctx, offer)

		// then
		if err == nil {
			t.Fatal("an oversized answer row was stored")
		}
		if after := readOfferState(t, ctx, fixture, offer.Number); after != before {
			t.Fatalf("failed offer changed rows from %q to %q", before, after)
		}
	})

	t.Run("committed offer reads back exactly", func(t *testing.T) {
		// given
		offer := newTestOffer(t, "9c1d2e3f-4a5b-4c6d-8e7f-0a1b2c3d4e5f")
		before := readOfferState(t, ctx, fixture, offer.Number)

		// when
		err := store.CreateOffer(ctx, offer)
		// then
		if err != nil {
			t.Fatal(err)
		}
		if delta := offerStateDelta(
			t,
			before,
			readOfferState(t, ctx, fixture, offer.Number),
		); delta !=
			"offers=1;answers=2;covers=2;numbered=1" {
			t.Fatalf("offer row delta = %s", delta)
		}
		stored, err := store.Offer(ctx, offer.Number)
		if err != nil || stored.ID == 0 || stored.Number != offer.Number ||
			optionalString(stored.ProductCode) != "TRI" ||
			optionalString(stored.Status) != "NEW" ||
			optionalDate(stored.CreationDate) != "2026-11-24" ||
			optionalDate(stored.PolicyFrom) != "2017-04-16" ||
			optionalDate(stored.PolicyTo) != "2018-04-15" ||
			optionalString(stored.TotalPrice) != "123.45" ||
			!optionalMapEqual(
				stored.CoverPrices,
				map[string]string{"C1": "100.10", "C2": "23.35"},
			) ||
			!optionalMapEqual(
				stored.Answers,
				map[string]string{"DESTINATION": "EUR", "NUM_OF_ADULTS": "1"},
			) {
			t.Fatalf("stored offer: %+v (%v)", stored, err)
		}
	})

	t.Run("sqlc schema equals the migrated shape", func(t *testing.T) {
		requireSQLCSchemaMatchesMigration(t, ctx, fixture)
	})
}

func TestRun_PolicyWriterReadiness(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	open := func(databaseURL string) (*pgxpool.Pool, error) {
		return postgres.OpenPool(ctx, postgres.PoolSettings{
			URL: databaseURL, MaxConnections: 2, ConnectTimeout: 5 * time.Second,
		})
	}
	pool, err := open(fixture.WriterURL("policy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	// when
	unmigrated := postgres.CheckReadiness(ctx, pool, time.Second)
	fixture.Call(t, ctx, "migrate", "version=1")
	migrated := postgres.CheckReadiness(ctx, pool, time.Second)

	// then
	if unmigrated == nil {
		t.Fatal("readiness passed without the required Policy tables")
	}
	if migrated != nil {
		t.Fatalf("readiness after migration: %v", migrated)
	}
	for _, test := range []struct{ name, url string }{
		{"missing CA", strings.Replace(fixture.WriterURL("policy"),
			url.QueryEscape(fixture.CA), url.QueryEscape(fixture.CA+".missing"), 1)},
		{"other database", fixture.WriterURL("pricing")},
		{"wrong endpoint", strings.Replace(fixture.WriterURL("policy"),
			fixture.Address.Port(), "1", 1)},
	} {
		t.Run("refuses "+test.name, func(t *testing.T) {
			// when
			refused, err := open(test.url)

			// then
			if err == nil {
				refused.Close()
				t.Fatal("writer pool opened against an unverified target")
			}
		})
	}
}

func requireJavaPolicy(t *testing.T, policy postgres.Policy, err error) {
	t.Helper()
	if err != nil || len(policy.Versions) != 2 {
		t.Fatalf("existing Java policy: %+v (%v)", policy, err)
	}
	first, second := policy.Versions[0], policy.Versions[1]
	if optionalInt64(first.Number) != 1 || optionalString(first.TotalPremium) != "123.45" ||
		!reflect.DeepEqual(
			first.Covers,
			[]postgres.Cover{
				{Code: stringPointer("C1"), Price: stringPointer("100.00")},
				{Code: stringPointer("C2"), Price: stringPointer("23.45")},
			},
		) ||
		optionalInt64(
			second.Number,
		) != 2 || optionalString(second.TotalPremium) != "3.71" ||
		!reflect.DeepEqual(
			second.Covers,
			[]postgres.Cover{
				{Code: stringPointer("C1"), Price: stringPointer("3.01")},
				{Code: stringPointer("C2"), Price: stringPointer("0.70")},
			},
		) ||
		optionalString(first.AccountNumber) != optionalString(second.AccountNumber) ||
		optionalDate(second.CoverTo) != "2026-11-23" ||
		optionalDate(second.VersionFrom) != "2026-11-24" {
		t.Fatalf("existing Java policy history differs from the C02 fixture: %+v", policy)
	}
}

func requireUnchangedPolicy(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	want postgres.Policy,
) {
	t.Helper()
	got, err := store.Policy(ctx, want.Number)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("existing policy history changed: %+v (%v)", got, err)
	}
}

func requireIssuanceState(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	number, want string,
) {
	t.Helper()
	var got string
	if err := fixture.Admin.QueryRow(ctx, readIssuanceStateQuery, number).
		Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("independent session observed %q, want %q", got, want)
	}
}

// issuanceFrom derives the first version from the stored offer and its quote, as issuance does.
func issuanceFrom(offer postgres.Offer, number string) postgres.Issuance {
	covers := make([]postgres.Cover, 0, len(offer.CoverPrices))
	for _, code := range slices.Sorted(maps.Keys(offer.CoverPrices)) {
		covers = append(
			covers,
			postgres.Cover{Code: stringPointer(code), Price: offer.CoverPrices[code]},
		)
	}
	versionNumber := int64(1)
	return postgres.Issuance{
		Number: number, AgentLogin: stringPointer("synthetic.agent"),
		Version: postgres.Version{
			Number: &versionNumber, ProductCode: offer.ProductCode,
			FirstName: stringPointer("Synthetic"), LastName: stringPointer("Issuer"),
			Pesel:         stringPointer("00000000000"),
			AccountNumber: stringPointer("8a7b6c5d-4e3f-4a1b-9c8d-7e6f5a4b3c22"),
			CoverFrom:     offer.PolicyFrom, CoverTo: offer.PolicyTo,
			VersionFrom: offer.PolicyFrom, VersionTo: offer.PolicyTo,
			TotalPremium: offer.TotalPrice, Covers: covers,
		},
		Intent: postgres.Intent{
			ID:      [16]byte{0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x44, 0x84},
			Topic:   "policy-registered",
			Key:     number,
			Payload: []byte(`{"policy":{"number":"` + number + `"}}`),
		},
	}
}

func requireNullableAggregates(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	store *postgres.Store,
) {
	t.Helper()
	fixture.Exec(t, ctx, insertNullableAggregatesQuery)
	nullableOffer, err := store.Offer(ctx, "nullable-offer")
	if err != nil || nullableOffer.ProductCode != nil || nullableOffer.Status != nil ||
		nullableOffer.CreationDate != nil || nullableOffer.PolicyFrom != nil ||
		nullableOffer.PolicyTo != nil || nullableOffer.TotalPrice != nil ||
		nullableOffer.Answers["nullable-answer"] != nil ||
		nullableOffer.CoverPrices["nullable-cover"] != nil {
		t.Fatalf("nullable offer values were not preserved: %+v (%v)", nullableOffer, err)
	}
	zeroOffer, err := store.Offer(ctx, "zero-offer")
	if err != nil || optionalString(zeroOffer.TotalPrice) != "0.00" {
		t.Fatalf("zero offer amount differs from NULL: %+v (%v)", zeroOffer, err)
	}
	nullablePolicy, err := store.Policy(ctx, "nullable-policy")
	if err != nil || nullablePolicy.AgentLogin != nil || len(nullablePolicy.Versions) != 1 {
		t.Fatalf("nullable policy values were not preserved: %+v (%v)", nullablePolicy, err)
	}
	version := nullablePolicy.Versions[0]
	if version.Number != nil || version.ProductCode != nil || version.FirstName != nil ||
		version.LastName != nil || version.Pesel != nil || version.AccountNumber != nil ||
		version.CoverFrom != nil || version.CoverTo != nil || version.VersionFrom != nil ||
		version.VersionTo != nil || version.TotalPremium != nil || len(version.Covers) != 1 ||
		version.Covers[0].Code != nil || version.Covers[0].Price != nil {
		t.Fatalf("nullable version values were not preserved: %+v", version)
	}
}

func stringPointer(value string) *string { return &value }

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func optionalDate(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.DateOnly)
}

func optionalMapEqual(got map[string]*string, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if optionalString(got[key]) != value {
			return false
		}
	}
	return true
}

type coverQueryTracer struct {
	coversStarted chan struct{}
	continueRead  chan struct{}
	startedOnce   sync.Once
	releaseOnce   sync.Once
}

func newCoverQueryTracer() *coverQueryTracer {
	return &coverQueryTracer{
		coversStarted: make(chan struct{}),
		continueRead:  make(chan struct{}),
	}
}

func (tracer *coverQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "FROM public.cover AS c") {
		tracer.startedOnce.Do(func() { close(tracer.coversStarted) })
		select {
		case <-tracer.continueRead:
		case <-ctx.Done():
		}
	}
	return ctx
}

func (*coverQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (tracer *coverQueryTracer) release() {
	tracer.releaseOnce.Do(func() { close(tracer.continueRead) })
}

// requireSQLCSchemaMatchesMigration applies the sqlc input to an empty database and checks it
// with the managed-shape inspection that migration status uses.
func requireSQLCSchemaMatchesMigration(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
) {
	t.Helper()
	inspection, err := os.ReadFile("../administration/queries/inspect-policy-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	fixture.Exec(t, ctx, "CREATE DATABASE sqlc_schema OWNER policy")
	address := *fixture.Address
	address.Path = "/sqlc_schema"
	connection, err := pgx.Connect(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	if _, err := connection.Exec(ctx, "SET ROLE policy;\n"+sqlcSchema); err != nil {
		t.Fatal("apply sqlc schema:", err)
	}
	var matches bool
	if err := connection.QueryRow(ctx, string(inspection), true).Scan(&matches); err != nil ||
		!matches {
		t.Fatalf("sqlc schema differs from the managed migration shape (%v)", err)
	}
}

// newTestOffer prices the parent TRI request with the independent C02 fractional quote.
func newTestOffer(t *testing.T, number string) domain.Offer {
	t.Helper()
	offer, err := domain.NewOffer(number, domain.OfferRequest{
		ProductCode:    "TRI",
		PolicyFrom:     time.Date(2017, 4, 16, 0, 0, 0, 0, time.UTC),
		PolicyTo:       time.Date(2018, 4, 15, 0, 0, 0, 0, time.UTC),
		SelectedCovers: []string{"C1", "C2"},
		Answers: []domain.Answer{
			{QuestionCode: "DESTINATION", Kind: domain.ChoiceAnswer, Value: "EUR"},
			{QuestionCode: "NUM_OF_ADULTS", Kind: domain.NumericAnswer, Value: "1"},
		},
	}, domain.Quote{
		TotalPrice:  "123.45",
		CoverPrices: map[string]string{"C1": "100.10", "C2": "23.35"},
	}, time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func readOfferState(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	number string,
) string {
	t.Helper()
	var state string
	if err := fixture.Admin.QueryRow(ctx, readOfferStateQuery, number).
		Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// offerStateDelta subtracts two readOfferState values field by field.
func offerStateDelta(t *testing.T, before, after string) string {
	t.Helper()
	beforeFields, afterFields := strings.Split(before, ";"), strings.Split(after, ";")
	deltas := make([]string, 0, len(afterFields))
	for index, field := range afterFields {
		name, afterValue, _ := strings.Cut(field, "=")
		_, beforeValue, _ := strings.Cut(beforeFields[index], "=")
		var from, to int
		if _, err := fmt.Sscan(beforeValue, &from); err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Sscan(afterValue, &to); err != nil {
			t.Fatal(err)
		}
		deltas = append(deltas, fmt.Sprintf("%s=%d", name, to-from))
	}
	return strings.Join(deltas, ";")
}
