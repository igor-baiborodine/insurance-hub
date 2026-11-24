//go:build integration

package postgres_test

import (
	"context"
	_ "embed"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

//go:embed testdata/sql/read-issuance-state.sql
var readIssuanceStateQuery string

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
		URL: fixture.RuntimeURL(
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
	if offerErr != nil || offer.TotalPrice != "123.45" || offer.Status != "NEW" ||
		!maps.Equal(offer.CoverPrices, map[string]string{"C1": "100.00", "C2": "23.45"}) ||
		!maps.Equal(
			offer.Answers,
			map[string]string{"NUM_OF_CLAIM": "0", "TYPE": "APARTMENT"},
		) {
		t.Fatalf("stored offer: %+v (%v)", offer, offerErr)
	}
	// Java expires an offer 30 days after creation.
	if offer.CreationDate.AddDate(0, 0, 30).Before(businessDate) {
		t.Fatal("the controlled business date does not keep the stored offer valid")
	}
	requireJavaPolicy(t, existing, policyErr)
	if _, err := store.Policy(ctx, "missing"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("missing policy: %v", err)
	}
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
			issued.Versions[0].TotalPremium != "123.45" {
			t.Fatalf("issued policy: %+v (%v)", issued, err)
		}
		requireUnchangedPolicy(t, ctx, store, existing)
	})

	t.Run("sqlc schema equals the migrated shape", func(t *testing.T) {
		requireSQLCSchemaMatchesMigration(t, ctx, fixture)
	})
}

func requireJavaPolicy(t *testing.T, policy postgres.Policy, err error) {
	t.Helper()
	if err != nil || len(policy.Versions) != 2 {
		t.Fatalf("existing Java policy: %+v (%v)", policy, err)
	}
	first, second := policy.Versions[0], policy.Versions[1]
	if first.Number != 1 || first.TotalPremium != "123.45" ||
		!reflect.DeepEqual(
			first.Covers,
			[]postgres.Cover{{"C1", "100.00"}, {"C2", "23.45"}},
		) ||
		second.Number != 2 || second.TotalPremium != "3.71" ||
		!reflect.DeepEqual(
			second.Covers,
			[]postgres.Cover{{"C1", "3.01"}, {"C2", "0.70"}},
		) ||
		first.AccountNumber != second.AccountNumber ||
		second.CoverTo.Format(time.DateOnly) != "2026-11-23" ||
		second.VersionFrom.Format(time.DateOnly) != "2026-11-24" {
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
		covers = append(covers, postgres.Cover{Code: code, Price: offer.CoverPrices[code]})
	}
	return postgres.Issuance{
		Number: number, AgentLogin: "synthetic.agent",
		Version: postgres.Version{
			Number: 1, ProductCode: offer.ProductCode, FirstName: "Synthetic",
			LastName: "Issuer", Pesel: "00000000000",
			AccountNumber: "8a7b6c5d-4e3f-4a1b-9c8d-7e6f5a4b3c22",
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
